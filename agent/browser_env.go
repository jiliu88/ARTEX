package agent

import (
	"strconv"
	"path/filepath"
)

// 标记整个任务命令树，子进程切换工作目录后仍可由服务端归属和回收。
func taskBrowserEnv(env []string, taskID int64, dataDir string) []string {
	if taskID>0 {
		root,_:=filepath.Abs(filepath.Join(dataDir,"tasks"))
		env=append(append([]string{},env...),"ARTEX_BROWSER_TASK="+strconv.FormatInt(taskID,10),"ARTEX_BROWSER_ROOT="+root)
	}
	return env
}
