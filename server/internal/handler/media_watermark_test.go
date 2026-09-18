package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/storage"
)

// ===== 媒体水印下发闸门（T4）测试夹具 =====

// makePaid 写入非零充值桶余额，使 DerivePlan 派生为 paid 档。
func makePaid(t *testing.T, g *gorm.DB, user model.PlatformUser) {
	t.Helper()
	if err := g.Create(&model.Credit{UserID: user.ID, PurchasedMicros: 1_000_000}).Error; err != nil {
		t.Fatalf("写入付费余额失败: %v", err)
	}
}

// seedMediaFile 直接写入媒体行（绕过上传流程），返回该行。
func seedMediaFile(t *testing.T, g *gorm.DB, user model.PlatformUser, key, mimeType string, body []byte) model.MediaFile {
	t.Helper()
	sum := sha256.Sum256(body)
	file := model.MediaFile{
		ID:               uuid.New(),
		UserID:           user.ID,
		StorageKey:       key,
		ObjectPath:       storage.ObjectPath(user.ID.String(), key),
		MimeType:         mimeType,
		Bytes:            int64(len(body)),
		Checksum:         hex.EncodeToString(sum[:]),
		ModerationStatus: "skipped",
		CreatedAt:        time.Now(),
	}
	if err := g.Create(&file).Error; err != nil {
		t.Fatalf("写入媒体行失败: %v", err)
	}
	return file
}

// putObject 把对象字节写入存储：fake 驱动直接写 objects，local 驱动走真实落盘。
func putObject(t *testing.T, stor storage.Storage, path string, body []byte) {
	t.Helper()
	if fake, ok := stor.(*fakeStorage); ok {
		fake.objects[path] = body
		return
	}
	if _, _, err := stor.Put(context.Background(), path, bytes.NewReader(body), "application/octet-stream"); err != nil {
		t.Fatalf("写入对象失败: %v", err)
	}
}

