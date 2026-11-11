package traffic

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"regexp"
	"sync"
	"time"
)

// 前台写入可以取消后台回收，避免历史流量扫描占住删除、录制和关闭流程。
func (t *Traffic) cancelMaintenance() {
	t.maintenanceMu.Lock()
	defer t.maintenanceMu.Unlock()
	if t.blobGCCancel != nil {
		t.blobGCCancel()
	}
	if t.reclaimCancel != nil {
		t.reclaimCancel()
	}
}

func lockTrafficWriter(ctx context.Context, mu *sync.Mutex, interrupt func()) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if interrupt != nil {
			interrupt()
		}
		if mu.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (t *Traffic) lockForMutation() {
	_ = lockTrafficWriter(context.Background(), &t.wmu, t.cancelMaintenance)
}

func (t *Traffic) scheduleBlobGC() {
	t.startBlobGC(t.gcBlobsContext)
}

// 分块读取旧引用，保留跨块的哈希标记，并响应取消。
func collectLegacyFileRefs(ctx context.Context, path string, refs map[string]struct{}, marker *regexp.Regexp) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	buffer := make([]byte, 64*1024+80)
	tail := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := file.Read(buffer[tail:])
		data := buffer[:tail+n]
		for _, match := range marker.FindAllSubmatch(data, -1) {
			refs[string(match[1])] = struct{}{}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		tail = min(80, len(data))
		copy(buffer, data[len(data)-tail:])
	}
}

// 回收失败只会延后释放磁盘空间，不应让已提交的任务删除一直等待。
func (t *Traffic) startBlobGC(collect func(context.Context) error) {
	if t.stopping() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.maintenanceMu.Lock()
	if t.blobGCCancel != nil {
		t.blobGCCancel()
	}
	t.blobGCSerial++
	serial := t.blobGCSerial
	t.blobGCCancel = cancel
	t.maintenanceMu.Unlock()
	t.reaping.Go(func() {
		defer cancel()
		defer func() {
			t.maintenanceMu.Lock()
			if t.blobGCSerial == serial {
				t.blobGCCancel = nil
			}
			t.maintenanceMu.Unlock()
		}()
		if err := lockTrafficWriter(ctx, &t.wmu, nil); err != nil {
			return
		}
		defer t.wmu.Unlock()
		if err := collect(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			log.Printf("[traffic] 后台 blob 回收未完成：%v", err)
		}
	})
}
