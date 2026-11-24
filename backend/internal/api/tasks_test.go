package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// TestTasksListReturnsStagesAndControl 验证任务列表返回三阶段状态与控制状态。
func TestTasksListReturnsStagesAndControl(t *testing.T) {
	env, _ := newEditEnv(t)
	cookie := env.setup(t)
	ctx := context.Background()

	postID, err := env.posts.Create(ctx, posts.Post{Title: "任务列表", ImageCount: 2, Status: posts.StatusUploading})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := env.posts.AttachImage(ctx, posts.Image{
		PostID: postID, SortOrder: 1, Status: posts.ImageStatusUploaded, MimeType: "image/jpeg",
	}); err != nil {
		t.Fatalf("保存图片失败: %v", err)
	}
	if err := env.posts.Pause(ctx, postID); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}

	// 三张都收下后存频道失败：任务是失败态，但图片仍在库里。
	if _, err := env.handle.ExecContext(ctx,
		`UPDATE posts SET status = ?, publish_state = ?, error_message = ? WHERE id = ?`,
		posts.StatusFailed, posts.PublishIdle, "存频道失败", postID); err != nil {
		t.Fatalf("更新任务状态失败: %v", err)
	}

	rec := env.do(t, http.MethodGet, "/api/tasks?page=1&page_size=10", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			ID            int64  `json:"id"`
			Status        string `json:"status"`
			PublishState  string `json:"publish_state"`
			ControlState  string `json:"control_state"`
			ImageCount    int    `json:"image_count"`
			StoredCount   int    `json:"stored_count"`
			ReceivedCount int    `json:"received_count"`
			UploadedCount int    `json:"uploaded_count"`
			ChannelCount  int    `json:"channel_count"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析任务失败: %v", err)
	}
	if body.Total != 1 || len(body.Items) != 1 {
		t.Fatalf("任务 = %d/%d，期望 1 条", body.Total, len(body.Items))
	}
	item := body.Items[0]
	if item.ID != postID || item.ControlState != posts.ControlPaused || item.ImageCount != 2 || item.StoredCount != 1 {
		t.Errorf("任务行 = %+v", item)
	}
	// 已接收 1 张但尚未存入频道：三段状态不能互相冒充。
	if item.UploadedCount != 1 || item.ChannelCount != 0 || item.ReceivedCount != 1 {
		t.Errorf("任务行 = %+v", item)
	}

	// 停止后不再出现在进行中列表里。
	if err := env.posts.Stop(ctx, postID); err != nil {
		t.Fatalf("停止失败: %v", err)
	}
	if err := env.posts.Delete(ctx, postID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	rec = env.do(t, http.MethodGet, fmt.Sprintf("/api/tasks?page=1&page_size=10"), "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("第二次状态码 = %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析任务失败: %v", err)
	}
	if body.Total != 0 {
		t.Errorf("删除后任务数 = %d，期望 0", body.Total)
	}
}

// TestUploadRejectsStaleRound 验证上传轮次不匹配时拒绝写入。
func TestUploadRejectsStaleRound(t *testing.T) {
	env, _ := newEditEnv(t)
	cookie := env.setup(t)
	ctx := context.Background()
	postID, err := env.posts.Create(ctx, posts.Post{Title: "轮次", ImageCount: 1, Status: posts.StatusUploading})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := env.handle.ExecContext(ctx, `UPDATE posts SET upload_round = 2 WHERE id = ?`, postID); err != nil {
		t.Fatalf("写入轮次失败: %v", err)
	}
	rec := env.do(t, http.MethodPost, fmt.Sprintf("/api/posts/%d/images?sort=1&round=1", postID), "", cookie)
	if rec.Code != http.StatusConflict {
		t.Errorf("旧轮次状态码 = %d，期望 409，响应: %s", rec.Code, rec.Body.String())
	}
}

// TestTasksListFiltersBeforePaging 验证筛选在分页前生效，且三段计数各自独立。
func TestTasksListFiltersBeforePaging(t *testing.T) {
	env, _ := newEditEnv(t)
	cookie := env.setup(t)
	ctx := context.Background()

	create := func(title string) int64 {
		id, err := env.posts.Create(ctx, posts.Post{Title: title, ImageCount: 2, Status: posts.StatusUploading})
		if err != nil {
			t.Fatalf("创建帖子失败: %v", err)
		}
		return id
	}
	active := create("进行中")
	paused := create("已暂停")
	stopped := create("已停止")
	finished := create("已完成")
	failed := create("失败")

	// 进行中的帖子：一张已接收未入频道，一张已入频道，三段口径不能互相冒充。
	for _, image := range []posts.Image{
		{PostID: active, SortOrder: 1, Status: posts.ImageStatusUploaded, MimeType: "image/jpeg"},
		{PostID: active, SortOrder: 2, Status: posts.ImageStatusStored, MimeType: "image/jpeg"},
	} {
		if _, err := env.posts.AttachImage(ctx, image); err != nil {
			t.Fatalf("保存图片失败: %v", err)
		}
	}
	for _, stmt := range []string{
		fmt.Sprintf(`UPDATE posts SET control_state = 'paused' WHERE id = %d`, paused),
		fmt.Sprintf(`UPDATE posts SET control_state = 'stopped' WHERE id = %d`, stopped),
		fmt.Sprintf(`UPDATE posts SET status = 'completed', publish_state = 'published' WHERE id = %d`, finished),
		fmt.Sprintf(`UPDATE posts SET status = 'failed', publish_state = 'failed' WHERE id = %d`, failed),
	} {
		if _, err := env.handle.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("准备任务状态失败: %v", err)
		}
	}

	type page struct {
		Items []struct {
			ID            int64 `json:"id"`
			StoredCount   int   `json:"stored_count"`
			UploadedCount int   `json:"uploaded_count"`
			ChannelCount  int   `json:"channel_count"`
		} `json:"items"`
		Total int `json:"total"`
	}
	read := func(status string) page {
		rec := env.do(t, http.MethodGet, "/api/tasks?page=1&page_size=2&status="+status, "", cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态 %q 状态码 = %d，响应: %s", status, rec.Code, rec.Body.String())
		}
		var body page
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析任务失败: %v", err)
		}
		return body
	}
	countOf := func(status string) int { return read(status).Total }

	if got := countOf(""); got != 5 {
		t.Errorf("全部任务 = %d，期望 5", got)
	}
	// 未知筛选回落为全部，旧调用不会因为参数拼错而看不到任务。
	if got := countOf("weird"); got != 5 {
		t.Errorf("未知筛选 = %d，期望回落全部 5", got)
	}
	if got := countOf(posts.TaskFilterActive); got != 1 {
		t.Errorf("进行中 = %d，期望 1", got)
	}
	if got := countOf(posts.TaskFilterPaused); got != 1 {
		t.Errorf("已暂停 = %d，期望 1", got)
	}
	if got := countOf(posts.TaskFilterStopped); got != 1 {
		t.Errorf("已停止 = %d，期望 1", got)
	}
	if got := countOf(posts.TaskFilterFinished); got != 1 {
		t.Errorf("已完成 = %d，期望 1", got)
	}

	// total 与分页同条件：page_size 小于命中数时仍返回筛选后的总数。
	many := read(posts.TaskFilterAll)
	if many.Total != 5 || len(many.Items) > 2 {
		t.Errorf("全部任务分页 = %d 条 / 共 %d，期望最多 2 条共 5 条", len(many.Items), many.Total)
	}
	activePage := read(posts.TaskFilterActive)
	if len(activePage.Items) != 1 || activePage.Items[0].ID != active {
		t.Fatalf("进行中任务 = %+v", activePage.Items)
	}
	row := activePage.Items[0]
	if row.StoredCount != 2 || row.UploadedCount != 1 || row.ChannelCount != 1 {
		t.Errorf("三段计数 = 已接收 %d / 已上传 %d / 已入频道 %d，期望 2 / 1 / 1",
			row.StoredCount, row.UploadedCount, row.ChannelCount)
	}
}

// TestTasksListKeepsReceivedAfterChannelFailure 验证存频道失败不会倒退成未接收。
func TestTasksListKeepsReceivedAfterChannelFailure(t *testing.T) {
	env, _ := newEditEnv(t)
	cookie := env.setup(t)
	ctx := context.Background()

	postID, err := env.posts.Create(ctx, posts.Post{Title: "频道失败", ImageCount: 3, Status: posts.StatusUploading})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	statuses := []string{posts.ImageStatusStored, posts.ImageStatusStored, posts.ImageStatusFailed}
	for index, status := range statuses {
		if _, err := env.posts.AttachImage(ctx, posts.Image{
			PostID: postID, SortOrder: index + 1, Status: status, MimeType: "image/jpeg",
		}); err != nil {
			t.Fatalf("保存图片失败: %v", err)
		}
		// 附件插入后按目标状态落库，模拟上传成功再存频道失败。
		if status != posts.ImageStatusStored {
			if _, err := env.handle.ExecContext(ctx,
				`UPDATE post_images SET status = ? WHERE post_id = ? AND sort_order = ?`,
				status, postID, index+1); err != nil {
				t.Fatalf("更新图片状态失败: %v", err)
			}
		}
	}

	// 三张都收下后存频道失败：任务是失败态，但图片仍在库里。
	if _, err := env.handle.ExecContext(ctx,
		`UPDATE posts SET status = ?, publish_state = ?, error_message = ? WHERE id = ?`,
		posts.StatusFailed, posts.PublishIdle, "存频道失败", postID); err != nil {
		t.Fatalf("更新任务状态失败: %v", err)
	}

	rec := env.do(t, http.MethodGet, "/api/tasks?page=1&page_size=10", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			ID            int64 `json:"id"`
			StoredCount   int   `json:"stored_count"`
			ReceivedCount int   `json:"received_count"`
			UploadedCount int   `json:"uploaded_count"`
			ChannelCount  int   `json:"channel_count"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析任务失败: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].ID != postID {
		t.Fatalf("任务行 = %+v", body.Items)
	}
	row := body.Items[0]
	// 3 张都已收到，2 张入频道；兼容口径 stored_count 仍是上传加入库。
	if row.ReceivedCount != 3 || row.ChannelCount != 2 || row.UploadedCount != 0 {
		t.Errorf("计数 = 已接收 %d / 频道 %d / 上传中 %d，期望 3 / 2 / 0",
			row.ReceivedCount, row.ChannelCount, row.UploadedCount)
	}
	if row.StoredCount != 2 {
		t.Errorf("兼容 stored_count = %d，期望 2", row.StoredCount)
	}
}

