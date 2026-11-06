package server

import (
	"strings"
	"testing"
)

func TestTaskBrowserRulesPreserveCustomPrompts(t *testing.T) {
	for _,role:=range []string{"worker","planner","mainagent"} {
		body:=taskBrowserPrompt(role,"用户自定义规范")
		if !strings.HasPrefix(body,"用户自定义规范") || !strings.Contains(body,"task_browser") {t.Fatal("policy not appended")}
		if again:=taskBrowserPrompt(role,body);again!=body {t.Fatal("policy duplicated")}
	}
	if got:=taskBrowserPrompt("auto","original");got!="original" {t.Fatal("unrelated role changed")}
}
