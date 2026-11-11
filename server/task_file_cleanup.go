package server

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// 数据库提交后的文件先移到已提交回收区，物理删除不再占住 HTTP 响应。
func (m *Manager) commitTaskFiles(stage *taskFileDeleteStage) error {
	if stage == nil || stage.done {
		return nil
	}
	parent := filepath.Join(m.dir, ".delete-trash")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	destination := filepath.Join(parent, filepath.Base(stage.stageDir))
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		if err != nil {
			return err
		}
		return os.ErrExist
	}
	if err := os.Rename(stage.stageDir, destination); err != nil {
		return err
	}
	stage.done = true
	m.reapTaskFiles(destination)
	return nil
}

func (m *Manager) reapTaskFiles(path string) {
	ctx := m.fileCleanupCtx
	if ctx == nil {
		ctx = context.Background()
	}
	m.fileReaping.Go(func() {
		if err := removeCommittedFiles(ctx, path); err != nil && ctx.Err() == nil {
			log.Printf("[task-delete] 已提交文件回收未完成：%v", err)
		}
	})
}

func (m *Manager) resumeTaskFileCleanup() {
	parent := filepath.Join(m.dir, ".delete-trash")
	entries, err := os.ReadDir(parent)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[task-delete] 读取已提交回收区失败：%v", err)
		}
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "task-") {
			m.reapTaskFiles(filepath.Join(parent, entry.Name()))
		}
	}
}

func removeCommittedFiles(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		child := filepath.Join(path, entry.Name())
		if entry.IsDir() {
			err = removeCommittedFiles(ctx, child)
		} else {
			err = os.Remove(child)
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Remove(path)
}
