//go:build linux

package taskbrowser

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const Supported = true

func prepareProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

type proc struct {
	pid, parent, group int
	start, task, name  string
}

func taskFromPath(root, cwd string) string {
	rel, err := filepath.Rel(root, strings.TrimSuffix(cwd, " (deleted)"))
	if err != nil {
		return ""
	}
	id := strings.Split(rel, string(filepath.Separator))[0]
	if validID(id) {
		return id
	}
	return ""
}

func processInfo(pid int, root string) (proc, bool) {
	base := "/proc/" + strconv.Itoa(pid)
	raw, err := os.ReadFile(base + "/stat")
	if err != nil {
		return proc{}, false
	}
	text := string(raw)
	a, b := strings.Index(text, "("), strings.LastIndex(text, ")")
	if a < 0 || b <= a {
		return proc{}, false
	}
	f := strings.Fields(text[b+1:])
	if len(f) < 20 || f[0] == "Z" {
		return proc{}, false
	}
	p := proc{pid: pid, name: text[a+1 : b], start: f[19]}
	if exe, err := os.Readlink(base + "/exe"); err == nil && browserProcess(filepath.Base(strings.TrimSuffix(exe," (deleted)"))) {
		p.name = filepath.Base(strings.TrimSuffix(exe," (deleted)"))
	}
	p.parent, _ = strconv.Atoi(f[1])
	p.group, _ = strconv.Atoi(f[2])
	// 限定当前进程的 UID 与 cgroup，避免影响同机其他容器和用户。
	selfCG, _ := os.ReadFile("/proc/self/cgroup")
	cg, err := os.ReadFile(base + "/cgroup")
	if err != nil || string(cg) != string(selfCG) {
		return proc{}, false
	}
	fi, err := os.Stat(base)
	if err != nil {
		return proc{}, false
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return proc{}, false
	}
	cwd, _ := os.Readlink(base + "/cwd")
	p.task = taskFromPath(root, cwd)
	if p.task == "" {
		env, _ := os.ReadFile(base + "/environ")
		var task, ownerRoot string
		for _, v := range strings.Split(string(env), "\x00") {
			if strings.HasPrefix(v, "ARTEX_BROWSER_TASK=") {
				task = strings.TrimPrefix(v, "ARTEX_BROWSER_TASK=")
			}
			if strings.HasPrefix(v, "ARTEX_BROWSER_ROOT=") {
				ownerRoot = strings.TrimPrefix(v, "ARTEX_BROWSER_ROOT=")
			}
		}
		if ownerRoot == root && validID(task) {
			p.task = task
		}
	}
	return p, true
}

func browserProcess(name string) bool {
	if strings.HasPrefix(name, "chrome.artex-r") || strings.HasPrefix(name, "headless_shell.") {
		return true
	}
	switch name {
	case "chrome", "chromium", "chromium-browse", "headless_shell", "chrome-headless", "chrome_crashpad", "Xvfb":
		return true
	}
	return false
}

func processes(root string) map[int]proc {
	all := map[int]proc{}
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if p, ok := processInfo(pid, root); ok {
			all[pid] = p
		}
	}
	return all
}

func processTaskIDs(root string) []string {
	seen := map[string]bool{}
	for _, p := range processes(root) {
		if p.task != "" && browserProcess(p.name) {
			seen[p.task] = true
		}
	}
	out := []string{}
	for id := range seen {
		out = append(out, id)
	}
	return out
}

func belongsTo(p proc, keep int, all map[int]proc) bool {
	if keep == 0 {
		return false
	}
	for steps := 0; steps < 128; steps++ {
		if p.pid == keep || p.group == keep {
			return true
		}
		next, ok := all[p.parent]
		if !ok || next.pid == p.pid {
			return false
		}
		p = next
	}
	return false
}

func terminateTask(root, id string, keep int) {
	all := processes(root)
	var fds []int
	for _, p := range all {
		if p.task != id || !browserProcess(p.name) || belongsTo(p, keep, all) {
			continue
		}
		fd, err := unix.PidfdOpen(p.pid, 0)
		if err != nil {
			continue
		}
		// pidfd 打开后重新核验启动时间，防止 PID 复用误杀。
		now, ok := processInfo(p.pid, root)
		if !ok || now.start != p.start || now.task != id {
			unix.Close(fd)
			continue
		}
		_ = unix.PidfdSendSignal(fd, unix.SIGTERM, nil, 0)
		fds = append(fds, fd)
	}
	if len(fds) > 0 {
		time.Sleep(200 * time.Millisecond)
		log.Printf("[browser] task=%s reclaimed_processes=%d",id,len(fds))
	}
	for _, fd := range fds {
		_ = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
		_ = unix.Close(fd)
	}
}
