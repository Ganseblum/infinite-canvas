package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/infinite-canvas/server/internal/auth"
	"github.com/infinite-canvas/server/internal/model"
)

var maxAgeRe = regexp.MustCompile(`^private, max-age=(\d+)$`)

func countFiles(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历媒体目录失败: %v", err)
	}
	return count
}

func TestMediaLocalUploadHeadGetDelete(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	root := t.TempDir()
	r := newResourceRouter(t, g, cfg, newLocalStorage(root))
	user := createUser(t, g, "media@example.com", "mediauser", "password123", true)
	token := accessToken(t, cfg, &user)

	payload := testPNG
	w := doRaw(r, http.MethodPut, "/api/media/image:Abc123", payload, "image/png", token, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("上传失败: code=%d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	sum := sha256.Sum256(payload)
	wantChecksum := hex.EncodeToString(sum[:])
	if body["checksum"] != wantChecksum || body["storageKey"] != "image:Abc123" ||
		body["bytes"].(float64) != float64(len(payload)) || body["mimeType"] != "image/png" {
		t.Fatalf("上传响应不符: %v", body)
	}

	// 落盘路径由服务端按 {root}/{userID}/image/Abc123 组装
	diskPath := filepath.Join(root, user.ID.String(), "image", "Abc123")
	if data, err := os.ReadFile(diskPath); err != nil || string(data) != string(payload) {
		t.Fatalf("磁盘文件不符: err=%v data=%q", err, data)
	}

	// HEAD
	w = doRaw(r, http.MethodHead, "/api/media/image:Abc123", nil, "", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("HEAD 应 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Length") != strconv.Itoa(len(payload)) ||
		w.Header().Get("Content-Type") != "image/png" ||
		w.Header().Get("X-Checksum") != wantChecksum {
		t.Fatalf("HEAD 响应头不符: %v", w.Header())
	}

	// GET 本地驱动直接回流二进制，带 ETag 与一年 immutable
	w = doRaw(r, http.MethodGet, "/api/media/image:Abc123", nil, "", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET 应 200, got %d", w.Code)
	}
	if w.Body.String() != string(payload) {
		t.Fatalf("GET 内容不符: %q", w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Fatalf("本地驱动缓存头不符: %s", w.Header().Get("Cache-Control"))
	}
	if w.Header().Get("ETag") != `"`+wantChecksum+`"` {
		t.Fatalf("ETag 应为 checksum 引号形式: %s", w.Header().Get("ETag"))
	}

	// 重复 PUT 是覆盖语义：唯一约束更新原行
	w = doRaw(r, http.MethodPut, "/api/media/image:Abc123", testPNG2, "image/png", token, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("覆盖上传失败: %d", w.Code)
	}
	var count int64
	if err := g.Model(&model.MediaFile{}).Where("user_id = ? AND storage_key = ?", user.ID, "image:Abc123").Count(&count).Error; err != nil {
		t.Fatalf("统计媒体记录失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("重复上传应只有一条记录, got %d", count)
	}

	// DELETE 删记录与对象，重复删除 204
	if w := doRaw(r, http.MethodDelete, "/api/media/image:Abc123", nil, "", token, nil); w.Code != http.StatusNoContent {
		t.Fatalf("删除应 204, got %d", w.Code)
	}
	if _, err := os.Stat(diskPath); !os.IsNotExist(err) {
		t.Fatalf("磁盘对象应被删除: %v", err)
	}
	if w := doRaw(r, http.MethodGet, "/api/media/image:Abc123", nil, "", token, nil); w.Code != http.StatusNotFound {
		t.Fatalf("删除后 GET 应 404, got %d", w.Code)
	}
	if w := doRaw(r, http.MethodDelete, "/api/media/image:Abc123", nil, "", token, nil); w.Code != http.StatusNoContent {
		t.Fatalf("重复删除应 204, got %d", w.Code)
	}
}

func TestMediaStorageKeyAndPathTraversalRejected(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	root := t.TempDir()
	r := newResourceRouter(t, g, cfg, newLocalStorage(root))
	user := createUser(t, g, "key@example.com", "keyuser", "password123", true)
	token := accessToken(t, cfg, &user)

	for _, key := range []string{"image:bad.dot", "image:", "unknown:abc", "image:abc%20def"} {
		w := doRaw(r, http.MethodPut, "/api/media/"+key, testPNG, "image/png", token, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("非法 key %q PUT 应 400, got %d body=%s", key, w.Code, w.Body.String())
		}
		if code := errorCode(t, w); code != "VALIDATION_FAILED" {
			t.Fatalf("非法 key %q 错误码应为 VALIDATION_FAILED, got %s", key, code)
		}
		w = doRaw(r, http.MethodGet, "/api/media/"+key, nil, "", token, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("非法 key %q GET 应 400, got %d", key, w.Code)
		}
	}

	// 路径穿越：编码后的 ../ 无法匹配单段路由，同样不能触达文件系统
	w := doRaw(r, http.MethodGet, "/api/media/image:..%2F..%2Fetc%2Fpasswd", nil, "", token, nil)
	if w.Code == http.StatusOK {
		t.Fatalf("路径穿越请求不应成功: %d", w.Code)
	}
	if countFiles(t, root) != 0 {
		t.Fatalf("根目录下不应出现任何文件")
	}

	// storageKey 必须经服务端重装路径，用户输入的 ../ 不会成为真实路径
	if _, err := os.Stat(filepath.Join(root, "..", "etc", "passwd")); err == nil {
		t.Fatal("不应在媒体根目录之外创建文件")
	}
}

func TestMediaEmailNotVerifiedBlocksUploadOnly(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newResourceRouter(t, g, cfg, newLocalStorage(t.TempDir()))
	user := createUser(t, g, "unverified@example.com", "unverified", "password123", false)
	token := accessToken(t, cfg, &user)

	w := doRaw(r, http.MethodPut, "/api/media/image:Abc123", testPNG, "image/png", token, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("未验证邮箱上传应 403, got %d body=%s", w.Code, w.Body.String())
	}
	if code := errorCode(t, w); code != "EMAIL_NOT_VERIFIED" {
		t.Fatalf("错误码应为 EMAIL_NOT_VERIFIED, got %s", code)
	}
	// 画布读写不受限制
	if w := doAuthJSON(r, http.MethodPost, "/api/canvases", token, canvasPayload("未验证也能建")); w.Code != http.StatusCreated {
		t.Fatalf("未验证用户创建画布应成功: %d", w.Code)
	}
	if w := doAuthJSON(r, http.MethodPost, "/api/assets", token, map[string]any{
		"kind": "text", "title": "文本", "data": map[string]any{"content": "x"},
	}); w.Code != http.StatusCreated {
		t.Fatalf("未验证用户创建文本素材应成功: %d body=%s", w.Code, w.Body.String())
	}
}

func TestMediaFileTooLargeBoundary(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	root := t.TempDir()
	r := newResourceRouter(t, g, cfg, newLocalStorage(root))
	user := createUser(t, g, "toolarge@example.com", "toolarge", "password123", true)
	token := accessToken(t, cfg, &user)

	// 把免费档单文件上限改成 16 字节，验证边界与超限
	if err := g.Model(&model.Plan{}).Where("id = ?", "free").Update("max_file_bytes", 16).Error; err != nil {
		t.Fatalf("调整档位失败: %v", err)
	}

	// 恰好等于上限：允许（用 PNG 头的前 16 字节，text/plain 已不在允许类型清单内）
	w := doRaw(r, http.MethodPut, "/api/media/image:Exact16", testPNG[:16], "image/png", token, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("等于上限应允许: %d body=%s", w.Code, w.Body.String())
	}
	// 超过一个字节：413，且不留记录与文件
	w = doRaw(r, http.MethodPut, "/api/media/image:Over17", testPNG[:17], "image/png", token, nil)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超过上限应 413, got %d body=%s", w.Code, w.Body.String())
	}
	if code := errorCode(t, w); code != "FILE_TOO_LARGE" {
		t.Fatalf("错误码应为 FILE_TOO_LARGE, got %s", code)
	}
	var count int64
	if err := g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&count).Error; err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("超限文件不应写库, got %d 条", count)
	}
	if got := countFiles(t, root); got != 1 {
		t.Fatalf("超限文件不应留盘, 根目录文件数=%d", got)
	}
}

func TestMediaChecksumMismatchLeavesNothing(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	root := t.TempDir()
	r := newResourceRouter(t, g, cfg, newLocalStorage(root))
	user := createUser(t, g, "checksum@example.com", "checksum", "password123", true)
	token := accessToken(t, cfg, &user)

	w := doRaw(r, http.MethodPut, "/api/media/image:Sum1", testPNG, "image/png", token,
		map[string]string{"X-Checksum": "deadbeef"})
	if w.Code != http.StatusConflict {
		t.Fatalf("校验和不一致应 409, got %d body=%s", w.Code, w.Body.String())
	}
	if code := errorCode(t, w); code != "CHECKSUM_MISMATCH" {
		t.Fatalf("错误码应为 CHECKSUM_MISMATCH, got %s", code)
	}
	var count int64
	if err := g.Model(&model.MediaFile{}).Count(&count).Error; err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("校验失败不应写库, got %d 条", count)
	}
	if got := countFiles(t, root); got != 0 {
		t.Fatalf("校验失败不应留文件, 文件数=%d", got)
	}

	// 正确的 X-Checksum 可以上传
	sum := sha256.Sum256(testPNG)
	w = doRaw(r, http.MethodPut, "/api/media/image:Sum1", testPNG, "image/png", token,
		map[string]string{"X-Checksum": hex.EncodeToString(sum[:])})
	if w.Code != http.StatusCreated {
		t.Fatalf("正确校验和应上传成功: %d body=%s", w.Code, w.Body.String())
	}

	// Content-Type 必填
	w = doRaw(r, http.MethodPut, "/api/media/image:NoType", []byte("data"), "", token, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺少 Content-Type 应 400, got %d", w.Code)
	}
}

func TestMediaReadAuthBearerAndCookie(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newResourceRouter(t, g, cfg, newLocalStorage(t.TempDir()))
	user := createUser(t, g, "mediaauth@example.com", "mediaauth", "password123", true)
	other := createUser(t, g, "mediaother@example.com", "mediaother", "password123", true)
	token := accessToken(t, cfg, &user)
	otherToken := accessToken(t, cfg, &other)

	if w := doRaw(r, http.MethodPut, "/api/media/image:Auth1", testPNG, "image/png", token, nil); w.Code != http.StatusCreated {
		t.Fatalf("上传失败: %d", w.Code)
	}

	// 无凭证 401
	if w := doRaw(r, http.MethodGet, "/api/media/image:Auth1", nil, "", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("无凭证应 401, got %d", w.Code)
	}
	// Bearer 可用
	if w := doRaw(r, http.MethodGet, "/api/media/image:Auth1", nil, "", token, nil); w.Code != http.StatusOK {
		t.Fatalf("Bearer GET 应 200, got %d", w.Code)
	}
	// ic_media cookie 可用（模拟 <img src>）
	mediaToken, err := auth.IssueMediaToken(user.ID, []byte(cfg.JWTSecret))
	if err != nil {
		t.Fatalf("签发媒体令牌失败: %v", err)
	}
	cookie := &http.Cookie{Name: auth.MediaCookieName, Value: mediaToken}
	if w := doRaw(r, http.MethodGet, "/api/media/image:Auth1", nil, "", "", nil, cookie); w.Code != http.StatusOK {
		t.Fatalf("ic_media cookie GET 应 200, got %d body=%s", w.Code, w.Body.String())
	}
	// cookie 对 HEAD 同样生效
	if w := doRaw(r, http.MethodHead, "/api/media/image:Auth1", nil, "", "", nil, cookie); w.Code != http.StatusOK {
		t.Fatalf("ic_media cookie HEAD 应 200, got %d", w.Code)
	}
	// access token 当 cookie 用应 401（scope 缺失）
	accessCookie := &http.Cookie{Name: auth.MediaCookieName, Value: token}
	if w := doRaw(r, http.MethodGet, "/api/media/image:Auth1", nil, "", "", nil, accessCookie); w.Code != http.StatusUnauthorized {
		t.Fatalf("access token 冒充媒体 cookie 应 401, got %d", w.Code)
	}
	// scope 不是 media 的 JWT 伪造 cookie 应 401
	scoped := signScopedJWT(t, []byte(cfg.JWTSecret), user.ID.String(), "access")
	if w := doRaw(r, http.MethodGet, "/api/media/image:Auth1", nil, "", "", nil, &http.Cookie{Name: auth.MediaCookieName, Value: scoped}); w.Code != http.StatusUnauthorized {
		t.Fatalf("scope=access 的 JWT 伪造 cookie 应 401, got %d", w.Code)
	}
	// 损坏的 cookie 401
	if w := doRaw(r, http.MethodGet, "/api/media/image:Auth1", nil, "", "", nil, &http.Cookie{Name: auth.MediaCookieName, Value: "broken"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("损坏 cookie 应 401, got %d", w.Code)
	}
	// 媒体 token 当 Bearer 用应 401（access token 解析拒绝 scope=media）
	if w := doRaw(r, http.MethodPut, "/api/media/image:Auth1", testPNG, "image/png", mediaToken, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("媒体 token 当 Bearer 应 401, got %d body=%s", w.Code, w.Body.String())
	}
	// PUT / DELETE 不认 cookie
	if w := doRaw(r, http.MethodPut, "/api/media/image:Auth2", testPNG, "image/png", "", nil, cookie); w.Code != http.StatusUnauthorized {
		t.Fatalf("PUT 只认 Bearer, cookie 应 401, got %d", w.Code)
	}
	if w := doRaw(r, http.MethodDelete, "/api/media/image:Auth1", nil, "", "", nil, cookie); w.Code != http.StatusUnauthorized {
		t.Fatalf("DELETE 只认 Bearer, cookie 应 401, got %d", w.Code)
	}

	// 跨用户：他人的 Bearer 与 cookie 都查不到该 storageKey → 404
	if w := doRaw(r, http.MethodGet, "/api/media/image:Auth1", nil, "", otherToken, nil); w.Code != http.StatusNotFound {
		t.Fatalf("跨用户 GET 应 404, got %d", w.Code)
	}
	otherMediaToken, err := auth.IssueMediaToken(other.ID, []byte(cfg.JWTSecret))
	if err != nil {
		t.Fatalf("签发他人媒体令牌失败: %v", err)
	}
	if w := doRaw(r, http.MethodGet, "/api/media/image:Auth1", nil, "", "", nil,
		&http.Cookie{Name: auth.MediaCookieName, Value: otherMediaToken}); w.Code != http.StatusNotFound {
		t.Fatalf("跨用户 cookie GET 应 404, got %d", w.Code)
	}
}

func TestMediaS3RedirectAndCacheHeaders(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	fake := newFakeStorage("s3")
	fake.presignURL = "https://s3.example.com/test-bucket/object?X-Amz-Signature=fake"
	r := newResourceRouter(t, g, cfg, fake)
	user := createUser(t, g, "s3media@example.com", "s3media", "password123", true)
	token := accessToken(t, cfg, &user)

	// 用假驱动走完 PUT（不发网络请求），再断言 GET 的 302 与缓存头
	if w := doRaw(r, http.MethodPut, "/api/media/image:S3Key1", testPNG, "image/png", token, nil); w.Code != http.StatusCreated {
		t.Fatalf("假驱动上传失败: %d body=%s", w.Code, w.Body.String())
	}
	if len(fake.objects) != 1 {
		t.Fatalf("假驱动应记录一个对象: %v", fake.objects)
	}

	w := doRaw(r, http.MethodGet, "/api/media/image:S3Key1", nil, "", token, nil)
	if w.Code != http.StatusFound {
		t.Fatalf("s3 驱动 GET 应 302, got %d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Location") != fake.presignURL {
		t.Fatalf("Location 不符: %s", w.Header().Get("Location"))
	}
	cacheControl := w.Header().Get("Cache-Control")
	matches := maxAgeRe.FindStringSubmatch(cacheControl)
	if matches == nil {
		t.Fatalf("302 缓存格式应为 private, max-age=N: %q", cacheControl)
	}
	if strings.Contains(cacheControl, "immutable") {
		t.Fatalf("302 绝不能带 immutable: %s", cacheControl)
	}
	maxAge, _ := strconv.Atoi(matches[1])
	if maxAge < 7100 || maxAge > 7140 {
		t.Fatalf("剩余秒数应约等于 预签名 2h - 60: %d", maxAge)
	}

	// HEAD 只查索引，同样可用
	if w := doRaw(r, http.MethodHead, "/api/media/image:S3Key1", nil, "", token, nil); w.Code != http.StatusOK {
		t.Fatalf("s3 HEAD 应 200, got %d", w.Code)
	}
}

func TestSessionCookiesIssuedRotatedAndCleared(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	h := NewAuthHandler(g, cfg, testMailer())
	r := newAuthRouter(t, cfg, h)

	w, _, refreshCookie := registerUser(t, r, "cookies@example.com", "cookies", "password123")
	mediaCookie := findCookie(w, auth.MediaCookieName)
	if mediaCookie == nil || mediaCookie.Value == "" {
		t.Fatal("注册应下发 ic_media cookie")
	}
	if mediaCookie.Path != auth.MediaCookiePath || !mediaCookie.HttpOnly || mediaCookie.MaxAge <= 0 {
		t.Fatalf("ic_media cookie 属性不符: %+v", mediaCookie)
	}

	// 刷新时轮换 ic_media
	w = doJSON(r, http.MethodPost, "/api/auth/refresh", nil, refreshCookie)
	rotatedRefresh := findCookie(w, RefreshCookieName)
	rotatedMedia := findCookie(w, auth.MediaCookieName)
	if rotatedRefresh == nil || rotatedMedia == nil {
		t.Fatal("刷新应同时下发 ic_refresh 与 ic_media")
	}
	if rotatedMedia.Value == mediaCookie.Value {
		t.Fatal("刷新应轮换 ic_media")
	}

	// 登出清两枚 cookie，且各自带回原 Path
	w = doJSON(r, http.MethodPost, "/api/auth/logout", nil, rotatedRefresh)
	clearedRefresh := findCookie(w, RefreshCookieName)
	clearedMedia := findCookie(w, auth.MediaCookieName)
	if clearedRefresh == nil || clearedRefresh.MaxAge >= 0 || clearedRefresh.Path != RefreshCookiePath {
		t.Fatalf("登出应清 ic_refresh 且带回原 Path: %+v", clearedRefresh)
	}
	if clearedMedia == nil || clearedMedia.MaxAge >= 0 || clearedMedia.Path != auth.MediaCookiePath {
		t.Fatalf("登出应清 ic_media 且带回原 Path: %+v", clearedMedia)
	}
}

func signScopedJWT(t *testing.T, secret []byte, sub, scope string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub":   sub,
		"scope": scope,
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		t.Fatalf("签发测试 JWT 失败: %v", err)
	}
	return token
}
