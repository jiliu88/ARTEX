package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/taskbrowser"
	actool "github.com/Autumn-27/norma/tool"
)

func TestTaskBrowserRulesPreserveCustomPrompts(t *testing.T) {
	for _, role := range []string{"worker", "planner", "mainagent"} {
		body := taskBrowserPrompt(role, "用户自定义规范")
		if !strings.HasPrefix(body, "用户自定义规范") || !strings.Contains(body, "task_browser") {
			t.Fatal("policy not appended")
		}
		if again := taskBrowserPrompt(role, body); again != body {
			t.Fatal("policy duplicated")
		}
	}
	if got := taskBrowserPrompt("auto", "original"); got != "original" {
		t.Fatal("unrelated role changed")
	}
}

func TestTaskBrowserMCPUsesManagedInstance(t *testing.T) {
	if os.Getenv("ARTEX_BROWSER_MCP_SMOKE") != "1" {
		t.Skip("需要 CI/部署环境的预装 browser MCP")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var active atomic.Bool
	active.Store(true)
	m := taskbrowser.New(t.TempDir(), func(string) bool { return active.Load() }, nil)
	defer m.Close()
	session, err := m.Ensure(ctx, "900002")
	if err != nil {
		t.Fatal(err)
	}
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<h1>synthetic browser lifecycle " + r.URL.Path + "</h1>"))
	}))
	defer page.Close()
	args, _ := json.Marshal([]string{"--cdp-endpoint", session.Endpoint, "--isolated"})
	metadata, err := connectMCP(ctx, &db.MCPServer{Name: browserMCPName, Command: "playwright-mcp", Transport: "stdio", Args: args})
	if err != nil {
		t.Fatal(err)
	}
	defer metadata.Close()
	tools, err := metadata.Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var navigate actool.CoreTool
	for _, tool := range tools {
		if strings.HasSuffix(tool.Name(), "browser_navigate") {
			navigate = tool
		}
	}
	if navigate == nil {
		t.Fatal("missing browser_navigate")
	}
	b1 := &taskBrowserBridge{ctx: ctx, manager: m, id: 900002}
	defer b1.Close()
	b2 := &taskBrowserBridge{ctx: ctx, manager: m, id: 900002}
	defer b2.Close()
	call := func(b *taskBrowserBridge, path string) {
		in, _ := json.Marshal(map[string]string{"url": page.URL + path})
		tool := &taskBrowserMCPTool{CoreTool: navigate, bridge: b}
		result, err := tool.Call(ctx, in, &actool.ToolContext{WorkingDir: m.TaskDir("900002")})
		if err != nil || result.IsError {
			t.Fatalf("MCP failed: %v %+v", err, result)
		}
	}
	call(b1, "/one")
	call(b2, "/two")
	if b1.endpoint != b2.endpoint || b1.endpoint != session.Endpoint {
		t.Fatal("workers did not share CDP")
	}
	_ = b1.Close()
	call(b2, "/still-alive")
	again, err := m.Ensure(ctx, "900002")
	if err != nil || again.PID != session.PID {
		t.Fatal("closing one worker restarted browser")
	}
	active.Store(false)
	m.Reconcile()
	if err := taskbrowser.Healthy(ctx, session.Endpoint); err == nil {
		t.Fatal("browser survived terminal task")
	}
}
