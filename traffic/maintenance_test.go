package traffic

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestDeleteContextCancelsWriterWait(t *testing.T) {
	tr, _ := openTraffic(t)
	tr.wmu.Lock()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := tr.StageDeleteHostsExactContext(ctx, []string{"synthetic.example.test"})
		done <- err
	}()
	select {
	case err := <-done:
		tr.wmu.Unlock()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("writer wait error=%v", err)
		}
	case <-time.After(time.Second):
		tr.wmu.Unlock()
		<-done
		t.Fatal("取消没有释放等待流量写锁的删除请求")
	}
	stage, err := tr.StageDeleteHostsExact([]string{"synthetic.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestForegroundRecordPreemptsBlobGC(t *testing.T) {
	tr, _ := openTraffic(t)
	entered := make(chan struct{})
	tr.startBlobGC(func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("后台回收未开始")
	}
	done := make(chan struct{})
	go func() {
		tr.record(newFlow("foreground.example.test", "GET", "/probe", nil, []byte("synthetic response")))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		tr.cancelMaintenance()
		<-done
		t.Fatal("前台录制被后台回收阻塞")
	}
	tr.reaping.Wait()
	if count, err := tr.Count(); err != nil || count != 1 {
		t.Fatalf("record count=%d err=%v", count, err)
	}
}

func TestCloseCancelsBlobGC(t *testing.T) {
	tr, _ := openTraffic(t)
	entered := make(chan struct{})
	tr.startBlobGC(func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	})
	<-entered
	done := make(chan error, 1)
	go func() { done <- tr.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("关闭仍在等待后台回收")
	}
}

func TestLegacyBlobReferencesAcrossReadBoundary(t *testing.T) {
	hash := strings.Repeat("a", 64)
	file := filepath.Join(t.TempDir(), "request.http")
	content := strings.Repeat("x", 64*1024+60) + "@blob sha256:" + hash + " suffix"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	refs := map[string]struct{}{}
	marker := regexp.MustCompile(`@blob sha256:([0-9a-f]{64})`)
	if err := collectLegacyFileRefs(t.Context(), file, refs, marker); err != nil {
		t.Fatal(err)
	}
	if _, ok := refs[hash]; !ok || len(refs) != 1 {
		t.Fatal("跨读取块的旧 blob 引用丢失")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := collectLegacyFileRefs(ctx, file, map[string]struct{}{}, marker); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled reference scan=%v", err)
	}
}

func TestLegacyUncertainReferencesDoNotCollectBlobs(t *testing.T) {
	tr, dir := openTraffic(t)
	hash := strings.Repeat("c", 64)
	blob := filepath.Join(dir, "_blobs", "sha256", "cc", hash+".bin")
	if err := os.MkdirAll(filepath.Dir(blob), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blob, []byte("synthetic blob"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, "legacy.example.test", "GET", "synthetic")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "unavailable"), filepath.Join(legacy, "request.http")); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.DB().Exec(`INSERT INTO exchanges(id,host,path) VALUES('synthetic','legacy.example.test','legacy.example.test/GET/synthetic')`); err != nil {
		t.Fatal(err)
	}
	if err := tr.gcBlobsContext(t.Context()); err == nil {
		t.Fatal("引用无法完整确认时仍继续回收")
	}
	if _, err := os.Stat(blob); err != nil {
		t.Fatalf("不确定的历史引用导致 blob 被删除：%v", err)
	}
}
