package service

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/storage"
)

// mapStorage 是清理测试用的内存存储驱动。
type mapStorage struct {
	objects map[string][]byte
}

func newMapStorage() *mapStorage { return &mapStorage{objects: map[string][]byte{}} }

func (m *mapStorage) Kind() string { return "local" }

func (m *mapStorage) Put(_ context.Context, path string, r io.Reader, _ string) (int64, string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, "", err
	}
	m.objects[path] = data
	return int64(len(data)), "sum", nil
}

func (m *mapStorage) Get(_ context.Context, path string) (io.ReadCloser, error) {
	data, ok := m.objects[path]
	if !ok {
		return nil, storage.ErrObjectNotFound
	}
	return io.NopCloser(newReader(data)), nil
}

func (m *mapStorage) Presign(context.Context, string) (storage.Presigned, error) {
	return storage.Presigned{URL: "https://example.com", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (m *mapStorage) Delete(_ context.Context, path string) error {
	delete(m.objects, path)
	return nil
}

func (m *mapStorage) Stat(_ context.Context, path string) (int64, error) {
	data, ok := m.objects[path]
	if !ok {
		return 0, storage.ErrObjectNotFound
	}
	return int64(len(data)), nil
}

func newReader(data []byte) io.Reader { return &byteReader{data: data} }

type byteReader struct {
	data []byte
	pos  int
}

func (b *byteReader) Read(p []byte) (int, error) {
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += n
	return n, nil
}

func seedMedia(t *testing.T, g *gorm.DB, stor *mapStorage, userID uuid.UUID, key string, bytes int64) model.MediaFile {
	t.Helper()
	file := model.MediaFile{
		ID:         uuid.New(),
		UserID:     userID,
		StorageKey: key,
		ObjectPath: userID.String() + "/image/" + key,
		MimeType:   "image/png",
		Bytes:      bytes,
		Checksum:   "sum",
	}
	if err := g.Create(&file).Error; err != nil {
		t.Fatalf("写入媒体记录失败: %v", err)
	}
	stor.objects[file.ObjectPath] = []byte("x")
	return file
}

func TestCleanupRemovesOrphansKeepsReferenced(t *testing.T) {
	g := newServiceDB(t)
	stor := newMapStorage()
	cleanup := NewCleanupService(g, stor)
	user := createUserRow(t, g)

	referenced := seedMedia(t, g, stor, user.ID, "image:Referenced1", 100)
	orphan := seedMedia(t, g, stor, user.ID, "image:Orphan1", 50)

	canvas := model.Canvas{
		ID: uuid.New(), UserID: user.ID, Title: "画布", Revision: 1,
		Data: datatypes.JSON([]byte(`{"nodes":[{"metadata":{"storageKey":"image:Referenced1"}}]}`)),
	}
	if err := g.Create(&canvas).Error; err != nil {
		t.Fatalf("写入画布失败: %v", err)
	}

	report, err := cleanup.Reclaim(context.Background(), user.ID, 7, 1<<30, time.Now(), false)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if report.Reclaimed != 1 || report.Items[0].StorageKey != orphan.StorageKey {
		t.Fatalf("应只清理孤儿媒体: %+v", report)
	}
	if _, ok := stor.objects[referenced.ObjectPath]; !ok {
		t.Fatalf("被引用的媒体不应被删除")
	}
	var count int64
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&count)
	if count != 1 {
		t.Fatalf("清理后应剩一条媒体记录, got %d", count)
	}
	used, _ := NewQuotaService(g).StorageBytes(context.Background(), user.ID)
	if used != 100 {
		t.Fatalf("清理后计数应重算为 100, got %d", used)
	}
}

func TestCleanupExpiryFollowsLastTouched(t *testing.T) {
	g := newServiceDB(t)
	stor := newMapStorage()
	cleanup := NewCleanupService(g, stor)
	user := createUserRow(t, g)

	stale := seedMedia(t, g, stor, user.ID, "image:Stale1", 100)
	fresh := seedMedia(t, g, stor, user.ID, "image:Fresh1", 100)

	oldCanvas := model.Canvas{
		ID: uuid.New(), UserID: user.ID, Title: "旧画布", Revision: 1,
		Data: datatypes.JSON([]byte(`{"nodes":[{"metadata":{"storageKey":"image:Stale1"}}]}`)),
	}
	if err := g.Create(&oldCanvas).Error; err != nil {
		t.Fatalf("写入画布失败: %v", err)
	}
	// 把最后触达时间推到保留期之外。
	past := time.Now().AddDate(0, 0, -30)
	if err := g.Model(&model.Canvas{}).Where("id = ?", oldCanvas.ID).Update("updated_at", past).Error; err != nil {
		t.Fatalf("调整画布时间失败: %v", err)
	}
	recentCanvas := model.Canvas{
		ID: uuid.New(), UserID: user.ID, Title: "新画布", Revision: 1,
		Data: datatypes.JSON([]byte(`{"nodes":[{"metadata":{"storageKey":"image:Fresh1"}}]}`)),
	}
	if err := g.Create(&recentCanvas).Error; err != nil {
		t.Fatalf("写入画布失败: %v", err)
	}

	report, err := cleanup.Reclaim(context.Background(), user.ID, 7, 1<<30, time.Now(), false)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if report.Reclaimed != 1 || report.Items[0].StorageKey != stale.StorageKey || report.Items[0].Reason != "expired" {
		t.Fatalf("应按最后触达时间清理过期媒体: %+v", report)
	}
	if _, ok := stor.objects[fresh.ObjectPath]; !ok {
		t.Fatalf("近期触达的媒体不应被清理")
	}
}

func TestCleanupDryRunKeepsEverything(t *testing.T) {
	g := newServiceDB(t)
	stor := newMapStorage()
	cleanup := NewCleanupService(g, stor)
	user := createUserRow(t, g)
	orphan := seedMedia(t, g, stor, user.ID, "image:Dry1", 50)

	report, err := cleanup.Reclaim(context.Background(), user.ID, 7, 1<<30, time.Now(), true)
	if err != nil {
		t.Fatalf("dry-run 失败: %v", err)
	}
	if report.Reclaimed != 1 || len(report.Items) != 1 {
		t.Fatalf("dry-run 应列出待清理项: %+v", report)
	}
	if _, ok := stor.objects[orphan.ObjectPath]; !ok {
		t.Fatalf("dry-run 不应删除对象")
	}
	var count int64
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&count)
	if count != 1 {
		t.Fatalf("dry-run 不应删除媒体记录, got %d", count)
	}
}

