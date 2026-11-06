// Package taskbrowser 管理主任务唯一的浏览器实例，生命周期独立于 Worker。
package taskbrowser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Session struct {
	TaskID   string `json:"task_id"`
	Endpoint string `json:"cdp_endpoint"`
	PID      int    `json:"pid"`
}

type instance struct {
	Session
	cmd     *exec.Cmd
	done    chan struct{}
	profile string
}

type Manager struct {
	mu        sync.Mutex
	root      string
	instances map[string]*instance
	allowed   func(string) bool
	proxy     func() string
	closed    bool
	// 测试可替换启动器，生产只使用预装的 Chrome/Chromium。
	command func(string, string) (*exec.Cmd, error)
}

func New(dataDir string, allowed func(string) bool, proxy func() string) *Manager {
	abs, _ := filepath.Abs(dataDir)
	m := &Manager{root: filepath.Join(abs, "tasks"), instances: map[string]*instance{}, allowed: allowed, proxy: proxy}
	m.command = m.browserCommand
	return m
}

func validID(id string) bool {
	n, err := strconv.ParseInt(id, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == id
}

func (m *Manager) TaskDir(id string) string { return filepath.Join(m.root, id) }

// Ensure 在同一把锁下检查、启动和发布实例。并发 Worker 不能各自启动浏览器。
func (m *Manager) Ensure(ctx context.Context, id string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validID(id) || m.closed || (m.allowed != nil && !m.allowed(id)) {
		return Session{}, errors.New("任务已结束、暂停或不可运行，拒绝创建浏览器")
	}
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	if old := m.instances[id]; old != nil {
		select {
		case <-old.done:
			m.stopLocked(id)
		default:
			return old.Session, nil
		}
	}
	dir := filepath.Join(m.root, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return Session{}, err
	}
	profile, err := os.MkdirTemp(dir, ".browser-")
	if err != nil {
		return Session{}, err
	}
	cmd, err := m.command(id, profile)
	if err != nil {
		os.RemoveAll(profile)
		return Session{}, err
	}
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "ARTEX_BROWSER_TASK="+id, "ARTEX_BROWSER_ROOT="+m.root)
	prepareProcess(cmd)
	// 不记录浏览器输出，避免 URL、Cookie 等进入应用日志。
	if err := cmd.Start(); err != nil {
		os.RemoveAll(profile)
		return Session{}, err
	}
	i := &instance{Session: Session{TaskID: id, PID: cmd.Process.Pid}, cmd: cmd, done: make(chan struct{}), profile: profile}
	m.instances[id] = i
	go func() { _ = cmd.Wait(); close(i.done) }()
	ready, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if raw, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort")); err == nil {
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if len(lines) == 2 {
				port, err := strconv.Atoi(lines[0])
				if err == nil && port > 0 && port < 65536 {
					i.Endpoint = fmt.Sprintf("http://127.0.0.1:%d", port)
					if m.allowed != nil && !m.allowed(id) {
						m.stopLocked(id)
						return Session{}, errors.New("任务已结束")
					}
					return i.Session, nil
				}
			}
		}
		select {
		case <-ready.Done():
			m.stopLocked(id)
			return Session{}, fmt.Errorf("浏览器启动超时或已取消: %w", ready.Err())
		case <-i.done:
			m.stopLocked(id)
			return Session{}, errors.New("浏览器在就绪前退出")
		case <-tick.C:
		}
	}
}

func (m *Manager) browserCommand(id, profile string) (*exec.Cmd, error) {
	path := os.Getenv("ARTEX_BROWSER_EXECUTABLE")
	if path == "" {
		candidates := []string{"/opt/google/chrome/chrome.artex-real"}
		for _, pattern := range []string{"/root/.cache/ms-playwright/chromium-*/chrome-linux*/chrome.artex-real", "/ms-playwright/chromium-*/chrome-linux*/chrome.artex-real"} {
			matches, _ := filepath.Glob(pattern)
			candidates = append(candidates, matches...)
		}
		for _, candidate := range candidates {
			if info, err := os.Stat(candidate); err == nil && info.Mode()&0111 != 0 {
				path = candidate
				break
			}
		}
	}
	if path == "" {
		return nil, errors.New("未安装受管 Chrome；请运行镜像中的 install-browser-guard.sh")
	}
	args := []string{"--headless=new", "--no-sandbox", "--disable-dev-shm-usage", "--no-first-run", "--no-default-browser-check", "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0", "--user-data-dir=" + profile, "about:blank"}
	if m.proxy != nil {
		if p := m.proxy(); p != "" {
			args = append(args, "--proxy-server="+p)
		}
	}
	return exec.Command(path, args...), nil
}

func (m *Manager) stopLocked(id string) {
	i := m.instances[id]
	// 即使主进程已经退出，也按任务归属清理遗留的渲染器、crashpad 和 Xvfb。
	terminateTask(m.root, id, 0)
	if i != nil {
		_ = i.cmd.Process.Kill()
		select {
		case <-i.done:
		case <-time.After(2 * time.Second):
		}
		_ = os.RemoveAll(i.profile)
		delete(m.instances, id)
	}
}

func (m *Manager) Stop(id string) { m.mu.Lock(); defer m.mu.Unlock(); m.stopLocked(id) }

// Reconcile 也清理旧版本、手工启动、逃离父进程的浏览器；不依赖模型收尾。
func (m *Manager) Reconcile() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, i := range m.instances {
		dead := false
		select {
		case <-i.done:
			dead = true
		default:
		}
		if dead || m.closed || (m.allowed != nil && !m.allowed(id)) {
			m.stopLocked(id)
		}
	}
	for _, id := range processTaskIDs(m.root) {
		keep := 0
		if i := m.instances[id]; i != nil {
			keep = i.PID
		}
		terminateTask(m.root, id, keep)
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for id := range m.instances {
		m.stopLocked(id)
	}
	for _, id := range processTaskIDs(m.root) {
		terminateTask(m.root, id, 0)
	}
}

// Healthy 用于合成验收，不访问任何目标站点。
func Healthy(ctx context.Context, endpoint string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/json/version", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var info map[string]any
	if resp.StatusCode != 200 {
		return fmt.Errorf("CDP HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return err
	}
	if _, ok := info["webSocketDebuggerUrl"]; !ok {
		return errors.New("CDP 未就绪")
	}
	return nil
}
