//go:build linux

package taskbrowser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// 合成子进程只提供回环 CDP 就绪响应，不访问目标网络或业务数据。
func TestBrowserProcessHelper(t *testing.T) {
	args := os.Args
	if len(args) < 3 || args[len(args)-2] != "--" {
		return
	}
	if err := os.WriteFile("/proc/self/comm", []byte("chrome"), 0600); err != nil {
		os.Exit(2)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(2)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(filepath.Join(args[len(args)-1], "DevToolsActivePort"), []byte(fmt.Sprintf("%d\n/devtools/browser/synthetic\n", port)), 0600); err != nil {
		os.Exit(2)
	}
	_ = http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"webSocketDebuggerUrl":"ws://127.0.0.1/synthetic"}`))
	}))
}

func fakeManager(t *testing.T, allowed func(string) bool) (*Manager, *atomic.Int32) {
	t.Helper()
	m := New(t.TempDir(), allowed, nil)
	starts := &atomic.Int32{}
	m.command = func(id, profile string) (*exec.Cmd, error) {
		starts.Add(1)
		return exec.Command(os.Args[0], "-test.run=^TestBrowserProcessHelper$", "--", profile), nil
	}
	t.Cleanup(m.Close)
	return m, starts
}

func TestConcurrentWorkersShareOnlyOneBrowser(t *testing.T) {
	m, starts := fakeManager(t, nil)
	var wg sync.WaitGroup
	pids := make(chan int, 32)
	for n := 0; n < 32; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := m.Ensure(context.Background(), "16")
			if err != nil {
				t.Error(err)
				return
			}
			pids <- s.PID
		}()
	}
	wg.Wait()
	close(pids)
	pid := 0
	for p := range pids {
		if pid != 0 && p != pid {
			t.Fatal("duplicate browser")
		}
		pid = p
	}
	if starts.Load() != 1 {
		t.Fatalf("starts=%d", starts.Load())
	}
	other, err := m.Ensure(context.Background(), "17")
	if err != nil {
		t.Fatal(err)
	}
	if other.PID == pid || starts.Load() != 2 {
		t.Fatal("task isolation failed")
	}
	m.Stop("16")
	if _, ok := processInfo(pid, m.root); ok {
		t.Fatal("browser survived stop")
	}
	if _, ok := processInfo(other.PID, m.root); !ok {
		t.Fatal("another task was killed")
	}
}

func TestTerminalPauseDeleteAndShutdownCleanup(t *testing.T) {
	for _, reason := range []string{"done", "timeout", "paused", "deleted", "shutdown"} {
		t.Run(reason, func(t *testing.T) {
			var active atomic.Bool
			active.Store(true)
			m, _ := fakeManager(t, func(string) bool { return active.Load() })
			s, err := m.Ensure(context.Background(), "16")
			if err != nil {
				t.Fatal(err)
			}
			active.Store(false)
			if reason == "shutdown" {
				m.Close()
			} else {
				m.Reconcile()
			}
			if _, ok := processInfo(s.PID, m.root); ok {
				t.Fatal("terminal browser survived")
			}
			if _, err := m.Ensure(context.Background(), "16"); err == nil {
				t.Fatal("terminal task reopened browser")
			}
			if matches, _ := filepath.Glob(filepath.Join(m.root, "16", ".browser-*")); len(matches) != 0 {
				t.Fatal("profile survived cleanup")
			}
		})
	}
}

func TestCancelledStartupAndInvalidTask(t *testing.T) {
	m, starts := fakeManager(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Ensure(ctx, "16"); err == nil {
		t.Fatal("cancel ignored")
	}
	for _, id := range []string{"0", "-1", "../16", "016", "x"} {
		if _, err := m.Ensure(context.Background(), id); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	if starts.Load() != 0 {
		t.Fatal("invalid request launched browser")
	}
}

func TestReconcileKillsUnregisteredAndRestartOrphans(t *testing.T) {
	m, _ := fakeManager(t, nil)
	s, err := m.Ensure(context.Background(), "16")
	if err != nil {
		t.Fatal(err)
	}
	profile := t.TempDir()
	rogue := exec.Command(os.Args[0], "-test.run=^TestBrowserProcessHelper$", "--", profile)
	rogue.Dir = profile // 故意离开任务目录，依然通过继承的任务标记归属。
	rogue.Env = append(os.Environ(), "ARTEX_BROWSER_TASK=16", "ARTEX_BROWSER_ROOT="+m.root)
	prepareProcess(rogue)
	if err := rogue.Start(); err != nil {
		t.Fatal(err)
	}
	defer rogue.Process.Kill()
	for n := 0; n < 100; n++ {
		if _, err := os.Stat(filepath.Join(profile, "DevToolsActivePort")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	m.Reconcile()
	_ = rogue.Wait()
	if _, ok := processInfo(s.PID, m.root); !ok {
		t.Fatal("registered instance killed")
	}
	// 新管理器无实例注册，必须清理上一代留下的浏览器。
	restarted := New(filepath.Dir(m.root), nil, nil)
	restarted.Reconcile()
	select {
	case <-m.instances["16"].done:
	case <-time.After(3 * time.Second):
		t.Fatal("restart orphan survived")
	}
}

func TestTaskPathBoundary(t *testing.T) {
	root := "/data/tasks"
	for cwd, want := range map[string]string{"/data/tasks/16/i2": "16", "/data/tasks/16": "16", "/data/tasks-other/16": "", "/data/tasks/../16": "", "/data/tasks/16/i2 (deleted)": "16"} {
		if got := taskFromPath(root, cwd); got != want {
			t.Fatalf("%q: %q", cwd, got)
		}
	}
}

func TestRealBrowserLifecycle(t *testing.T) {
	if os.Getenv("ARTEX_BROWSER_EXECUTABLE") == "" {
		t.Skip("CI 与部署验收显式指定预装 Chrome")
	}
	var active atomic.Bool
	active.Store(true)
	m := New(t.TempDir(), func(string) bool { return active.Load() }, nil)
	defer m.Close()
	var startupLog bytes.Buffer
	launch := m.command
	m.command = func(id, profile string) (*exec.Cmd, error) {
		cmd, err := launch(id, profile)
		if err == nil {
			cmd.Stderr = &startupLog
		}
		return cmd, err
	}
	// 生命周期验收包含启动、上下文和回收，不能复用仅启动阶段的 30 秒预算。
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s, err := m.Ensure(ctx, "900001")
	if err != nil {
		t.Fatalf("%v; synthetic Chrome startup: %s", err, startupLog.String())
	}
	if err := Healthy(ctx, s.Endpoint); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := m.Ensure(ctx, "900001")
			if e != nil || v.PID != s.PID {
				t.Errorf("not reused: %v", e)
			}
		}()
	}
	wg.Wait()
	resp, err := http.Get(s.Endpoint + "/json/version")
	if err != nil {
		t.Fatal(err)
	}
	var version struct {
		URL string `json:"webSocketDebuggerUrl"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&version)
	resp.Body.Close()
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, version.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	// 两个子任务各自使用隔离上下文，均位于同一浏览器主进程。
	for n := 1; n <= 2; n++ {
		if err := ws.WriteJSON(map[string]any{"id": n, "method": "Target.createBrowserContext"}); err != nil {
			t.Fatal(err)
		}
		var result struct {
			ID     int            `json:"id"`
			Result map[string]any `json:"result"`
			Error  any            `json:"error"`
		}
		_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
		if err := ws.ReadJSON(&result); err != nil {
			t.Fatal(err)
		}
		if result.ID != n || result.Error != nil || result.Result["browserContextId"] == nil {
			t.Fatalf("context %d failed", n)
		}
	}
	m.Reconcile()
	if err := Healthy(ctx, s.Endpoint); err != nil {
		t.Fatal("reaper killed live managed browser", err)
	}
	active.Store(false)
	m.Reconcile()
	if _, ok := processInfo(s.PID, m.root); ok {
		t.Fatal("real browser survived task end")
	}
	if ids := processTaskIDs(m.root); len(ids) != 0 {
		t.Fatal("real browser children survived", ids)
	}
	t.Log(fmt.Sprintf("real Chrome singleton and lifecycle passed; concurrency=%s", strconv.Itoa(16)))
}
