package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestTaskDeleteBulkChildrenBeforeNodes(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	first, err := d.CreateTask("bulk delete", "synthetic graph", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.CreateTask("retain references", "synthetic survivor", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	function := fmt.Sprintf("test_bulk_delete_%d", first.ID)
	t.Cleanup(func() {
		_, _ = d.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON exploration_nodes; DROP FUNCTION IF EXISTS %s()`, function, function))
		_ = d.DeleteTask(first.ID)
		_ = d.DeleteTask(second.ID)
	})
	if _, err := d.Exec(`INSERT INTO exploration_nodes(exploration_id,kind,state)
SELECT $1,'fact','confirmed' FROM generate_series(1,1000)`, first.ExplorationID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO activity(exploration_id,node_id,kind)
SELECT $1,id,'tool_result' FROM exploration_nodes CROSS JOIN generate_series(1,20)
WHERE exploration_id=$1`, first.ExplorationID); err != nil {
		t.Fatal(err)
	}
	var nodeID int64
	if err := d.QueryRow(`SELECT min(id) FROM exploration_nodes WHERE exploration_id=$1`, first.ExplorationID).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO activity(exploration_id,node_id,kind) VALUES ($1,$2,'tool_result')`, second.ExplorationID, nodeID); err != nil {
		t.Fatal(err)
	}
	var findingID int64
	if err := d.QueryRow(`INSERT INTO findings(task_id,node_id,name) VALUES($1,$2,'retained finding') RETURNING id`, first.ID, nodeID).Scan(&findingID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM findings WHERE id=$1`, findingID) })
	// 若回归成先删节点，触发器立即报错；无须依赖机器速度判断执行顺序。
	if _, err := d.Exec(fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.exploration_id=%d AND EXISTS(SELECT 1 FROM activity WHERE exploration_id=OLD.exploration_id) THEN
  RAISE EXCEPTION 'activity must be removed before graph nodes';
 END IF;
 RETURN OLD;
END $$;
CREATE TRIGGER %s BEFORE DELETE ON exploration_nodes FOR EACH ROW EXECUTE FUNCTION %s()`, function, first.ExplorationID, function, function)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := d.DeleteTask(first.ID); err != nil {
		t.Fatal(err)
	}
	t.Logf("nodes=1000 activity_rows=20020 delete_ms=%d", time.Since(started).Milliseconds())
	var remaining, foreignActivity, retainedFinding int
	if err := d.QueryRow(`SELECT count(*) FROM activity WHERE exploration_id=$1`, first.ExplorationID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("owned activity=%d err=%v", remaining, err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM activity WHERE exploration_id=$1 AND node_id IS NULL`, second.ExplorationID).Scan(&foreignActivity); err != nil || foreignActivity != 1 {
		t.Fatalf("foreign activity=%d err=%v", foreignActivity, err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM findings WHERE id=$1 AND task_id IS NULL AND node_id IS NULL`, findingID).Scan(&retainedFinding); err != nil || retainedFinding != 1 {
		t.Fatalf("retained finding=%d err=%v", retainedFinding, err)
	}
}

func TestTaskDeleteCancellationReleasesLocks(t *testing.T) {
	dsn := testDSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTask("canceled delete", "synthetic lock", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	blocker, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err := blocker.Exec(`LOCK TABLE assets IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	deleter, pid := openTaskDeleteTestDB(t, dsn)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := deleter.DeleteTaskCascadePrepared(ctx, task.ID, true, false, false, nil)
		done <- err
	}()
	if err := waitForTaskDeleteBlock(d, pid, done); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := waitForTaskDeleteResult(done); err == nil {
		t.Fatal("canceled deletion unexpectedly succeeded")
	}
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, err := d.GetTask(task.ID); err != nil || got == nil {
		t.Fatalf("canceled deletion lost task: err=%v", err)
	}
	// 取消必须释放任务行锁，后续重试才能正常提交。
	if err := d.DeleteTask(task.ID); err != nil {
		t.Fatal(err)
	}
}