// TestTasksListClampsPageAfterShrink 验证命中数变少时请求页被钳回有效页。
func TestTasksListClampsPageAfterShrink(t *testing.T) {
	env, _ := newEditEnv(t)
	cookie := env.setup(t)
	ctx := context.Background()

	ids := make([]int64, 0, 21)
	for index := 0; index < 21; index++ {
		id, err := env.posts.Create(ctx, posts.Post{Title: "任务", ImageCount: 1, Status: posts.StatusUploading})
		if err != nil {
			t.Fatalf("创建帖子失败: %v", err)
		}
		ids = append(ids, id)
	}

	type page struct {
		Items    []json.RawMessage `json:"items"`
		Total    int               `json:"total"`
		Page     int               `json:"page"`
		PageSize int               `json:"page_size"`
	}
	read := func(pageNo int) page {
		rec := env.do(t, http.MethodGet, fmt.Sprintf("/api/tasks?page=%d&page_size=20", pageNo), "", cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("第 %d 页状态码 = %d，响应: %s", pageNo, rec.Code, rec.Body.String())
		}
		var body page
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析任务失败: %v", err)
		}
		return body
	}

	second := read(2)
	if second.Total != 21 || len(second.Items) != 1 || second.Page != 2 {
		t.Fatalf("第 2 页 = %d 条 / 共 %d / 页 %d，期望 1 条共 21 页 2",
			len(second.Items), second.Total, second.Page)
	}

	// 两条任务被移除或完成出列表后总页数只剩 1 页，第 2 页应回落到有效页。
	for _, id := range ids[:2] {
		if _, err := env.handle.ExecContext(ctx, `UPDATE posts SET pending_delete = 1 WHERE id = ?`, id); err != nil {
			t.Fatalf("移除任务失败: %v", err)
		}
	}
	clamped := read(2)
	if clamped.Total != 19 || clamped.Page != 1 || len(clamped.Items) != 19 {
		t.Errorf("钳制后 = %d 条 / 共 %d / 页 %d，期望 19 条共 19 页 1",
			len(clamped.Items), clamped.Total, clamped.Page)
	}
}
