// Package testutil 提供跨域共享的测试公共件：测试库夹具、HTTP 请求辅助、
// 用户/令牌夹具与假存储驱动。本包禁止 import 任何业务域包（account/canvas/
// ai/billing/admin），否则域包的包内测试会形成 import 环。
package testutil

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
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/platform/membership"
	"github.com/infinite-canvas/server/internal/storage"
)

// TestPNG 是带真实 PNG 魔数的最小上传夹具（33 字节）：上传侧会嗅探文件头，
// 声明 image/* 而内容不是图片会被 400 拒绝，测试夹具必须用真图片字节。
var TestPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")

// TestPNG2 是内容不同的第二份 PNG 夹具（34 字节），供覆盖上传断言内容变化。
var TestPNG2 = append(append([]byte(nil), TestPNG...), 'x')

// TestWebP 是带真实 WebP 魔数（RIFF....WEBPVP8）的最小夹具：orig 下发按文件头
// 嗅探实际类型（评审 E-1），夹具必须真的会被 http.DetectContentType 识别为 image/webp。
var TestWebP = append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 \x10\x00\x00\x00"), bytes.Repeat([]byte{0x00}, 16)...)

// NewTestDB 打开一个独立的内存 SQLite 并完成迁移、档位与角色目录同步。
func NewTestDB(t *testing.T) *gorm.DB {
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
	if err := db.SeedMembershipPlans(g); err != nil {
		t.Fatalf("写入默认档位失败: %v", err)
	}
	// 与生产启动一致：同步系统角色与权限点投影，管理接口的权限判定依赖它。
	if err := authz.Sync(g); err != nil {
		t.Fatalf("同步角色与权限目录失败: %v", err)
	}
	return g
}

