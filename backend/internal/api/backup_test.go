package api

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/backup"
	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
)

// newBackupManager 在独立数据目录里准备一份可备份的数据库。
func newBackupManager(t *testing.T, dataDir string) *backup.Manager {
	t.Helper()
	handle, err := db.Open(filepath.Join(dataDir, "sesegraph.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := db.Migrate(context.Background(), handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if _, err := handle.ExecContext(context.Background(),
		`INSERT INTO settings (key, value) VALUES ('probe', 'backup-ok')`); err != nil {
		t.Fatalf("写入数据失败: %v", err)
	}
	return backup.New(backup.Options{DataDir: dataDir, Handle: handle})
}

// backupHandle 打开测试库并写入一行数据。
func backupHandle(t *testing.T, dataDir string) *sql.DB {
	t.Helper()
	handle, err := db.Open(filepath.Join(dataDir, "sesegraph.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := db.Migrate(context.Background(), handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return handle
}

// blockingSender 首次发送时阻塞，用于验证并发触发被拒绝；后续发送立即返回。
type blockingSender struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (b *blockingSender) SendDocument(context.Context, string, string) error {
	first := false
	b.once.Do(func() { first = true })
	if first {
		close(b.started)
		<-b.release
	}
	return nil
}

func TestBackupStatusWithoutBackups(t *testing.T) {
	dataDir := t.TempDir()
	manager := newBackupManager(t, dataDir)
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		deps.Backup = manager
		deps.BackupSchedule = "每天 03:00"
	})
	cookie := env.setup(t)

	rec := env.do(t, http.MethodGet, "/api/backup/status", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"enabled":true`) || !strings.Contains(body, "每天 03:00") {
		t.Errorf("响应 = %s，期望包含定时说明", body)
	}
	if !strings.Contains(body, `"latest":null`) {
		t.Errorf("响应 = %s，期望 latest 为空", body)
	}

	if rec := env.do(t, http.MethodGet, "/api/backup/download", "", cookie); rec.Code != http.StatusNotFound {
		t.Errorf("无备份时下载状态 = %d，期望 404", rec.Code)
	}
}

func TestBackupTriggerAndDownload(t *testing.T) {
	dataDir := t.TempDir()
	manager := newBackupManager(t, dataDir)
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		deps.Backup = manager
	})
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPost, "/api/backup/trigger", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("触发状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "sesegraph-") || !strings.Contains(body, `"sent":false`) {
		t.Errorf("响应 = %s，期望备份名与未发送标记", body)
	}

	download := env.do(t, http.MethodGet, "/api/backup/download", "", cookie)
	if download.Code != http.StatusOK {
		t.Fatalf("下载状态码 = %d，期望 200", download.Code)
	}
	if disposition := download.Header().Get("Content-Disposition"); !strings.Contains(disposition, "attachment") {
		t.Errorf("Content-Disposition = %q，期望附件下载", disposition)
	}
	if download.Body.Len() == 0 {
		t.Error("下载内容为空")
	}
	if cache := download.Header().Get("Cache-Control"); cache != "no-store" {
		t.Errorf("Cache-Control = %q，期望 no-store", cache)
	}
}

func TestBackupTriggerRejectsConcurrent(t *testing.T) {
	dataDir := t.TempDir()
	sender := &blockingSender{started: make(chan struct{}), release: make(chan struct{})}
	manager := backup.New(backup.Options{DataDir: dataDir, Handle: backupHandle(t, dataDir), Sender: sender})
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		deps.Backup = manager
	})
	cookie := env.setup(t)

	first := make(chan int, 1)
	go func() {
		rec := env.do(t, http.MethodPost, "/api/backup/trigger", "", cookie)
		first <- rec.Code
	}()
	<-sender.started
	second := env.do(t, http.MethodPost, "/api/backup/trigger", "", cookie)
	if second.Code != http.StatusConflict {
		t.Errorf("并发触发状态码 = %d，期望 409", second.Code)
	}
	close(sender.release)
	if code := <-first; code != http.StatusOK {
		t.Errorf("首次触发状态码 = %d，期望 200", code)
	}

	// 结束后可以再次触发。
	time.Sleep(50 * time.Millisecond)
	if rec := env.do(t, http.MethodPost, "/api/backup/trigger", "", cookie); rec.Code != http.StatusOK {
		t.Errorf("再次触发状态码 = %d，期望 200", rec.Code)
	}
}

// failingSender 模拟发送阶段失败，用于验证接口只给警告、本地备份仍可下载。
type failingSender struct {
	err error
}

func (f *failingSender) SendDocument(context.Context, string, string) error { return f.err }

// TestBackupTriggerWarnsWhenTargetNotPrivate 验证目标不是私有会话时只提示警告并保留本地备份。
func TestBackupTriggerWarnsWhenTargetNotPrivate(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"公开频道", backup.ErrTargetPublic, "公开频道"},
		{"无法确认", backup.ErrTargetUnverified, "无法确认"},
	}
	for _, sample := range cases {
		t.Run(sample.name, func(t *testing.T) {
			dataDir := t.TempDir()
			manager := backup.New(backup.Options{
				DataDir: dataDir,
				Handle:  backupHandle(t, dataDir),
				Sender:  &failingSender{err: sample.err},
			})
			env := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
				deps.Backup = manager
			})
			cookie := env.setup(t)

			rec := env.do(t, http.MethodPost, "/api/backup/trigger", "", cookie)
			if rec.Code != http.StatusOK {
				t.Fatalf("触发状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, `"sent":false`) || !strings.Contains(body, sample.want) {
				t.Errorf("响应 = %s，期望未发送并说明原因", body)
			}

			download := env.do(t, http.MethodGet, "/api/backup/download", "", cookie)
			if download.Code != http.StatusOK {
				t.Fatalf("下载状态码 = %d，期望本地备份仍可下载", download.Code)
			}
		})
	}
}