// 泄漏面③（S3 预签名 TTL ≤360s）+ S3 路径 Head/Get 同口径。
func TestMediaS3PaidOrigRedirectShortTTL(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	fake := newFakeStorage("s3")
	fake.presignURL = "https://s3.example.com/test-bucket/object?X-Amz-Signature=fake"
	r := newResourceRouter(t, g, cfg, fake)
	owner := createUser(t, g, "s3paid@example.com", "s3paid", "password123", true)
	token := accessToken(t, cfg, &owner)
	makePaid(t, g, owner)

	origBody := testPNG2
	file := seedMediaFile(t, g, owner, "image:S3Orig1", "image/png", testPNG)
	fake.objects[file.ObjectPath] = testPNG
	fake.objects[storage.OrigPath(owner.ID.String(), "image:S3Orig1")] = origBody

	// GET：paid + orig → 302 到 orig 预签名，TTL 精确 360s（干净件直链短时效红线）
	w := doRaw(r, http.MethodGet, "/api/media/image:S3Orig1", nil, "", token, nil)
	if w.Code != http.StatusFound {
		t.Fatalf("paid+orig 的 GET 应 302, got %d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Location") != fake.presignURL {
		t.Fatalf("Location 应指向 orig 预签名结果: %s", w.Header().Get("Location"))
	}
	if n := len(fake.withTTLs); n == 0 {
		t.Fatal("应调用 PresignWithTTL")
	} else if got := fake.withTTLs[n-1]; got != 360*time.Second || got > 360*time.Second {
		t.Fatalf("orig 预签名 TTL 应为 360s, got %s", got)
	}

	// HEAD 同口径：Content-Length 用 orig 的 Stat 值，ETag 带 orig- 前缀
	w = doRaw(r, http.MethodHead, "/api/media/image:S3Orig1", nil, "", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("s3 HEAD 应 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Length") != strconv.Itoa(len(origBody)) {
		t.Fatalf("HEAD Content-Length 应为 orig 大小 %d, got %s", len(origBody), w.Header().Get("Content-Length"))
	}
	if w.Header().Get("ETag") != `"orig-`+file.Checksum+`"` {
		t.Fatalf("HEAD ETag 应带 orig- 前缀: %s", w.Header().Get("ETag"))
	}
	if w.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("HEAD 缓存头应为 no-cache: %s", w.Header().Get("Cache-Control"))
	}

	// 申请下载 → 取件：S3 驱动 302，TTL 同样 360s
	w = doRaw(r, http.MethodPost, "/api/media/image:S3Orig1/download", nil, "", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("申请下载应 200, got %d body=%s", w.Code, w.Body.String())
	}
	tokenURL := decodeBody(t, w)["url"].(string)
	if !strings.HasPrefix(tokenURL, "/api/media-download/") {
		t.Fatalf("paid+orig 应签发取件链接: %s", tokenURL)
	}
	w = doRaw(r, http.MethodGet, tokenURL, nil, "", token, nil)
	if w.Code != http.StatusFound {
		t.Fatalf("取件应 302, got %d body=%s", w.Code, w.Body.String())
	}
	if got := fake.withTTLs[len(fake.withTTLs)-1]; got != 360*time.Second {
		t.Fatalf("取件预签名 TTL 应为 360s, got %s", got)
	}
}

// 泄漏面④配套：缓存头分级四态（local 驱动）。
func TestMediaDeliveryCacheHeaderGrading(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	stor := newLocalStorage(t.TempDir())
	r := newResourceRouter(t, g, cfg, stor)

	// 态一：免费档 image → no-cache + wm- ETag（可变字节，含无 orig 历史产物）
	freeUser := createUser(t, g, "gradefree@example.com", "gradefree", "password123", true)
	freeToken := accessToken(t, cfg, &freeUser)
	if w := doRaw(r, http.MethodPut, "/api/media/image:Grade1", testPNG, "image/png", freeToken, nil); w.Code != http.StatusCreated {
		t.Fatalf("免费档上传失败: %d body=%s", w.Code, w.Body.String())
	}
	sum := sha256.Sum256(testPNG)
	freeChecksum := hex.EncodeToString(sum[:])
	w := doRaw(r, http.MethodGet, "/api/media/image:Grade1", nil, "", freeToken, nil)
	if w.Code != http.StatusOK || w.Body.String() != string(testPNG) {
		t.Fatalf("免费档 GET 应出主对象: code=%d", w.Code)
	}
	if w.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("免费档 image 缓存头应为 no-cache: %s", w.Header().Get("Cache-Control"))
	}
	if w.Header().Get("ETag") != `"wm-`+freeChecksum+`"` {
		t.Fatalf("免费档 image ETag 应带 wm- 前缀: %s", w.Header().Get("ETag"))
	}

	// 态二：付费档且有干净原件 → no-cache + orig- ETag，出原件字节。
	// 复刻 webp 源生成件场景（评审 E-1）：orig 存 webp 原始字节，媒体行 mime 是
	// 水印版 image/png，下发必须按实际字节嗅探为 image/webp。
	paid := createUser(t, g, "gradepaid@example.com", "gradepaid", "password123", true)
	paidToken := accessToken(t, cfg, &paid)
	makePaid(t, g, paid)
	origBody := testWebP
	w = doRaw(r, http.MethodPut, "/api/media/image:Grade2", testPNG, "image/png", paidToken, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("付费档上传失败: %d body=%s", w.Code, w.Body.String())
	}
	rowChecksum := decodeBody(t, w)["checksum"].(string)
	// 上传流程的 orig 补删先于夹具写入，必须在其后种入 orig
	putObject(t, stor, storage.OrigPath(paid.ID.String(), "image:Grade2"), origBody)
	w = doRaw(r, http.MethodGet, "/api/media/image:Grade2", nil, "", paidToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("paid+orig GET 应 200, got %d", w.Code)
	}
	if w.Body.String() != string(origBody) {
		t.Fatalf("付费档应出干净原件字节")
	}
	if w.Header().Get("Content-Type") != "image/webp" {
		t.Fatalf("paid+orig GET Content-Type 应按嗅探为 image/webp: %s", w.Header().Get("Content-Type"))
	}
	if w.Header().Get("X-Checksum") != "" {
		t.Fatalf("paid+orig GET 不应下发 X-Checksum: %s", w.Header().Get("X-Checksum"))
	}
	if w.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("paid+orig 缓存头应为 no-cache: %s", w.Header().Get("Cache-Control"))
	}
	if w.Header().Get("ETag") != `"orig-`+rowChecksum+`"` {
		t.Fatalf("paid+orig ETag 应带 orig- 前缀: %s", w.Header().Get("ETag"))
	}
	// HEAD 与 GET 完全同口径：Content-Type 同为嗅探值，Content-Length 用 orig Stat 值，
	// 行 checksum 与 orig 字节不一致，X-Checksum 同样省略。
	w = doRaw(r, http.MethodHead, "/api/media/image:Grade2", nil, "", paidToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("paid+orig HEAD 应 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "image/webp" {
		t.Fatalf("paid+orig HEAD Content-Type 应按嗅探为 image/webp: %s", w.Header().Get("Content-Type"))
	}
	if w.Header().Get("Content-Length") != strconv.Itoa(len(origBody)) {
		t.Fatalf("HEAD Content-Length 应为 orig 大小 %d, got %s", len(origBody), w.Header().Get("Content-Length"))
	}
	if w.Header().Get("X-Checksum") != "" {
		t.Fatalf("paid+orig HEAD 不应下发 X-Checksum: %s", w.Header().Get("X-Checksum"))
	}
	if w.Header().Get("ETag") != `"orig-`+rowChecksum+`"` {
		t.Fatalf("HEAD ETag 应与 GET 同口径: %s", w.Header().Get("ETag"))
	}

	// 态三：付费档无干净原件 → 维持稳定字节 immutable
	if w := doRaw(r, http.MethodPut, "/api/media/image:Grade3", testPNG, "image/png", paidToken, nil); w.Code != http.StatusCreated {
		t.Fatalf("付费档上传失败: %d", w.Code)
	}
	w = doRaw(r, http.MethodGet, "/api/media/image:Grade3", nil, "", paidToken, nil)
	if w.Body.String() != string(testPNG) {
		t.Fatalf("付费无 orig 应出主对象")
	}
	if w.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Fatalf("付费无 orig 应维持 immutable: %s", w.Header().Get("Cache-Control"))
	}
	if w.Header().Get("ETag") != `"`+rowChecksum+`"` {
		t.Fatalf("付费无 orig 应为无前缀 ETag: %s", w.Header().Get("ETag"))
	}

	// 态四：非可水印类型（audio）→ 永远稳定字节 immutable
	if w := doRaw(r, http.MethodPut, "/api/media/audio:Grade4", []byte{0x00, 0x01, 0x02, 0x03}, "audio/mpeg", freeToken, nil); w.Code != http.StatusCreated {
		t.Fatalf("音频上传失败: %d body=%s", w.Code, w.Body.String())
	}
	w = doRaw(r, http.MethodGet, "/api/media/audio:Grade4", nil, "", freeToken, nil)
	if w.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Fatalf("audio 应维持 immutable: %s", w.Header().Get("Cache-Control"))
	}
	if strings.Contains(w.Header().Get("ETag"), "wm-") {
		t.Fatalf("audio ETag 不应带 wm- 前缀: %s", w.Header().Get("ETag"))
	}
}