func TestCleanupDowngradeStopsAtLimit(t *testing.T) {
	g := newServiceDB(t)
	stor := newMapStorage()
	cleanup := NewCleanupService(g, stor)
	user := createUserRow(t, g)

	// 三张画布各引用一个媒体，全部在保留期内，但总量超过日落档上限。
	var lastCanvas uuid.UUID
	for i, key := range []string{"image:A1", "image:B1", "image:C1"} {
		file := seedMedia(t, g, stor, user.ID, key, 100)
		canvas := model.Canvas{
			ID: uuid.New(), UserID: user.ID, Title: key, Revision: 1,
			Data: datatypes.JSON([]byte(`{"nodes":[{"metadata":{"storageKey":"` + key + `"}}]}`)),
		}
		if err := g.Create(&canvas).Error; err != nil {
			t.Fatalf("写入画布失败: %v", err)
		}
		// 让三个媒体有不同的最后触达时间：A 最旧、C 最新。
		updatedAt := time.Now().Add(-time.Duration(3-i) * time.Hour)
		g.Model(&model.Canvas{}).Where("id = ?", canvas.ID).Update("updated_at", updatedAt)
		g.Model(&model.MediaFile{}).Where("id = ?", file.ID).Update("created_at", updatedAt)
		lastCanvas = canvas.ID
	}
	_ = lastCanvas

	// 上限 250 字节：至少清理 1 个最旧的媒体后回落到上限内。
	report, err := cleanup.Reclaim(context.Background(), user.ID, 7, 250, time.Now(), false)
	if err != nil {
		t.Fatalf("降档清理失败: %v", err)
	}
	if report.Reclaimed < 1 {
		t.Fatalf("应清理至少一个媒体: %+v", report)
	}
	if report.StorageUsed > 250 {
		t.Fatalf("清理后用量应回落到上限内: %d", report.StorageUsed)
	}
	if report.Reclaimed >= 3 {
		t.Fatalf("降档清理不应删光全部媒体: %+v", report)
	}
	// 最旧的先被清理。
	if report.Items[0].StorageKey != "image:A1" {
		t.Fatalf("应按最后触达时间从旧到新清理: %+v", report.Items)
	}
}

func TestCleanupKeepsSoftDeletedCanvasMedia(t *testing.T) {
	g := newServiceDB(t)
	stor := newMapStorage()
	cleanup := NewCleanupService(g, stor)
	user := createUserRow(t, g)
	file := seedMedia(t, g, stor, user.ID, "image:Soft1", 100)

	canvas := model.Canvas{
		ID: uuid.New(), UserID: user.ID, Title: "软删画布", Revision: 1,
		Data: datatypes.JSON([]byte(`{"nodes":[{"metadata":{"storageKey":"image:Soft1"}}]}`)),
	}
	if err := g.Create(&canvas).Error; err != nil {
		t.Fatalf("写入画布失败: %v", err)
	}
	if err := g.Delete(&canvas).Error; err != nil {
		t.Fatalf("软删除画布失败: %v", err)
	}

	report, err := cleanup.Reclaim(context.Background(), user.ID, 7, 1<<30, time.Now(), false)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if report.Reclaimed != 0 {
		t.Fatalf("软删除画布引用的媒体不应被当孤儿清理: %+v", report)
	}
	if _, ok := stor.objects[file.ObjectPath]; !ok {
		t.Fatalf("软删除画布引用的媒体不应被删除")
	}
}
