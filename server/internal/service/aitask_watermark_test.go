package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/infinite-canvas/server/internal/crypto"
	"github.com/infinite-canvas/server/internal/db"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/provider"
	"github.com/infinite-canvas/server/internal/storage"
)

// wmStorage 是视频水印挂钩测试用的内存存储驱动（参照 cleanup_test.go 的 mapStorage
// 独立实现，避免与并行任务的测试文件耦合），额外支持按路径注入 Put 失败。
type wmStorage struct {
	objects map[string][]byte
	deleted []string // Delete 调用过的全部路径（含失败调用），供断言补偿删除
	failPut map[string]error
	// failPutFn 按路径谓词注入 Put 失败：orig 路径带每次随机生成的对象 id，
	// 无法预先写死路径，用「含 /orig/ 段」匹配注入。
	failPutFn func(path string) error
	failDel   map[string]error
}

func newWMStorage() *wmStorage {
	return &wmStorage{objects: map[string][]byte{}, failPut: map[string]error{}, failDel: map[string]error{}}
}

func (m *wmStorage) Kind() string { return "local" }

func (m *wmStorage) Put(_ context.Context, path string, r io.Reader, _ string) (int64, string, error) {
	if err, ok := m.failPut[path]; ok {
		return 0, "", err
	}
	if m.failPutFn != nil {
		if err := m.failPutFn(path); err != nil {
			return 0, "", err
		}
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, "", err
	}
	m.objects[path] = data
	return int64(len(data)), "sum", nil
}

