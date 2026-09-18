package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/infinite-canvas/server/internal/auth"
	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/db"
	"github.com/infinite-canvas/server/internal/mail"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/storage"
	"github.com/infinite-canvas/server/internal/watermark"
)

// testPNG 是带真实 PNG 魔数的最小上传夹具（33 字节）：上传侧会嗅探文件头，
// 声明 image/* 而内容不是图片会被 400 拒绝，测试夹具必须用真图片字节。
var testPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")

// testPNG2 是内容不同的第二份 PNG 夹具（34 字节），供覆盖上传断言内容变化。
var testPNG2 = append(append([]byte(nil), testPNG...), 'x')

// testWebP 是带真实 WebP 魔数（RIFF....WEBPVP8）的最小夹具：orig 下发按文件头
// 嗅探实际类型（评审 E-1），夹具必须真的会被 http.DetectContentType 识别为 image/webp。
var testWebP = append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 \x10\x00\x00\x00"), bytes.Repeat([]byte{0x00}, 16)...)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
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
	// 与生产启动一致：同步系统角色与权限点投影，管理接口的权限判定依赖它。
	if err := authz.Sync(g); err != nil {
		t.Fatalf("同步角色与权限目录失败: %v", err)
	}
	return g
}

func testConfig() *config.Config {
	return &config.Config{
		JWTSecret:                  "test-secret-test-secret-test-secret",
		CredentialKey:              "0123456789abcdef0123456789abcdef",
		RegistrationEnabled:        true,
		FreeGrantEnabled:           true,
		FreeGrantCampaignID:        "signup-test",
		FreeGrantDailyBudgetMicros: 1_000_000,
		FreeGrantRiskThreshold:     100,
		MailDriver:                 "log",
	}
}

func testMailer() *mail.Mailer {
	return mail.New(mail.Config{Driver: "log", AppBaseURL: "http://localhost:3000"})
}

func newAuthRouter(t *testing.T, cfg *config.Config, h *AuthHandler) *gin.Engine {
	t.Helper()
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	regLim := middleware.NewLimiter(time.Hour, 10)
	loginLim := middleware.NewLimiter(10*time.Minute, 20)
	mailLim := middleware.NewLimiter(time.Hour, 10)
	secret := []byte(cfg.JWTSecret)
	ipKey := func(prefix string) func(*gin.Context) string {
		return func(c *gin.Context) string { return prefix + middleware.ClientIP(c) }
	}
	authGroup := r.Group("/api/auth")
	authGroup.POST("/register", middleware.RateLimit(regLim, ipKey("reg:")), h.Register)
	authGroup.POST("/login", middleware.RateLimit(loginLim, ipKey("login:")), h.Login)
	authGroup.POST("/refresh", h.Refresh)
	authGroup.POST("/logout", h.Logout)
	authGroup.POST("/verify-email/send", middleware.Auth(secret), middleware.RateLimit(mailLim, ipKey("mail:")), h.VerifyEmailSend)
	authGroup.POST("/verify-email", h.VerifyEmail)
	authGroup.POST("/password/forgot", middleware.RateLimit(mailLim, ipKey("mail:")), h.ForgotPassword)
	authGroup.POST("/password/reset", h.ResetPassword)
	return r
}

