package db

import (
 "context"
 "fmt"
 "testing"
)

func TestLoadingReadFiltersAndCounts(t *testing.T) {
 d,err:=Open(testDSN(t)); if err!=nil {t.Fatal(err)}; defer d.Close()
 task,err:=d.CreateTask("loading fixture","",nil,0,0); if err!=nil {t.Fatal(err)}
 defer d.DeleteTask(task.ID)
 key:=fmt.Sprint(task.ID)
 defer d.Exec(`DELETE FROM intercept_pending WHERE task_id IN ($1,$2)`,key,key+"-other")
 for _,scope:=range []string{key,key+"-other"} {
  if _,err=d.CreateInterceptPending(0,0,scope,"test","test",nil,"synthetic");err!=nil {t.Fatal(err)}
 }
 if _,err=d.CreateDecidedIntercept(0,0,key,"test","test",nil,"allowed","synthetic");err!=nil {t.Fatal(err)}
 if count,err:=d.PendingInterceptCount(key);err!=nil || count!=1 {t.Fatalf("pending count=%d err=%v",count,err)}
 if rows,err:=d.ListPendingInterceptsForTask(key);err!=nil || len(rows)!=1 || rows[0].TaskID!=key {t.Fatalf("pending filtering failed: %v",err)}
 asset,err:=d.Assets().UpsertRootDomain(UpsertRootDomainReq{Domain:"loading-filter-"+key+".invalid",TaskID:task.ID});if err!=nil {t.Fatal(err)}
 defer d.Assets().DeleteByIDs([]int64{asset})
 store:=d.Exploration(task.ExplorationID)
 first,err:=store.AddNode(KindIntent,map[string]any{"summary":"one"},0,"running","test",[]int64{asset});if err!=nil {t.Fatal(err)}
 second,err:=store.AddNode(KindIntent,map[string]any{"summary":"two"},0,"open","test",[]int64{asset});if err!=nil {t.Fatal(err)}
 if _,err=store.AddNode(KindGoal,map[string]any{"text":"goal"},0,"met","test",nil);err!=nil {t.Fatal(err)}
 running,goals,met,err:=store.RuntimeCounts(context.Background());if err!=nil||running!=1||goals!=1||met!=1 {t.Fatalf("aggregate running=%d goals=%d met=%d err=%v",running,goals,met,err)}
 rows,err:=d.Assets().IntentAssetsForIDs(task.ID,[]int64{first.ID});if err!=nil||len(rows)!=1||rows[0].IntentID!=first.ID {t.Fatalf("intent filtering failed %v",err)}
 rows,err=d.Assets().IntentAssetsForIDs(task.ID,[]int64{});if err!=nil||len(rows)!=0 {t.Fatalf("empty selection leaked rows %v",err)}
 rows,err=d.Assets().IntentAssets(task.ID);if err!=nil||len(rows)!=2 {t.Fatalf("legacy list changed second=%d err=%v",second.ID,err)}
}
