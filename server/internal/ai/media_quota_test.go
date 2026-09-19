package ai

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/membership"
	platformstorage "github.com/infinite-canvas/server/internal/platform/storage"
	"github.com/infinite-canvas/server/internal/testutil"
)

func TestMediaUploadQuotaCountsAndRejects(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	root := t.TempDir()
	r := newResourceRouter(t, g, cfg, testutil.NewLocalStorage(root))
	user := testutil.CreateUser(t, g, "quota@example.com", "quotauser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	// 把免费档的存储上限调小，便于触达 507：改档位后按 SyncQuota 把配额回写账户行。
	if err := g.Model(&model.MembershipPlan{}).Where("id = ?", "free").Update("storage_bytes", 40).Error; err != nil {
		t.Fatalf("调整档位失败: %v", err)
	}
	if err := membership.NewService(g).SyncQuota(context.Background(), user.ID); err != nil {
		t.Fatalf("回写配额失败: %v", err)
	}
	w := testutil.DoRaw(r, http.MethodPut, "/api/v1/media/image:Quota1", testutil.TestPNG, "image/png", token, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("首次上传失败: code=%d body=%s", w.Code, w.Body.String())
	}

	w = testutil.DoRaw(r, http.MethodPut, "/api/v1/media/image:Quota2", testutil.TestPNG, "image/png", token, nil)
	if w.Code != http.StatusInsufficientStorage || testutil.ErrorCode(t, w) != "STORAGE_QUOTA_EXCEEDED" {
		t.Fatalf("超额上传应 507 STORAGE_QUOTA_EXCEEDED, got %d %s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	errObj, _ := body["error"].(map[string]any)
	if errObj["limit"] != float64(40) || errObj["used"] != float64(66) {
		t.Fatalf("507 响应应带 used 与 limit: %v", body)
	}

	// 覆盖上传只按增量计数，且删除后计数回退。
	w = testutil.DoRaw(r, http.MethodPut, "/api/v1/media/image:Quota1", testutil.TestPNG2, "image/png", token, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("覆盖上传失败: code=%d body=%s", w.Code, w.Body.String())
	}
	usage := platformstorage.NewService(g, model.ProductCanvas)
	used, _, err := usage.Snapshot(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("读取用量失败: %v", err)
	}
	if used != 34 {
		t.Fatalf("覆盖上传后用量应为 5, got %d", used)
	}
	w = testutil.DoRaw(r, http.MethodDelete, "/api/v1/media/image:Quota1", nil, "", token, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("删除失败: code=%d body=%s", w.Code, w.Body.String())
	}
	used, _, err = usage.Snapshot(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("读取用量失败: %v", err)
	}
	if used != 0 {
		t.Fatalf("删除后用量应为 0, got %d", used)
	}
}

func TestMediaUploadReadOnlyReturns403(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewLocalStorage(t.TempDir()))
	user := testutil.CreateUser(t, g, "readonly@example.com", "readonlyuser", "password123", true)
	token := testutil.AccessToken(t, cfg, &user)

	if err := g.Model(&model.MembershipPlan{}).Where("id = ?", "free").Update("storage_bytes", 4).Error; err != nil {
		t.Fatalf("调整档位失败: %v", err)
	}
	// 人为把账户行改成 used > quota 的只读态，模拟降档后的只读态。
	if err := g.Model(&model.StorageAccount{}).Where("user_id = ?", user.ID).
		Updates(map[string]any{"quota_bytes": 4, "used_bytes": 100}).Error; err != nil {
		t.Fatalf("写入用量失败: %v", err)
	}
	w := testutil.DoRaw(r, http.MethodPut, "/api/v1/media/image:ReadOnly1", testutil.TestPNG, "image/png", token, nil)
	// READ_ONLY 是 403：与 402「没余额」分开，前端才能区分该充值还是该清理。
	if w.Code != http.StatusForbidden || testutil.ErrorCode(t, w) != "READ_ONLY" {
		t.Fatalf("只读态上传应 403 READ_ONLY, got %d %s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	errObj, _ := body["error"].(map[string]any)
	if errObj["planId"] != "free" {
		t.Fatalf("READ_ONLY 响应应带当前档位: %v", body)
	}
}

func TestAdminRecalculateStorageHealsCounter(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewLocalStorage(t.TempDir()))
	user := testutil.CreateUser(t, g, "heal@example.com", "healuser", "password123", true)

	file := model.MediaFile{
		ID: uuid.New(), UserID: user.ID, StorageKey: "image:Heal1", ObjectPath: "path",
		MimeType: "image/png", Bytes: 42, Checksum: "x",
	}
	if err := g.Create(&file).Error; err != nil {
		t.Fatalf("写入媒体记录失败: %v", err)
	}
	if err := g.Model(&model.StorageAccount{}).Where("user_id = ?", user.ID).
		Update("used_bytes", 999).Error; err != nil {
		t.Fatalf("写入用量失败: %v", err)
	}
	// 直接调用重算，验证计数被 SUM(bytes) 覆盖。
	used, err := platformstorage.NewService(g, model.ProductCanvas).Recalculate(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("重算失败: %v", err)
	}
	if used != 42 {
		t.Fatalf("重算后应为 42, got %d", used)
	}
	_ = r
}