func newAccountRouter(t *testing.T, g *gorm.DB, cfg *config.Config) *gin.Engine {
	t.Helper()
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	acct := NewAccountHandler(g, cfg, service.NewFreeGrantService(g), NewAuthHandler(g, cfg, testMailer()))
	me := r.Group("/api/me", middleware.Auth([]byte(cfg.JWTSecret)))
	me.POST("/free-grant/claim", acct.ClaimFreeGrant)
	return r
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLogs 捕获 slog 输出，用于从 log 邮件驱动里取出验证/重置链接中的令牌。
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

// lastEmailToken 从已捕获日志里取出最近一次邮件链接中的令牌明文。
func lastEmailToken(t *testing.T, logs *syncBuffer) string {
	t.Helper()
	text := logs.String()
	idx := bytes.LastIndex([]byte(text), []byte("token="))
	if idx < 0 {
		t.Fatalf("日志中未找到邮件令牌, logs=%s", text)
	}
	rest := text[idx+len("token="):]
	end := 0
	for end < len(rest) && (rest[end] == '-' || rest[end] == '_' ||
		(rest[end] >= '0' && rest[end] <= '9') ||
		(rest[end] >= 'a' && rest[end] <= 'z') ||
		(rest[end] >= 'A' && rest[end] <= 'Z')) {
		end++
	}
	if end == 0 {
		t.Fatalf("日志中的邮件令牌为空, logs=%s", text)
	}
	return rest[:end]
}

func doJSON(r http.Handler, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return doJSONWithToken(r, method, path, body, "", cookies...)
}

func doAuthJSON(r http.Handler, method, path, accessToken string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return doJSONWithToken(r, method, path, body, accessToken, cookies...)
}

func doJSONWithToken(r http.Handler, method, path string, body any, accessToken string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	for _, c := range cookies {
		if c != nil {
			req.AddCookie(c)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func findCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// errorCode 从统一错误响应里取出业务错误码。
func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析错误响应失败: %v body=%s", err, w.Body.String())
	}
	return body.Error.Code
}

// tableReadBarrier 让 n 个并发请求都完成对指定表的读取后再继续，
// 用于稳定构造「都读到同一行、再竞争写入」的并发场景（如令牌一次性消费）。
func tableReadBarrier(g *gorm.DB, table string, n int) {
	var mu sync.Mutex
	arrived := 0
	release := make(chan struct{})
	g.Callback().Query().After("gorm:query").Register("test:"+table+"_read_barrier", func(tx *gorm.DB) {
		if tx.Statement.Table != table {
			return
		}
		mu.Lock()
		arrived++
		if arrived == n {
			close(release)
		}
		mu.Unlock()
		<-release
	})
}

// refreshReadBarrier 让 n 个并发刷新请求都查完令牌后再继续，
// 确保测试稳定命中「并发轮换竞争失败」分支，而不是读到已撤销令牌的复用检测分支。
func refreshReadBarrier(g *gorm.DB, n int) {
	tableReadBarrier(g, "refresh_tokens", n)
}

type sessionResp struct {
	AccessToken string `json:"accessToken"`
	User        struct {
		ID            string `json:"id"`
		EmailVerified bool   `json:"emailVerified"`
	} `json:"user"`
}

func decodeSession(t *testing.T, w *httptest.ResponseRecorder) sessionResp {
	t.Helper()
	var resp sessionResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v, body=%s", err, w.Body.String())
	}
	return resp
}

func registerUser(t *testing.T, r http.Handler, email, username, password string) (*httptest.ResponseRecorder, sessionResp, *http.Cookie) {
	t.Helper()
	w := doJSON(r, http.MethodPost, "/api/auth/register", map[string]string{
		"email": email, "username": username, "password": password,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("注册失败: code=%d body=%s", w.Code, w.Body.String())
	}
	sess := decodeSession(t, w)
	cookie := findCookie(w, RefreshCookieName)
	if cookie == nil {
		t.Fatal("注册未下发 refresh cookie")
	}
	return w, sess, cookie
}

// createUser 直接写入用户，密码哈希用最低 cost，加速登录相关用例。
func createUser(t *testing.T, g *gorm.DB, email, username, password string, verified bool) model.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("生成密码哈希失败: %v", err)
	}
	user := model.User{
		ID:           uuid.New(),
		Email:        email,
		Username:     username,
		PasswordHash: string(hash),
		DisplayName:  username,
		Role:         "user",
		Status:       "active",
	}
	if verified {
		now := time.Now()
		user.EmailVerifiedAt = &now
	}
	if err := g.Create(&user).Error; err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	return user
}

func accessToken(t *testing.T, cfg *config.Config, user *model.User) string {
	t.Helper()
	token, err := auth.IssueAccessToken(user, []byte(cfg.JWTSecret))
	if err != nil {
		t.Fatalf("签发 access token 失败: %v", err)
	}
	return token
}

// setUserRole 直接写库分配角色（同时更新旧 role 投影列），用于构造后台角色夹具。
func setUserRole(t *testing.T, g *gorm.DB, user *model.User, roleKey *string) {
	t.Helper()
	if err := authz.AssignRole(g, user.ID, roleKey); err != nil {
		t.Fatalf("分配角色失败: %v", err)
	}
	user.RoleKey = roleKey
	user.Role = authz.RoleProjection(roleKey)
}

// promoteAdmin 把用户提升为系统角色管理员。
func promoteAdmin(t *testing.T, g *gorm.DB, user *model.User) {
	t.Helper()
	key := authz.SystemRoleKey
	setUserRole(t, g, user, &key)
}

// ===== 第二期资源接口测试辅助 =====

// newResourceRouter 注册画布、素材、生成记录与媒体四组路由，媒体驱动可注入。
func newResourceRouter(t *testing.T, g *gorm.DB, cfg *config.Config, stor storage.Storage) *gin.Engine {
	return newResourceRouterWithModeration(t, g, cfg, stor, nil)
}