func (m *wmStorage) Get(_ context.Context, path string) (io.ReadCloser, error) {
	data, ok := m.objects[path]
	if !ok {
		return nil, storage.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *wmStorage) Presign(context.Context, string) (storage.Presigned, error) {
	return storage.Presigned{URL: "https://example.com", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (m *wmStorage) PresignWithTTL(_ context.Context, _ string, ttl time.Duration) (storage.Presigned, error) {
	return storage.Presigned{URL: "https://example.com", ExpiresAt: time.Now().Add(ttl)}, nil
}

func (m *wmStorage) Delete(_ context.Context, path string) error {
	m.deleted = append(m.deleted, path)
	if err, ok := m.failDel[path]; ok {
		return err
	}
	delete(m.objects, path)
	return nil
}

func (m *wmStorage) Stat(_ context.Context, path string) (int64, error) {
	data, ok := m.objects[path]
	if !ok {
		return 0, storage.ErrObjectNotFound
	}
	return int64(len(data)), nil
}

// wmVideoStub 是视频落盘水印挂钩的测试替身：可注入错误、可记录调用次数。
// 成功时输出「原始字节 + -watermarked 后缀」，保证水印字节必不等于原始字节。
type wmVideoStub struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (s *wmVideoStub) Video(_ context.Context, src []byte, _ string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return append(append([]byte(nil), src...), []byte("-watermarked")...), nil
}

func newWMTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	g, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	sqlDB, err := g.DB()
	if err != nil {
		t.Fatalf("获取底层连接失败: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.Migrate(g); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := db.SeedPlans(g); err != nil {
		t.Fatalf("写入默认档位失败: %v", err)
	}
	return g
}

// newWMTaskService 构造带水印挂钩的视频任务服务。
func newWMTaskService(t *testing.T, g *gorm.DB, stor *wmStorage, wm videoWatermarker, enabled func() bool) *AITaskService {
	t.Helper()
	cipher, err := crypto.New("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("初始化加密器失败: %v", err)
	}
	upstream := NewUpstreamService(g, cipher, DefaultUpstreamTimeouts())
	s := NewAITaskService(g, upstream, NewMediaWriteService(g, stor))
	if wm != nil {
		s.SetWatermark(wm, enabled)
	}
	return s
}

// seedWMTask 写入 paid/free 均可用的任务夹具：先入账再真实预扣，
// 退款断言才有消费流水可依。isPaid=false 时只入赠送桶，保持 free 档身份；
// isPaid=true 时入账两倍金额：PlanOf 按 purchased 余额判 paid（quota.go PlanOf），
// 预扣恰好消耗 costMicros，只入账一份会把余额扣空退化为 free 档。
func seedWMTask(t *testing.T, g *gorm.DB, isPaid bool, costMicros int64) (*model.AITask, *model.AIRequest, model.PlatformUser) {
	t.Helper()
	user := model.PlatformUser{
		ID:       uuid.New(),
		Email:    uuid.NewString() + "@example.com",
		Username: uuid.NewString()[:8],
		Status:   "active",
	}
	if err := g.Create(&user).Error; err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	credits := NewCreditService(g)
	if err := credits.EnsureCredit(g, user.ID); err != nil {
		t.Fatalf("建账本行失败: %v", err)
	}
	bucket := BucketGranted
	deposit := costMicros
	if isPaid {
		bucket = BucketPurchased
		deposit = costMicros * 2
	}
	if _, err := credits.Adjust(context.Background(), g, user.ID, bucket, deposit, "水印挂钩测试入账", "test"); err != nil {
		t.Fatalf("入账失败: %v", err)
	}
	transactions, err := credits.Reserve(context.Background(), user.ID, costMicros, "", "测试预扣")
	if err != nil {
		t.Fatalf("预扣失败: %v", err)
	}
	ids := make([]uuid.UUID, 0, len(transactions))
	for _, tx := range transactions {
		ids = append(ids, tx.ID)
	}
	raw, _ := json.Marshal(ids)
	request := &model.AIRequest{
		ID:                    uuid.New(),
		UserID:                user.ID,
		Capability:            "video",
		Model:                 "test-video",
		FinalCostMicros:       costMicros,
		Status:                "running",
		ConsumeTransactionIDs: raw,
	}
	if err := g.Create(request).Error; err != nil {
		t.Fatalf("写入请求行失败: %v", err)
	}
	generation := &model.Generation{
		ID: uuid.New(), UserID: user.ID, Kind: "video", Status: "pending", Model: "test-video",
	}
	if err := g.Create(generation).Error; err != nil {
		t.Fatalf("写入生成记录失败: %v", err)
	}
	task := &model.AITask{
		ID: uuid.New(), UserID: user.ID, RequestID: request.ID, Provider: "openai",
		UpstreamTaskID: "up-1", Status: "pending", GenerationID: generation.ID,
	}
	if err := g.Create(task).Error; err != nil {
		t.Fatalf("写入任务行失败: %v", err)
	}
	return task, request, user
}

// succeedWMTask 直接以「上游已返回视频字节」的状态驱动 succeedTask。
func succeedWMTask(t *testing.T, s *AITaskService, task *model.AITask, data []byte) error {
	t.Helper()
	return s.succeedTask(context.Background(), task, provider.VideoState{
		Status: "succeeded",
		Video:  &provider.GeneratedImage{Data: data, MimeType: "video/mp4"},
	}, time.Now())
}

// assertWMRefund 断言退款恰好一条、余额还原、请求收敛 failed。
func assertWMRefund(t *testing.T, g *gorm.DB, userID uuid.UUID, requestID uuid.UUID, costMicros int64) {
	t.Helper()
	var refunds int64
	g.Model(&model.CreditTransaction{}).Where("user_id = ? AND type = ?", userID, TxTypeRefund).Count(&refunds)
	if refunds != 1 {
		t.Fatalf("退款流水应只有一条, got %d", refunds)
	}
	balance, err := NewCreditService(g).Balance(context.Background(), userID)
	if err != nil {
		t.Fatalf("读取余额失败: %v", err)
	}
	if balance.PurchasedMicros+balance.GrantedMicros != costMicros {
		t.Fatalf("失败应全额退还, 余额=%d", balance.PurchasedMicros+balance.GrantedMicros)
	}
	var request model.AIRequest
	if err := g.First(&request, "id = ?", requestID).Error; err != nil {
		t.Fatalf("读取请求行失败: %v", err)
	}
	if request.Status != "failed" {
		t.Fatalf("请求应收敛为 failed: %s", request.Status)
	}
}

// TestVideoTaskWatermarkFailClosed 锁死 S1 红线：free 档视频水印失败 →
// failTask 既有退款路径，存储中无主对象也无 orig 对象，无任何媒体行。
func TestVideoTaskWatermarkFailClosed(t *testing.T) {
	g := newWMTestDB(t)
	stor := newWMStorage()
	wm := &wmVideoStub{err: errors.New("注入的水印故障")}
	s := newWMTaskService(t, g, stor, wm, func() bool { return true })
	task, request, user := seedWMTask(t, g, false, 400_000)

	if err := succeedWMTask(t, s, task, []byte("fake-mp4-data")); err != nil {
		t.Fatalf("succeedTask 水印失败应收敛而非报错: %v", err)
	}
	var stored model.AITask
	if err := g.First(&stored, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if stored.Status != "failed" || stored.Error != "水印处理失败" {
		t.Fatalf("任务应 fail-closed 为 failed/水印处理失败: %+v", stored)
	}
	var generation model.Generation
	if err := g.First(&generation, "id = ?", task.GenerationID).Error; err != nil {
		t.Fatalf("读取生成记录失败: %v", err)
	}
	if generation.Status != "failed" {
		t.Fatalf("生成记录应收敛为 failed: %s", generation.Status)
	}
	var mediaCount int64
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&mediaCount)
	if mediaCount != 0 {
		t.Fatalf("fail-closed 不应产生任何媒体行, got %d", mediaCount)
	}
	if len(stor.objects) != 0 {
		t.Fatalf("存储中不应有任何对象（主对象与 orig 均不可落盘）: %v", stor.objects)
	}
	assertWMRefund(t, g, user.ID, request.ID, 400_000)
}

// TestVideoTaskWatermarkFreeStoresOrig 验证 free 档成功序：
// orig 保留原始字节、主对象为水印后字节、媒体行记录水印版大小。
func TestVideoTaskWatermarkFreeStoresOrig(t *testing.T) {
	g := newWMTestDB(t)
	stor := newWMStorage()
	wm := &wmVideoStub{}
	s := newWMTaskService(t, g, stor, wm, func() bool { return true })
	task, _, user := seedWMTask(t, g, false, 400_000)

	if err := succeedWMTask(t, s, task, []byte("fake-mp4-data")); err != nil {
		t.Fatalf("succeedTask 失败: %v", err)
	}
	if wm.calls != 1 {
		t.Fatalf("free 档应烧录一次水印: calls=%d", wm.calls)
	}
	var stored model.AITask
	if err := g.First(&stored, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if stored.Status != "succeeded" || stored.StorageKey == "" {
		t.Fatalf("任务应收敛为 succeeded 并带 storageKey: %+v", stored)
	}
	original := []byte("fake-mp4-data")
	wantWM := append(append([]byte(nil), original...), []byte("-watermarked")...)
	mainPath := storage.ObjectPath(user.ID.String(), stored.StorageKey)
	origPath := storage.OrigPath(user.ID.String(), stored.StorageKey)
	if origPath == "" {
		t.Fatalf("视频 storageKey 必含冒号，orig 路径不应为空")
	}
	if got, ok := stor.objects[origPath]; !ok || !bytes.Equal(got, original) {
		t.Fatalf("orig 应存在且内容为原始字节: %q", got)
	}
	if got, ok := stor.objects[mainPath]; !ok || !bytes.Equal(got, wantWM) {
		t.Fatalf("主对象应为水印后字节: %q", got)
	}
	var file model.MediaFile
	if err := g.Where("user_id = ? AND storage_key = ?", user.ID, stored.StorageKey).First(&file).Error; err != nil {
		t.Fatalf("应写入媒体行: %v", err)
	}
	if file.MimeType != "video/mp4" || file.Bytes != int64(len(wantWM)) {
		t.Fatalf("媒体行应为水印版: mime=%s bytes=%d", file.MimeType, file.Bytes)
	}
}

// TestVideoTaskPaidSkipsWatermark 验证付费档现状不变：不烧水印、不写 orig、
// 主对象即原始字节。
func TestVideoTaskPaidSkipsWatermark(t *testing.T) {
	g := newWMTestDB(t)
	stor := newWMStorage()
	wm := &wmVideoStub{}
	s := newWMTaskService(t, g, stor, wm, func() bool { return true })
	task, _, user := seedWMTask(t, g, true, 400_000) // purchased > 0 → paid 档

	if err := succeedWMTask(t, s, task, []byte("fake-mp4-data")); err != nil {
		t.Fatalf("succeedTask 失败: %v", err)
	}
	if wm.calls != 0 {
		t.Fatalf("付费档不应调用水印: calls=%d", wm.calls)
	}
	var stored model.AITask
	if err := g.First(&stored, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if stored.Status != "succeeded" {
		t.Fatalf("任务应收敛为 succeeded: %+v", stored)
	}
	if len(stor.objects) != 1 {
		t.Fatalf("付费档不应写 orig, objects=%v", stor.objects)
	}
	mainPath := storage.ObjectPath(user.ID.String(), stored.StorageKey)
	if got, ok := stor.objects[mainPath]; !ok || !bytes.Equal(got, []byte("fake-mp4-data")) {
		t.Fatalf("付费档主对象应为原始字节: %q", got)
	}
}

// TestVideoTaskOrigPutFailureFailsTask 锁死落盘序第②步（评审 E-3）：orig Put 失败 →
// 整单失败退款。水印已烧但水印版主对象尚未落盘，存储中主对象与 orig 均不存在，
// 无任何媒体行。
func TestVideoTaskOrigPutFailureFailsTask(t *testing.T) {
	g := newWMTestDB(t)
	stor := newWMStorage()
	stor.failPutFn = func(path string) error {
		if strings.Contains(path, "/orig/") {
			return errors.New("注入的 orig 写入故障")
		}
		return nil
	}
	wm := &wmVideoStub{}
	s := newWMTaskService(t, g, stor, wm, func() bool { return true })
	task, request, user := seedWMTask(t, g, false, 400_000)

	if err := succeedWMTask(t, s, task, []byte("fake-mp4-data")); err != nil {
		t.Fatalf("succeedTask orig 写入失败应收敛而非报错: %v", err)
	}
	if wm.calls != 1 {
		t.Fatalf("orig 写入前水印应已烧录一次: calls=%d", wm.calls)
	}
	var stored model.AITask
	if err := g.First(&stored, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if stored.Status != "failed" || stored.Error != "视频落盘失败" {
		t.Fatalf("任务应 fail-closed 为 failed/视频落盘失败: %+v", stored)
	}
	var generation model.Generation
	if err := g.First(&generation, "id = ?", task.GenerationID).Error; err != nil {
		t.Fatalf("读取生成记录失败: %v", err)
	}
	if generation.Status != "failed" {
		t.Fatalf("生成记录应收敛为 failed: %s", generation.Status)
	}
	var mediaCount int64
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&mediaCount)
	if mediaCount != 0 {
		t.Fatalf("orig 写入失败不应产生任何媒体行, got %d", mediaCount)
	}
	if len(stor.objects) != 0 {
		t.Fatalf("orig 写入失败时主对象与 orig 均不应落盘: %v", stor.objects)
	}
	assertWMRefund(t, g, user.ID, request.ID, 400_000)
}

// TestVideoTaskWatermarkDisabled 验证开关关闭时整段跳过：free 档也不判档、
// 不写 orig、不烧水印，行为与现状完全一致。
func TestVideoTaskWatermarkDisabled(t *testing.T) {
	g := newWMTestDB(t)
	stor := newWMStorage()
	wm := &wmVideoStub{}
	s := newWMTaskService(t, g, stor, wm, func() bool { return false })
	task, _, user := seedWMTask(t, g, false, 400_000)

	if err := succeedWMTask(t, s, task, []byte("fake-mp4-data")); err != nil {
		t.Fatalf("succeedTask 失败: %v", err)
	}
	if wm.calls != 0 {
		t.Fatalf("开关关闭时不应调用水印: calls=%d", wm.calls)
	}
	var stored model.AITask
	if err := g.First(&stored, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if len(stor.objects) != 1 {
		t.Fatalf("开关关闭时不应写 orig, objects=%v", stor.objects)
	}
	mainPath := storage.ObjectPath(user.ID.String(), stored.StorageKey)
	if got, ok := stor.objects[mainPath]; !ok || !bytes.Equal(got, []byte("fake-mp4-data")) {
		t.Fatalf("开关关闭时主对象应为原始字节: %q", got)
	}
}

// TestVideoTaskSaveFailureCompensatesOrig 验证落盘序第③步：
// 正式存储失败时补偿删除已写的 orig（best-effort），仍然 failTask 退款。
func TestVideoTaskSaveFailureCompensatesOrig(t *testing.T) {
	g := newWMTestDB(t)
	stor := newWMStorage()
	wm := &wmVideoStub{}
	s := newWMTaskService(t, g, stor, wm, func() bool { return true })
	task, request, user := seedWMTask(t, g, false, 400_000)

	// 把 free 档单文件上限压到与原始产物等大：水印字节必然超限，Save 必失败。
	if err := g.Model(&model.Plan{}).Where("id = ?", "free").Update("max_file_bytes", len("fake-mp4-data")).Error; err != nil {
		t.Fatalf("调整档位上限失败: %v", err)
	}

	if err := succeedWMTask(t, s, task, []byte("fake-mp4-data")); err != nil {
		t.Fatalf("succeedTask 落盘失败应收敛而非报错: %v", err)
	}
	var stored model.AITask
	if err := g.First(&stored, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if stored.Status != "failed" || stored.Error != "视频落盘失败" {
		t.Fatalf("任务应为 failed/视频落盘失败: %+v", stored)
	}
	if len(stor.objects) != 0 {
		t.Fatalf("补偿后不应残留任何对象: %v", stor.objects)
	}
	found := false
	wantOrigPrefix := user.ID.String() + "/orig/"
	for _, deleted := range stor.deleted {
		if len(deleted) > len(wantOrigPrefix) && deleted[:len(wantOrigPrefix)] == wantOrigPrefix {
			found = true
		}
	}
	if !found {
		t.Fatalf("应补偿删除 orig, deleted=%v", stor.deleted)
	}
	var mediaCount int64
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&mediaCount)
	if mediaCount != 0 {
		t.Fatalf("落盘失败不应产生媒体行, got %d", mediaCount)
	}
	assertWMRefund(t, g, user.ID, request.ID, 400_000)
}
