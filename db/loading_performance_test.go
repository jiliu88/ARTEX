package db

import (
	"os"
	"sort"
	"testing"
	"time"
)

const legacyContextCoverageCTE = directTaskContextCTE + taskAssetVisibilityCTE + `,
target AS (
  SELECT DISTINCT a.id, a.type,
         COALESCE(a.url, a.domain, a.ip, a.app_name, a.root_domain, '') AS label
  FROM assets a
  JOIN task_scope ts ON (
       (ts.kind='company'     AND a.company_id = ts.company_id)
    OR (ts.kind='root_domain' AND a.root_domain = ts.domain)
    OR (ts.kind='subdomain'   AND a.domain = ts.domain)
    OR (ts.kind IN ('ip','cidr') AND ts.net >>= try_inet(a.ip))
    OR (ts.kind='icp' AND (
         lower(regexp_replace(COALESCE(a.icp,''), '[[:space:]]+', '', 'g')) = ts.value
         OR lower(regexp_replace(COALESCE(a.app_icp,''), '[[:space:]]+', '', 'g')) = ts.value
       ))
  )
  JOIN context_tasks ctx ON ctx.task_id=ts.task_id
  WHERE a.id IN (SELECT id FROM visible_assets)
	UNION
	SELECT a.id, a.type, COALESCE(a.url,a.domain,a.ip,a.app_name,a.root_domain,'') AS label
	FROM assets a WHERE EXISTS (SELECT 1 FROM context_tasks ctx WHERE ctx.task_id=ANY(a.task_ids))
	UNION
	SELECT a.id, a.type,
	       COALESCE(a.url, a.domain, a.ip, a.app_name, a.root_domain, '') AS label
	FROM assets a
	JOIN exploration_anchors ea ON ea.asset_id=a.id
	JOIN exploration_nodes en ON en.id=ea.node_id
	JOIN context_tasks ctx ON ctx.exploration_id=en.exploration_id
),
tested AS (
  SELECT DISTINCT ea.asset_id
  FROM exploration_anchors ea
  JOIN exploration_nodes en ON en.id=ea.node_id AND en.kind='fact'
  JOIN context_tasks ctx ON ctx.exploration_id=en.exploration_id
)`

// 只在明确提供隔离 DSN 时运行，不探测本机或生产配置。
func TestLoadingScopeEquivalenceAndPerformance(t *testing.T) {
	if os.Getenv("ARTEX_PG_DSN") == "" {
		t.Fatal("requires isolated ARTEX_PG_DSN")
	}
	d, err := Open(os.Getenv("ARTEX_PG_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fixture := `
 CREATE TEMP TABLE tasks(id bigint,exploration_id bigint,deleted_at timestamptz,share_global_assets boolean DEFAULT false);
 CREATE TEMP TABLE task_relations(task_id bigint,source_task_id bigint);
 CREATE TEMP TABLE task_scope(id bigint,task_id bigint,kind text,company_id bigint,domain text,net cidr,value text);
 CREATE TEMP TABLE assets(id bigint,type text,url text,domain text,ip text,app_name text,root_domain text,company_id bigint,icp text,app_icp text,task_ids bigint[] DEFAULT '{}');
 CREATE TEMP TABLE exploration_nodes(id bigint,exploration_id bigint,kind text);
 CREATE TEMP TABLE exploration_anchors(node_id bigint,asset_id bigint);
 INSERT INTO tasks VALUES(1,1,NULL,true),(2,2,NULL,false),(3,3,now(),false),(4,4,NULL,false);
 INSERT INTO task_relations VALUES(1,2),(1,3),(2,4);
 INSERT INTO assets SELECT g,'subdomain',NULL,'sub'||g||'.example',CASE WHEN g%3=0 THEN '192.0.2.1' WHEN g%3=1 THEN '2001:db8::1' ELSE 'invalid' END,NULL,'root'||(g%1000)||'.example',g%20,' ICP '||g%15,'APP'||g%10,'{}'::bigint[] FROM generate_series(1,20000) g;
 INSERT INTO task_scope SELECT g,CASE WHEN g<100 THEN 1 WHEN g<150 THEN 2 ELSE 4 END,'root_domain',NULL,'root'||g||'.example',NULL,NULL FROM generate_series(1,1000) g;
 INSERT INTO task_scope VALUES(1001,1,'cidr',NULL,NULL,'192.0.2.0/24',NULL),(1002,2,'ip',NULL,NULL,'2001:db8::1/128',NULL),(1003,1,'icp',NULL,NULL,NULL,'icp1'),(1004,2,'icp',NULL,NULL,NULL,'app1'),(1005,3,'company',5,NULL,NULL,NULL),(1006,1,'subdomain',NULL,'sub1.example',NULL,NULL),(1007,1,'company',1,NULL,NULL,NULL);
 INSERT INTO exploration_nodes VALUES(1,1,'fact'),(2,2,'fact'),(3,3,'fact'),(4,4,'fact');
 INSERT INTO exploration_anchors VALUES(1,19999),(2,19998),(3,19997),(4,19996);
 CREATE INDEX ON assets(root_domain); CREATE INDEX ON assets(domain); CREATE INDEX ON assets(company_id); CREATE INDEX ON assets USING GIN(task_ids);
 CREATE INDEX ON task_scope(task_id); ANALYZE assets; ANALYZE task_scope;`
	if _, err = tx.Exec(fixture); err != nil {
		t.Fatal(err)
	}
	fingerprint := func(cte string) string {
		var hash string
		if err := tx.QueryRow("WITH "+cte+" SELECT md5(string_agg(id::text||':'||type||':'||label,',' ORDER BY id)) FROM target", 1).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		return hash
	}
	if fingerprint(legacyContextCoverageCTE) != fingerprint(contextCoverageCTE) {
		t.Fatal("scope membership/provenance changed")
	}
	// 关闭共享时仍须与上游规则等价，且直接来源的共享权限不得传递。
	if _, err := tx.Exec(`UPDATE tasks SET share_global_assets=(id=2); UPDATE assets SET task_ids=ARRAY[CASE WHEN id%2=0 THEN 1 ELSE 2 END]::bigint[] WHERE id<=100`); err != nil {
		t.Fatal(err)
	}
	if fingerprint(legacyContextCoverageCTE) != fingerprint(contextCoverageCTE) {
		t.Fatal("isolated scope membership/provenance changed")
	}
	if _, err := tx.Exec(`UPDATE tasks SET share_global_assets=(id=1); UPDATE assets SET task_ids='{}'::bigint[]`); err != nil {
		t.Fatal(err)
	}
	median := func(cte string) time.Duration {
		_ = fingerprint(cte)
		times := []time.Duration{}
		for range 5 {
			start := time.Now()
			_ = fingerprint(cte)
			times = append(times, time.Since(start))
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		return times[2]
	}
	oldTime, newTime := median(legacyContextCoverageCTE), median(contextCoverageCTE)
	t.Logf("assets=20000 scopes=1007 old_ms=%d new_ms=%d improvement_percent=%.2f", oldTime.Milliseconds(), newTime.Milliseconds(), 100*(1-float64(newTime)/float64(oldTime)))
	if newTime > oldTime*3/10 || newTime > 500*time.Millisecond {
		t.Fatalf("query budget failed old=%v new=%v", oldTime, newTime)
	}
}