// newResourceRouterWithModeration 允许注入审核服务，用于上传审核测试。
func newResourceRouterWithModeration(t *testing.T, g *gorm.DB, cfg *config.Config, stor storage.Storage, moderationService *service.ModerationService) *gin.Engine {
	t.Helper()
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	secret := []byte(cfg.JWTSecret)
	canvasH := NewCanvasHandler(g)
	assetH := NewAssetHandler(g)
	genH := NewGenerationHandler(g)
	mediaH := NewMediaHandler(g, stor, moderationService, secret, watermark.NewService("", ""))

	api := r.Group("/api")
	canvases := api.Group("/canvases", middleware.Auth(secret))
	canvases.GET("", canvasH.List)
	canvases.POST("", canvasH.Create)
	canvases.GET("/:id", canvasH.Get)
	canvases.PUT("/:id", canvasH.Update)
	canvases.PATCH("/:id", canvasH.Patch)
	canvases.DELETE("/:id", canvasH.Delete)

	assets := api.Group("/assets", middleware.Auth(secret))
	assets.GET("", assetH.List)
	assets.POST("", assetH.Create)
	assets.GET("/:id", assetH.Get)
	assets.PATCH("/:id", assetH.Patch)
	assets.DELETE("/:id", assetH.Delete)

	generations := api.Group("/generations", middleware.Auth(secret))
	generations.GET("", genH.List)
	generations.GET("/:id", genH.Get)
	generations.DELETE("/:id", genH.Delete)

	media := api.Group("/media", middleware.MediaAuth(secret, g))
	media.HEAD("/:storageKey", mediaH.Head)
	media.GET("/:storageKey", mediaH.Get)
	media.PUT("/:storageKey", mediaH.Put)
	media.DELETE("/:storageKey", mediaH.Delete)
	media.POST("/:storageKey/download", mediaH.RequestDownload)

	download := api.Group("/media-download", middleware.MediaAuth(secret, g))
	download.GET("/:token", mediaH.ServeDownload)
	return r
}

// doRaw 发送原始二进制请求（媒体上传用），可选附加头部。
func doRaw(r http.Handler, method, path string, body []byte, contentType, accessToken string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		if c != nil {
			req.AddCookie(c)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v body=%s", err, w.Body.String())
	}
	return body
}

func decodeItems(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	body := decodeBody(t, w)
	raw, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("响应缺少 items 数组: %s", w.Body.String())
	}
	items := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		item, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("items 元素不是对象: %s", w.Body.String())
		}
		items = append(items, item)
	}
	return items
}

// fakeStorage 是媒体 S3 路径测试用的假驱动：不发任何网络请求。
type fakeStorage struct {
	kind       string
	objects    map[string][]byte
	deleted    []string
	gets       []string        // 记录每次 Get 的路径，供「304 不读文件体」断言
	withTTLs   []time.Duration // 记录每次 PresignWithTTL 收到的 ttl，供短时效断言
	presignURL string
	presignTTL time.Duration
	putErr     error
}

func newFakeStorage(kind string) *fakeStorage {
	return &fakeStorage{kind: kind, objects: map[string][]byte{}, presignTTL: 2 * time.Hour}
}

// newLocalStorage 返回基于调用方给定目录的本地存储驱动。
func newLocalStorage(root string) storage.Storage { return storage.NewLocal(root) }

func (f *fakeStorage) Kind() string { return f.kind }

func (f *fakeStorage) Put(_ context.Context, path string, r io.Reader, _ string) (int64, string, error) {
	if f.putErr != nil {
		return 0, "", f.putErr
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return int64(len(data)), "", err
	}
	f.objects[path] = data
	sum := sha256.Sum256(data)
	return int64(len(data)), hex.EncodeToString(sum[:]), nil
}

func (f *fakeStorage) Get(_ context.Context, path string) (io.ReadCloser, error) {
	f.gets = append(f.gets, path)
	data, ok := f.objects[path]
	if !ok {
		return nil, storage.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *fakeStorage) Presign(context.Context, string) (storage.Presigned, error) {
	if f.presignURL == "" {
		return storage.Presigned{}, errors.New("fake storage 未配置 presign URL")
	}
	return storage.Presigned{URL: f.presignURL, ExpiresAt: time.Now().Add(f.presignTTL)}, nil
}

// PresignWithTTL 是 Storage 接口新增方法（storage.T2）的最小 fake 补齐，返回精确 ttl 的到期时间。
func (f *fakeStorage) PresignWithTTL(_ context.Context, _ string, ttl time.Duration) (storage.Presigned, error) {
	f.withTTLs = append(f.withTTLs, ttl)
	if f.presignURL == "" {
		return storage.Presigned{}, errors.New("fake storage 未配置 presign URL")
	}
	return storage.Presigned{URL: f.presignURL, ExpiresAt: time.Now().Add(ttl)}, nil
}

func (f *fakeStorage) Delete(_ context.Context, path string) error {
	f.deleted = append(f.deleted, path)
	delete(f.objects, path)
	return nil
}

func (f *fakeStorage) Stat(_ context.Context, path string) (int64, error) {
	data, ok := f.objects[path]
	if !ok {
		return 0, storage.ErrObjectNotFound
	}
	return int64(len(data)), nil
}
