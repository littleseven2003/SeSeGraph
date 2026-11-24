package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoFile 读取仓库根目录下的部署文件；测试从 internal/settings 出发向上三层。
func repoFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", name, err)
	}
	return string(data)
}

// TestComposeKeepsWebManagedSettingsEmpty 保证两份编排文件不会把设置页可改的项写死。
// 只要 Compose 传入了非空默认值，该字段就会以环境变量为准并在界面置灰，新用户无法在网页修改。
func TestComposeKeepsWebManagedSettingsEmpty(t *testing.T) {
	for _, name := range []string{"docker-compose.yml", "docker-compose.standalone.yml"} {
		body := repoFile(t, name)
		cases := []struct {
			want string
			why  string
		}{
			{"UPLOAD_MODE: ${UPLOAD_MODE:-}", "默认画质要能在设置页与工作台修改"},
			{"TELEGRAPH_PATH_MODE: ${TELEGRAPH_PATH_MODE:-}", "链接生成方式要能在设置页修改"},
			{"BACKUP_CRON: \"${BACKUP_CRON:-}\"", "备份时刻要能在设置页修改"},
			{"BACKUP_KEEP: ${BACKUP_KEEP:-}", "备份保留份数要能在设置页修改"},
			{"BACKUP_ENABLED: ${BACKUP_ENABLED:-}", "备份开关要能在设置页修改"},
			{"PROXY_MODE: ${PROXY_MODE:-}", "代理模式要能在设置页修改"},
		}
		for _, item := range cases {
			if !strings.Contains(body, item.want) {
				t.Errorf("%s 缺少 %q：%s", name, item.want, item.why)
			}
		}
		if strings.Contains(body, "UPLOAD_MODE: ${UPLOAD_MODE:-photo}") {
			t.Errorf("%s 仍把默认画质写死为 photo，设置页会被锁定", name)
		}
		if strings.Contains(body, "0 3 * * *") {
			t.Errorf("%s 仍写死备份时刻，设置页会被锁定", name)
		}
	}
}

// TestComposePassesThroughDocumentedVariables 保证 .env.example 里出现的调优项能真正传到容器内。
func TestComposePassesThroughDocumentedVariables(t *testing.T) {
	for _, name := range []string{"docker-compose.yml", "docker-compose.standalone.yml"} {
		body := repoFile(t, name)
		for _, want := range []string{
			"UPLOAD_CONCURRENCY: ${UPLOAD_CONCURRENCY:-3}",
			"PROXY_QUEUE_DEPTH: ${PROXY_QUEUE_DEPTH:-100}",
			"PROXY_QUEUE_WAIT: ${PROXY_QUEUE_WAIT:-15}",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s 缺少透传 %q，.env 里设置后不生效", name, want)
			}
		}
	}
}

// TestEnvExampleLeavesWebManagedSettingsToUI 保证按样例复制 .env 后仍能在网页保存这些设置。
func TestEnvExampleLeavesWebManagedSettingsToUI(t *testing.T) {
	body := repoFile(t, ".env.example")
	for _, key := range []string{
		"PROXY_MODE",
		"PROXY_URL",
		"PROXY_FOR_TELEGRAM",
		"PROXY_FOR_TELEGRAPH",
		"UPLOAD_MODE",
		"TELEGRAPH_PATH_MODE",
		"TRAFFIC_ALERT_GB",
		"TRAFFIC_LIMIT_GB",
		"BACKUP_ENABLED",
		"BACKUP_CRON",
		"BACKUP_KEEP",
	} {
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, key+"=") {
				t.Errorf(".env.example 里的 %s 未注释，复制后会把设置页对应项锁成只读", key)
			}
		}
	}
	// 服务端接收上限与网关排队参数不是设置页字段，保留实际取值方便照抄。
	for _, want := range []string{"UPLOAD_CONCURRENCY=3", "PROXY_QUEUE_DEPTH=100", "PROXY_QUEUE_WAIT=15"} {
		if !strings.Contains(body, want) {
			t.Errorf(".env.example 缺少 %q", want)
		}
	}
}
