package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/service"
)

func TestMediaUploadQuotaCountsAndRejects(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	root := t.TempDir()
	r := newResourceRouter(t, g, cfg, newLocalStorage(root))
	user := createUser(t, g, "quota@example.com", "quotauser", "password123", true)
	token := accessToken(t, cfg, &user)

	// 把免费档的存储上限调小，便于触达 507。
	if err := g.Model(&model.Plan{}).Where("id = ?", "free").Update("storage_bytes", 10).Error; err != nil {
		t.Fatalf("调整档位失败: %v", err)
	}
	w := doRaw(r, http.MethodPut, "/api/media/image:Quota1", []byte("123456"), "image/png", token, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("首次上传失败: code=%d body=%s", w.Code, w.Body.String())
	}

	w = doRaw(r, http.MethodPut, "/api/media/image:Quota2", []byte("123456"), "image/png", token, nil)
	if w.Code != http.StatusInsufficientStorage || errorCode(t, w) != "STORAGE_QUOTA_EXCEEDED" {
		t.Fatalf("超额上传应 507 STORAGE_QUOTA_EXCEEDED, got %d %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	errObj, _ := body["error"].(map[string]any)
	if errObj["limit"] != float64(10) || errObj["used"] != float64(12) {
		t.Fatalf("507 响应应带 used 与 limit: %v", body)
	}

	// 覆盖上传只按增量计数，且删除后计数回退。
	w = doRaw(r, http.MethodPut, "/api/media/image:Quota1", []byte("12345"), "image/png", token, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("覆盖上传失败: code=%d body=%s", w.Code, w.Body.String())
	}
	quota := service.NewQuotaService(g)
	used, err := quota.StorageBytes(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("读取用量失败: %v", err)
	}
	if used != 5 {
		t.Fatalf("覆盖上传后用量应为 5, got %d", used)
	}
	w = doRaw(r, http.MethodDelete, "/api/media/image:Quota1", nil, "", token, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("删除失败: code=%d body=%s", w.Code, w.Body.String())
	}
	used, _ = quota.StorageBytes(context.Background(), user.ID)
	if used != 0 {
		t.Fatalf("删除后用量应为 0, got %d", used)
	}
}

func TestMediaUploadReadOnlyReturns402(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newResourceRouter(t, g, cfg, newLocalStorage(t.TempDir()))
	user := createUser(t, g, "readonly@example.com", "readonlyuser", "password123", true)
	token := accessToken(t, cfg, &user)

	if err := g.Model(&model.Plan{}).Where("id = ?", "free").Update("storage_bytes", 4).Error; err != nil {
		t.Fatalf("调整档位失败: %v", err)
	}
	// 人为写入一个超过档位上限的用量计数，模拟降档后的只读态。
	record := model.UsageRecord{UserID: user.ID, Metric: service.MetricStorageBytes, Period: service.PeriodTotal, Value: 100}
	if err := g.Create(&record).Error; err != nil {
		t.Fatalf("写入用量失败: %v", err)
	}
	w := doRaw(r, http.MethodPut, "/api/media/image:ReadOnly1", []byte("x"), "image/png", token, nil)
	// READ_ONLY 是 403：与 402「没余额」分开，前端才能区分该充值还是该清理。
	if w.Code != http.StatusForbidden || errorCode(t, w) != "READ_ONLY" {
		t.Fatalf("只读态上传应 403 READ_ONLY, got %d %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	errObj, _ := body["error"].(map[string]any)
	if errObj["planId"] != "free" {
		t.Fatalf("READ_ONLY 响应应带当前档位: %v", body)
	}
}

func TestAdminRecalculateStorageHealsCounter(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newResourceRouter(t, g, cfg, newLocalStorage(t.TempDir()))
	user := createUser(t, g, "heal@example.com", "healuser", "password123", true)

	file := model.MediaFile{
		ID: uuid.New(), UserID: user.ID, StorageKey: "image:Heal1", ObjectPath: "path",
		MimeType: "image/png", Bytes: 42, Checksum: "x",
	}
	if err := g.Create(&file).Error; err != nil {
		t.Fatalf("写入媒体记录失败: %v", err)
	}
	record := model.UsageRecord{UserID: user.ID, Metric: service.MetricStorageBytes, Period: service.PeriodTotal, Value: 999}
	if err := g.Save(&record).Error; err != nil {
		t.Fatalf("写入用量失败: %v", err)
	}
	// 直接调用重算，验证计数被 SUM(bytes) 覆盖。
	used, err := service.NewQuotaService(g).RecalculateStorage(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("重算失败: %v", err)
	}
	if used != 42 {
		t.Fatalf("重算后应为 42, got %d", used)
	}
	_ = r
}