// TestConfig 返回覆盖登录/注册/赠送链路的最小配置。
func TestConfig() *config.Config {
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

// TestMailer 返回 log 驱动的邮件器，配合 CaptureLogs 从日志取令牌。
func TestMailer() *mail.Mailer {
	return mail.New(mail.Config{Driver: "log", AppBaseURL: "http://localhost:3000"})
}

// NewRouter 构建 gin 测试引擎并交给 register 挂载路由。
func NewRouter(t *testing.T, register func(r *gin.Engine)) *gin.Engine {
	t.Helper()
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	register(r)
	return r
}

// SyncBuffer 是并发安全的日志缓冲。
type SyncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *SyncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *SyncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// CaptureLogs 捕获 slog 输出，用于从 log 邮件驱动里取出验证/重置链接中的令牌。
func CaptureLogs(t *testing.T) *SyncBuffer {
	t.Helper()
	buf := &SyncBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

// LastEmailToken 从已捕获日志里取出最近一次邮件链接中的令牌明文。
func LastEmailToken(t *testing.T, logs *SyncBuffer) string {
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

func DoJSON(r http.Handler, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return DoJSONWithToken(r, method, path, body, "", cookies...)
}

func DoAuthJSON(r http.Handler, method, path, accessToken string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return DoJSONWithToken(r, method, path, body, accessToken, cookies...)
}

func DoJSONWithToken(r http.Handler, method, path string, body any, accessToken string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
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

func FindCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// ErrorCode 从统一错误响应里取出业务错误码。
func ErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
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

// TableReadBarrier 让 n 个并发请求都完成对指定表的读取后再继续，
// 用于稳定构造「都读到同一行、再竞争写入」的并发场景（如令牌一次性消费）。
func TableReadBarrier(g *gorm.DB, table string, n int) {
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

// RefreshReadBarrier 让 n 个并发刷新请求都查完令牌后再继续，
// 确保测试稳定命中「并发轮换竞争失败」分支，而不是读到已撤销令牌的复用检测分支。
func RefreshReadBarrier(g *gorm.DB, n int) {
	TableReadBarrier(g, "sessions", n)
}

// SessionResp 是注册/登录响应的会话部分。
type SessionResp struct {
	AccessToken string `json:"accessToken"`
	User        struct {
		ID            string `json:"id"`
		EmailVerified bool   `json:"emailVerified"`
	} `json:"user"`
}

func DecodeSession(t *testing.T, w *httptest.ResponseRecorder) SessionResp {
	t.Helper()
	var resp SessionResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v, body=%s", err, w.Body.String())
	}
	return resp
}

// RegisterUser 通过注册接口创建用户并返回会话与 refresh cookie。
// refreshCookieName 由调用方传入（各域的 cookie 契约常量）。
func RegisterUser(t *testing.T, r http.Handler, email, username, password, refreshCookieName string) (*httptest.ResponseRecorder, SessionResp, *http.Cookie) {
	t.Helper()
	w := DoJSON(r, http.MethodPost, "/api/v1/auth/register", map[string]string{
		"email": email, "username": username, "password": password,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("注册失败: code=%d body=%s", w.Code, w.Body.String())
	}
	sess := DecodeSession(t, w)
	cookie := FindCookie(w, refreshCookieName)
	if cookie == nil {
		t.Fatal("注册未下发 refresh cookie")
	}
	return w, sess, cookie
}

// CreateUser 直接写入用户，密码哈希用最低 cost，加速登录相关用例。
// 平台账本行与免费存储配额随夹具一起建（与注册事务一致），
// 读余额/配额的接口不需要处理「行不存在」。
func CreateUser(t *testing.T, g *gorm.DB, email, username, password string, verified bool) model.PlatformUser {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("生成密码哈希失败: %v", err)
	}
	user := model.PlatformUser{
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
	if err := g.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		if err := billing.NewService(g, model.ProductCanvas).EnsureAccount(tx, user.ID); err != nil {
			return err
		}
		return membership.NewService(g).SyncQuotaWithin(tx, user.ID, time.Now())
	}); err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	return user
}

// AccessToken 按测试配置为用户签发 access token。
func AccessToken(t *testing.T, cfg *config.Config, user *model.PlatformUser) string {
	t.Helper()
	token, err := auth.IssueAccessToken(user, []byte(cfg.JWTSecret))
	if err != nil {
		t.Fatalf("签发 access token 失败: %v", err)
	}
	return token
}

// SetUserRole 直接写库分配角色（同时更新旧 role 投影列），用于构造后台角色夹具。
func SetUserRole(t *testing.T, g *gorm.DB, user *model.PlatformUser, roleKey *string) {
	t.Helper()
	if err := authz.AssignRole(g, user.ID, roleKey); err != nil {
		t.Fatalf("分配角色失败: %v", err)
	}
	user.RoleKey = roleKey
	user.Role = authz.RoleProjection(roleKey)
}

// PromoteAdmin 把用户提升为系统角色管理员。
func PromoteAdmin(t *testing.T, g *gorm.DB, user *model.PlatformUser) {
	t.Helper()
	key := authz.SystemRoleKey
	SetUserRole(t, g, user, &key)
}

// DoRaw 发送原始二进制请求（媒体上传用），可选附加头部。
func DoRaw(r http.Handler, method, path string, body []byte, contentType, accessToken string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
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

func DecodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v body=%s", err, w.Body.String())
	}
	return body
}

func DecodeItems(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	body := DecodeBody(t, w)
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

// FakeStorage 是媒体 S3 路径测试用的假驱动：不发任何网络请求。
type FakeStorage struct {
	kind       string
	Objects    map[string][]byte
	Deleted    []string
	Gets       []string        // 记录每次 Get 的路径，供「304 不读文件体」断言
	WithTTLs   []time.Duration // 记录每次 PresignWithTTL 收到的 ttl，供短时效断言
	PresignURL string
	PresignTTL time.Duration
	PutErr     error
}

func NewFakeStorage(kind string) *FakeStorage {
	return &FakeStorage{kind: kind, Objects: map[string][]byte{}, PresignTTL: 2 * time.Hour}
}

// NewLocalStorage 返回基于调用方给定目录的本地存储驱动。
func NewLocalStorage(root string) storage.Storage { return storage.NewLocal(root) }

func (f *FakeStorage) Kind() string { return f.kind }

func (f *FakeStorage) Put(_ context.Context, path string, r io.Reader, _ string) (int64, string, error) {
	if f.PutErr != nil {
		return 0, "", f.PutErr
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return int64(len(data)), "", err
	}
	f.Objects[path] = data
	sum := sha256.Sum256(data)
	return int64(len(data)), hex.EncodeToString(sum[:]), nil
}

func (f *FakeStorage) Get(_ context.Context, path string) (io.ReadCloser, error) {
	f.Gets = append(f.Gets, path)
	data, ok := f.Objects[path]
	if !ok {
		return nil, storage.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *FakeStorage) Presign(context.Context, string) (storage.Presigned, error) {
	if f.PresignURL == "" {
		return storage.Presigned{}, errors.New("fake storage 未配置 presign URL")
	}
	return storage.Presigned{URL: f.PresignURL, ExpiresAt: time.Now().Add(f.PresignTTL)}, nil
}

// PresignWithTTL 是 Storage 接口新增方法（storage.T2）的最小 fake 补齐，返回精确 ttl 的到期时间。
func (f *FakeStorage) PresignWithTTL(_ context.Context, _ string, ttl time.Duration) (storage.Presigned, error) {
	f.WithTTLs = append(f.WithTTLs, ttl)
	if f.PresignURL == "" {
		return storage.Presigned{}, errors.New("fake storage 未配置 presign URL")
	}
	return storage.Presigned{URL: f.PresignURL, ExpiresAt: time.Now().Add(ttl)}, nil
}

func (f *FakeStorage) Delete(_ context.Context, path string) error {
	f.Deleted = append(f.Deleted, path)
	delete(f.Objects, path)
	return nil
}

func (f *FakeStorage) Stat(_ context.Context, path string) (int64, error) {
	data, ok := f.Objects[path]
	if !ok {
		return 0, storage.ErrObjectNotFound
	}
	return int64(len(data)), nil
}
