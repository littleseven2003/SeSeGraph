package api

import (
	"context"
	"net/http"
	"testing"
)

// TestStagesReportCompressedCount 验证压缩进度随创建、进度与任务列表三处对外可见，
// 四阶段的第一段因此有统一的数据来源。
func TestStagesReportCompressedCount(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPost, "/api/posts",
		`{"title":"压缩记录","image_count":3,"upload_mode":"photo","compressed_count":2}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("创建状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID              int64 `json:"id"`
		CompressedCount *int  `json:"compressed_count"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if created.CompressedCount == nil || *created.CompressedCount != 2 {
		t.Fatalf("创建响应压缩进度 = %v，期望 2", created.CompressedCount)
	}

	var progress struct {
		ID              int64 `json:"id"`
		ImageCount      int   `json:"image_count"`
		CompressedCount *int  `json:"compressed_count"`
	}
	rec = env.do(t, http.MethodGet, "/api/posts/1/progress", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("进度状态码 = %d", rec.Code)
	}
	if err := decodeJSON(rec.Body.Bytes(), &progress); err != nil {
		t.Fatalf("解析进度失败: %v", err)
	}
	if progress.ID != created.ID || progress.ImageCount != 3 {
		t.Fatalf("进度内容不符: %+v", progress)
	}
	if progress.CompressedCount == nil || *progress.CompressedCount != 2 {
		t.Errorf("进度压缩值 = %v，期望 2", progress.CompressedCount)
	}

	var list struct {
		Items []struct {
			ID              int64 `json:"id"`
			CompressedCount *int  `json:"compressed_count"`
		} `json:"items"`
	}
	rec = env.do(t, http.MethodGet, "/api/tasks", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("任务列表状态码 = %d", rec.Code)
	}
	if err := decodeJSON(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("解析任务列表失败: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].CompressedCount == nil || *list.Items[0].CompressedCount != 2 {
		t.Fatalf("任务列表压缩值不符: %+v", list.Items)
	}
}

// TestStagesOldTaskWithoutCompressedCount 验证旧任务不乱填：没有记录时三处都返回空值。
func TestStagesOldTaskWithoutCompressedCount(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPost, "/api/posts", `{"title":"旧任务","image_count":2}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("创建状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}

	var progress struct {
		CompressedCount *int `json:"compressed_count"`
	}
	rec = env.do(t, http.MethodGet, "/api/posts/1/progress", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("进度状态码 = %d", rec.Code)
	}
	if err := decodeJSON(rec.Body.Bytes(), &progress); err != nil {
		t.Fatalf("解析进度失败: %v", err)
	}
	if progress.CompressedCount != nil {
		t.Errorf("未上报时压缩进度 = %d，期望空值", *progress.CompressedCount)
	}

	var list struct {
		Items []struct {
			CompressedCount *int `json:"compressed_count"`
		} `json:"items"`
	}
	rec = env.do(t, http.MethodGet, "/api/tasks", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("任务列表状态码 = %d", rec.Code)
	}
	if err := decodeJSON(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("解析任务列表失败: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].CompressedCount != nil {
		t.Errorf("旧任务压缩进度应保持空值: %+v", list.Items)
	}
}

// TestStagesRejectCompressedCountOverflow 验证压缩进度不会超过图片数量。
func TestStagesRejectCompressedCountOverflow(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPost, "/api/posts",
		`{"title":"越界","image_count":2,"compressed_count":3}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", rec.Code)
	}
}

// TestCreatePostPublishIntent 验证提交时的发布意图落库，并在建任务时冻结链接口径；
// 仅存图与旧任务保持空意图，不改变原有发布权限。
func TestCreatePostPublishIntent(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPost, "/api/posts",
		`{"title":"提交并发布","image_count":1,"publish_intent":"auto"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID            int64  `json:"id"`
		PublishIntent string `json:"publish_intent"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if created.PublishIntent != "auto" {
		t.Errorf("发布意图 = %q，期望 auto", created.PublishIntent)
	}
	info, err := env.posts.PublishInfo(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("读取发布信息失败: %v", err)
	}
	if info.PathMode != "title" {
		t.Errorf("冻结的链接口径 = %q，期望 title", info.PathMode)
	}

	// 仅存图不冻结口径，也不带意图。
	rec = env.do(t, http.MethodPost, "/api/posts", `{"title":"仅存图","image_count":1}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	var manual struct {
		ID            int64  `json:"id"`
		PublishIntent string `json:"publish_intent"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &manual); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if manual.PublishIntent != "" {
		t.Errorf("仅存图意图 = %q，期望空", manual.PublishIntent)
	}
	info, err = env.posts.PublishInfo(context.Background(), manual.ID)
	if err != nil {
		t.Fatalf("读取发布信息失败: %v", err)
	}
	if info.PathMode != "" {
		t.Errorf("仅存图不应冻结口径，得到 %q", info.PathMode)
	}
}

// TestCreatePostRejectsUnknownIntent 验证未知发布意图被拒绝。
func TestCreatePostRejectsUnknownIntent(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	rec := env.do(t, http.MethodPost, "/api/posts",
		`{"title":"非法意图","image_count":1,"publish_intent":"always"}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", rec.Code)
	}
}

// TestTasksListExposesPublishIntent 验证任务分页响应带出发布意图，供总进度确定分母。
func TestTasksListExposesPublishIntent(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	if rec := env.do(t, http.MethodPost, "/api/posts",
		`{"title":"自动发布","image_count":1,"publish_intent":"auto"}`, cookie); rec.Code != http.StatusCreated {
		t.Fatalf("创建状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	if rec := env.do(t, http.MethodPost, "/api/posts",
		`{"title":"仅存图","image_count":1}`, cookie); rec.Code != http.StatusCreated {
		t.Fatalf("创建状态码 = %d", rec.Code)
	}

	var list struct {
		Items []struct {
			Title         string `json:"title"`
			PublishIntent string `json:"publish_intent"`
		} `json:"items"`
	}
	rec := env.do(t, http.MethodGet, "/api/tasks", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("任务列表状态码 = %d", rec.Code)
	}
	if err := decodeJSON(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("解析任务列表失败: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("任务条数 = %d，期望 2", len(list.Items))
	}
	byTitle := map[string]string{}
	for _, item := range list.Items {
		byTitle[item.Title] = item.PublishIntent
	}
	if byTitle["自动发布"] != "auto" {
		t.Errorf("自动发布意图 = %q，期望 auto", byTitle["自动发布"])
	}
	if byTitle["仅存图"] != "" {
		t.Errorf("仅存图意图 = %q，期望空", byTitle["仅存图"])
	}
}