// 304 命中不读文件体：If-None-Match 命中时 fake 驱动零 Get 调用。
func TestMediaDelivery304SkipsBodyRead(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	fake := newFakeStorage("local")
	r := newResourceRouter(t, g, cfg, fake)
	user := createUser(t, g, "etag@example.com", "etaguser", "password123", true)
	token := accessToken(t, cfg, &user)
	file := seedMediaFile(t, g, user, "image:NotModified", "image/png", testPNG)
	fake.objects[file.ObjectPath] = testPNG
	etag := `"wm-` + file.Checksum + `"`

	w := doRaw(r, http.MethodGet, "/api/media/image:NotModified", nil, "", token, map[string]string{"If-None-Match": etag})
	if w.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match 命中应 304, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("304 不应带响应体")
	}
	if len(fake.gets) != 0 {
		t.Fatalf("304 命中不应读文件体, gets=%v", fake.gets)
	}
	// ETag 不一致时正常回流
	w = doRaw(r, http.MethodGet, "/api/media/image:NotModified", nil, "", token, map[string]string{"If-None-Match": `"other"`})
	if w.Code != http.StatusOK {
		t.Fatalf("ETag 不一致应 200, got %d", w.Code)
	}
	if len(fake.gets) != 1 || fake.gets[0] != file.ObjectPath {
		t.Fatalf("未命中应读一次主对象, gets=%v", fake.gets)
	}
}

