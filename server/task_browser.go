package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/taskbrowser"
	acperm "github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

func (s *Server) initTaskBrowsers() {
	if !taskbrowser.Supported { return }
	s.browsers = taskbrowser.New(s.dataDir, func(id string) bool {
		t, ok := s.m.Task(id)
		if !ok || s.engine.IsDeleting(id) {
			return false
		}
		v := t.lifecycleSnapshot()
		return !v.Paused && !v.Queued && !isTerminalStatus(v.Status)
	}, s.m.ProxyAddr)
	// 在恢复旧任务前回收旧版本残留，避免接管来源不明的浏览器会话。
	s.browsers.Reconcile()
	s.browserWG.Add(1)
	go func() {
		defer s.browserWG.Done()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-s.ctx.Done():
				s.browsers.Close()
				return
			case <-tick.C:
				s.browsers.Reconcile()
			}
		}
	}()
}

func taskBrowserTool(m *taskbrowser.Manager, id int64) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name:        "task_browser",
		Description: "获取或复用当前主任务唯一的 Chrome 实例及 CDP 地址。所有子任务必须连接此实例；禁止自行 launch Chrome。任务结束由服务端回收，不接受其他任务 ID。",
		Schema:      map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		Permissions: func(context.Context, json.RawMessage, acperm.Context) acperm.Decision { return acperm.Allowed() },
		Run: func(ctx context.Context, _ json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			v, err := m.Ensure(ctx, strconv.FormatInt(id, 10))
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			raw, _ := json.Marshal(v)
			return actool.Text(string(raw)), nil
		},
	})
}

// 每个执行会话独立连接 MCP，实际 Chrome 按主任务共享；仅首次调用才启动。
// 现有远端 browser MCP 只提供工具 schema，不再承载有 task_id 的浏览器操作。
type taskBrowserBridge struct {
	mu       sync.Mutex
	ctx      context.Context
	manager  *taskbrowser.Manager
	id       int64
	client   mcpClient
	tools    map[string]actool.CoreTool
	closed   bool
	endpoint string
}

func (b *taskBrowserBridge) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	if b.client != nil {
		return b.client.Close()
	}
	return nil
}

type taskBrowserMCPTool struct {
	actool.CoreTool
	bridge *taskBrowserBridge
}

func (t *taskBrowserMCPTool) Call(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	b := t.bridge
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return actool.Errorf("浏览器会话已结束"), nil
	}
	v, err := b.manager.Ensure(ctx, strconv.FormatInt(b.id, 10))
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	if b.client != nil && b.endpoint != v.Endpoint {
		b.client.Close()
		b.client = nil
		b.tools = nil
	}
	if b.client == nil {
		args, _ := json.Marshal([]string{"-c", `cd "$ARTEX_BROWSER_WORKDIR" && exec npx "$@"`, "artex-browser-mcp", "--no-install", "@playwright/mcp", "--cdp-endpoint", v.Endpoint, "--isolated"})
		env, _ := json.Marshal(map[string]string{"ARTEX_BROWSER_WORKDIR": b.manager.TaskDir(v.TaskID), "ARTEX_BROWSER_TASK": v.TaskID})
		cl, err := connectMCP(b.ctx, &db.MCPServer{Name: browserMCPName, Transport: "stdio", Command: "/bin/sh", Args: args, Env: env})
		if err != nil {
			return actool.Errorf(fmt.Sprintf("连接主任务浏览器失败: %v", err)), nil
		}
		tools, err := cl.Tools(ctx)
		if err != nil {
			cl.Close()
			return actool.Errorf("读取本地 browser MCP 工具失败"), nil
		}
		b.client = cl
		b.endpoint = v.Endpoint
		b.tools = map[string]actool.CoreTool{}
		for _, tool := range tools {
			b.tools[tool.Name()] = tool
		}
	}
	tool := b.tools[t.Name()]
	if tool == nil {
		return actool.Errorf("本地 browser MCP 未提供该工具"), nil
	}
	return tool.Call(ctx, in, tc)
}

// 固定提示词尾部应用于数据库自定义提示词，不覆盖用户现有正文。
const taskBrowserRules = `【浏览器资源规范】
同一主任务及其全部子任务最多共享一个 Chrome 浏览器实例；Chrome 的渲染、网络等子进程不算独立实例。
需要浏览器时优先使用 browser MCP，或调用 task_browser 获取 CDP 地址，再通过 Playwright connect_over_cdp / connectOverCDP 连接。不要调用 launch、launch_persistent_context、Chrome/Chromium 可执行文件或另外安装浏览器绕过管理。
不同子任务使用独立 BrowserContext/页面；自己创建的页面和上下文必须在 finally 中关闭。子任务结束不得调用 Browser.close、Browser.close CDP 命令或杀掉共享浏览器。
整个主任务完成、取消、暂停、超时或删除后，由服务端回收浏览器及其子进程；模型不需要也不得通过 pkill/killall 做全局清理。
浏览器连接断开时重新调用 task_browser 获取当前地址，禁止独立重启另一份浏览器。`

func taskBrowserPrompt(key, body string) string {
	switch key {
	case "worker", "planner", "mainagent":
		if !strings.Contains(body, "【浏览器资源规范】") {
			return body + "\n\n" + taskBrowserRules
		}
	}
	return body
}

func browserRunID(ctx context.Context) int64 { return agent.RunInfoFrom(ctx).TaskID }

// 一次性追加新提示词版本，保留原正文和版本历史；重复启动不重复写入。
func seedTaskBrowserRules(pg *db.DB) {
	for _, key := range []string{"worker", "planner", "mainagent"} {
		a, err := pg.GetAgentByKey(key)
		if err != nil || a == nil {
			continue
		}
		body, err := pg.CurrentPrompt(a.ID)
		if err != nil || body == "" {
			continue
		}
		updated := taskBrowserPrompt(key, body)
		if updated != body {
			_, _ = pg.SavePrompt(a.ID, updated, "增加主任务浏览器复用与服务端回收规范", "system")
		}
	}
}