// 泄漏面配套⑦：PUT 覆盖与 DELETE 的 orig 补删（fake 驱动记录 Delete 调用）。
func TestMediaPutOverwriteAndDeleteRemoveOrig(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	fake := newFakeStorage("local")
	r := newResourceRouter(t, g, cfg, fake)
	user := createUser(t, g, "origclean@example.com", "origclean", "password123", true)
	token := accessToken(t, cfg, &user)
	origPath := storage.OrigPath(user.ID.String(), "image:Clean1")

	if w := doRaw(r, http.MethodPut, "/api/media/image:Clean1", testPNG, "image/png", token, nil); w.Code != http.StatusCreated {
		t.Fatalf("首次上传失败: %d body=%s", w.Code, w.Body.String())
	}
	fake.objects[origPath] = []byte("stale-orig")
	// 覆盖写成功后主对象即权威，旧 orig 必须清除
	if w := doRaw(r, http.MethodPut, "/api/media/image:Clean1", testPNG2, "image/png", token, nil); w.Code != http.StatusCreated {
		t.Fatalf("覆盖上传失败: %d body=%s", w.Code, w.Body.String())
	}
	if !slices.Contains(fake.deleted, origPath) {
		t.Fatalf("覆盖上传后应补删 orig, deleted=%v", fake.deleted)
	}
	if _, ok := fake.objects[origPath]; ok {
		t.Fatal("orig 对象应已删除")
	}

	// DELETE：主对象删除后补删 orig
	fake.objects[origPath] = []byte("stale-orig")
	if w := doRaw(r, http.MethodDelete, "/api/media/image:Clean1", nil, "", token, nil); w.Code != http.StatusNoContent {
		t.Fatalf("删除应 204, got %d body=%s", w.Code, w.Body.String())
	}
	if !slices.Contains(fake.deleted, origPath) {
		t.Fatalf("删除媒体后应补删 orig, deleted=%v", fake.deleted)
	}
	if _, ok := fake.objects[origPath]; ok {
		t.Fatal("orig 对象应已删除")
	}
}

// 泄漏面兜底：档位派生失败必须 500，绝不降级直出字节。
func TestMediaDeliveryDerivePlanFailClosed(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	fake := newFakeStorage("s3")
	fake.presignURL = "https://s3.example.com/test-bucket/object?X-Amz-Signature=fake"
	r := newResourceRouter(t, g, cfg, fake)
	owner := createUser(t, g, "failclosed@example.com", "failclosed", "password123", true)
	token := accessToken(t, cfg, &owner)
	makePaid(t, g, owner)
	file := seedMediaFile(t, g, owner, "image:Fail1", "image/png", testPNG)
	fake.objects[file.ObjectPath] = testPNG
	// 删除 paid 档定义，使 DerivePlan 的 LoadPlan 失败
	if err := g.Where("id = ?", "paid").Delete(&model.Plan{}).Error; err != nil {
		t.Fatalf("删除付费档失败: %v", err)
	}

	if w := doRaw(r, http.MethodGet, "/api/media/image:Fail1", nil, "", token, nil); w.Code != http.StatusInternalServerError {
		t.Fatalf("档位派生失败 GET 应 500, got %d body=%s", w.Code, w.Body.String())
	}
	if w := doRaw(r, http.MethodPost, "/api/media/image:Fail1/download", nil, "", token, nil); w.Code != http.StatusInternalServerError {
		t.Fatalf("档位派生失败申请下载应 500, got %d body=%s", w.Code, w.Body.String())
	}
}

// 社区浏览者的展示路径放行仍是 findOwned 社区分支的既有行为：作者免费档出主对象。
// 该用例与 TestDownloadRequestCommunityViewerForbidden 成对，证明夹具真实命中社区分支。
func TestMediaCommunityReadStillAllowed(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	fake := newFakeStorage("s3")
	fake.presignURL = "https://s3.example.com/test-bucket/object?X-Amz-Signature=fake"
	r := newResourceRouter(t, g, cfg, fake)
	owner := createUser(t, g, "pubowner@example.com", "pubowner", "password123", true)
	viewer := createUser(t, g, "pubviewer@example.com", "pubviewer", "password123", true)
	viewerToken := accessToken(t, cfg, &viewer)

	key := "image:Pub1"
	file := seedMediaFile(t, g, owner, key, "image/png", testPNG)
	fake.objects[file.ObjectPath] = testPNG
	asset := model.Asset{ID: uuid.New(), UserID: owner.ID, Kind: "image", Title: "作品", Data: datatypes.JSON("{}"), StorageKey: key}
	if err := g.Create(&asset).Error; err != nil {
		t.Fatalf("写入素材失败: %v", err)
	}
	work := model.CommunityWork{ID: uuid.New(), UserID: owner.ID, AssetID: asset.ID, Title: "作品", Status: "published", ModerationOK: true}
	if err := g.Create(&work).Error; err != nil {
		t.Fatalf("写入社区作品失败: %v", err)
	}

	// 只读展示放行：浏览者 GET 已发布作品本体 → 302（s3 主对象预签名）
	w := doRaw(r, http.MethodGet, "/api/media/"+key, nil, "", viewerToken, nil)
	if w.Code != http.StatusFound {
		t.Fatalf("社区浏览者 GET 展示应放行, got %d body=%s", w.Code, w.Body.String())
	}
}
