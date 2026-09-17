package handler

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/auth"
	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/service"
)

// newAdminUsersRouter 按生产同样的中间件链注册本批涉及的路由：
// 登录/刷新/登出、改密、一个业务接口（画布列表）与管理员建号/重置密码/用户详情。
func newAdminUsersRouter(t *testing.T, g *gorm.DB, cfg *config.Config) *gin.Engine {
	t.Helper()
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	secret := []byte(cfg.JWTSecret)
	active := middleware.RequireActiveUser(g)
	gate := middleware.RequirePasswordChanged(g)
	authH := NewAuthHandler(g, cfg, testMailer())
	acct := NewAccountHandler(g, cfg, service.NewFreeGrantService(g), authH)
	adminH := NewAdminHandler(g, cfg, newFakeStorage("local"))
	canvasH := NewCanvasHandler(g)

	api := r.Group("/api")
	authGroup := api.Group("/auth")
	authGroup.POST("/login", authH.Login)
	authGroup.POST("/refresh", authH.Refresh)
	authGroup.POST("/logout", authH.Logout)

	me := api.Group("/me", middleware.Auth(secret), active, gate)
	me.POST("/password", acct.ChangePassword)

	canvases := api.Group("/canvases", middleware.Auth(secret), active, gate)
	canvases.GET("", canvasH.List)

	admin := api.Group("/admin", middleware.Auth(secret), active, gate, middleware.LoadAdminAccess(g))
	admin.POST("/users", middleware.RequirePermission(authz.PermRolesManage), adminH.CreateUser)
	admin.GET("/users/:id", middleware.RequirePermission(authz.PermUsersRead), adminH.GetUser)
	admin.POST("/users/:id/password", middleware.RequirePermission(authz.PermUsersWrite), adminH.ResetPassword)
	return r
}

// createAdminToken 建一个系统角色管理员并签发 access token。
func createAdminToken(t *testing.T, g *gorm.DB, cfg *config.Config, email, username string) string {
	t.Helper()
	admin := createUser(t, g, email, username, "password123", true)
	promoteAdmin(t, g, &admin)
	return accessToken(t, cfg, &admin)
}

func TestAdminCreateUserReturnsOneTimeTemporaryPassword(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newAdminUsersRouter(t, g, cfg)
	token := createAdminToken(t, g, cfg, "creator@example.com", "creator")

	w := doAuthJSON(r, http.MethodPost, "/api/admin/users", token, map[string]string{
		"email": "New.User@Example.com", "displayName": "新同事", "roleKey": "admin",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("建号应返回 201, got %d %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	password, _ := body["temporaryPassword"].(string)
	if len(password) < 8 {
		t.Fatalf("响应必须带回一次性临时密码: %s", w.Body.String())
	}
	user, ok := body["user"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 user 对象: %s", w.Body.String())
	}
	if user["email"] != "new.user@example.com" || user["username"] != "newuser" {
		t.Fatalf("邮箱应规范化、用户名取邮箱本地部分: %s", w.Body.String())
	}
	if user["mustChangePassword"] != true || user["emailVerified"] != true {
		t.Fatalf("新账号必须强制改密且邮箱已断言: %s", w.Body.String())
	}
	if user["roleKey"] != "admin" || user["displayName"] != "新同事" {
		t.Fatalf("角色与昵称应写入: %s", w.Body.String())
	}

	var row model.User
	if err := g.First(&row, "email = ?", "new.user@example.com").Error; err != nil {
		t.Fatalf("新账号未写库: %v", err)
	}
	if !row.MustChangePassword || row.EmailVerifiedAt == nil {
		t.Fatalf("库中 must_change_password/email_verified_at 未置位: %+v", row)
	}
	if !auth.CheckPassword(row.PasswordHash, password) {
		t.Fatal("返回的临时密码必须能通过哈希校验")
	}
	// 明文不落库：密码列里存的是 bcrypt 哈希而不是原文。
	var plainRows int64
	if err := g.Model(&model.User{}).Where("password_hash = ?", password).Count(&plainRows).Error; err != nil {
		t.Fatalf("查询密码列失败: %v", err)
	}
	if plainRows != 0 {
		t.Fatal("临时密码明文不允许落库")
	}

	var audit model.AdminAuditLog
	if err := g.First(&audit, "action = ?", "user.create").Error; err != nil {
		t.Fatalf("建号必须写审计: %v", err)
	}
	summary := string(audit.AfterSummary)
	if audit.TargetType != "user" || audit.TargetID != row.ID.String() {
		t.Fatalf("审计目标错误: %+v", audit)
	}
	if !strings.Contains(summary, "由管理员建号并断言邮箱") || !strings.Contains(summary, "new.user@example.com") {
		t.Fatalf("审计摘要应记录建号与断言邮箱: %s", summary)
	}
	if strings.Contains(summary, password) {
		t.Fatalf("审计摘要绝不允许出现临时密码: %s", summary)
	}
	// 临时密码只出现这一次：用户详情接口不再回传明文。
	detail := doAuthJSON(r, http.MethodGet, "/api/admin/users/"+row.ID.String(), token, nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("读取用户详情失败: %d %s", detail.Code, detail.Body.String())
	}
	if strings.Contains(detail.Body.String(), password) {
		t.Fatalf("用户详情接口不允许回传临时密码: %s", detail.Body.String())
	}
}

func TestAdminCreateUserRejectsDuplicateEmail(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newAdminUsersRouter(t, g, cfg)
	token := createAdminToken(t, g, cfg, "creator2@example.com", "creator2")
	createUser(t, g, "taken@example.com", "takenuser", "password123", false)

	w := doAuthJSON(r, http.MethodPost, "/api/admin/users", token, map[string]string{
		"email": "taken@example.com", "displayName": "重复", "roleKey": "admin",
	})
	if w.Code != http.StatusConflict || errorCode(t, w) != "EMAIL_TAKEN" {
		t.Fatalf("重复邮箱应复用既有 409 EMAIL_TAKEN, got %d %s", w.Code, w.Body.String())
	}
	var count int64
	g.Model(&model.User{}).Where("email = ?", "taken@example.com").Count(&count)
	if count != 1 {
		t.Fatalf("重复邮箱建号不应写入新行, count=%d", count)
	}
}

func TestAdminCreateUserRejectsUnknownRole(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newAdminUsersRouter(t, g, cfg)
	token := createAdminToken(t, g, cfg, "creator3@example.com", "creator3")

	w := doAuthJSON(r, http.MethodPost, "/api/admin/users", token, map[string]string{
		"email": "ghost-role@example.com", "displayName": "未知角色", "roleKey": "ghost",
	})
	if w.Code != http.StatusBadRequest || errorCode(t, w) != "VALIDATION_FAILED" {
		t.Fatalf("未知 roleKey 应 400 VALIDATION_FAILED, got %d %s", w.Code, w.Body.String())
	}
	var count int64
	g.Model(&model.User{}).Where("email = ?", "ghost-role@example.com").Count(&count)
	if count != 0 {
		t.Fatal("角色不存在的请求不应建号")
	}
}

func TestAdminCreateUserRequiresRolesManagePermission(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newAdminUsersRouter(t, g, cfg)
	// 只给 users.read 的自定义角色：能看到用户，但不能建号。
	if err := g.Create(&model.Role{Key: "viewer", Name: "只读"}).Error; err != nil {
		t.Fatalf("创建角色失败: %v", err)
	}
	if err := g.Create(&model.RolePermission{RoleKey: "viewer", PermissionKey: authz.PermUsersRead}).Error; err != nil {
		t.Fatalf("分配权限失败: %v", err)
	}
	viewer := createUser(t, g, "viewer@example.com", "viewer", "password123", true)
	viewerKey := "viewer"
	setUserRole(t, g, &viewer, &viewerKey)
	token := accessToken(t, cfg, &viewer)

	w := doAuthJSON(r, http.MethodPost, "/api/admin/users", token, map[string]string{
		"email": "should-not-exist@example.com", "displayName": "越权", "roleKey": "admin",
	})
	if w.Code != http.StatusForbidden || errorCode(t, w) != "FORBIDDEN" {
		t.Fatalf("无 roles.manage 权限应 403 FORBIDDEN, got %d %s", w.Code, w.Body.String())
	}
	var count int64
	g.Model(&model.User{}).Where("email = ?", "should-not-exist@example.com").Count(&count)
	if count != 0 {
		t.Fatal("越权请求不应建号")
	}
}

func TestForcedPasswordChangeGate(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newAdminUsersRouter(t, g, cfg)
	user := createUser(t, g, "forced@example.com", "forced", "initial-pass-1", true)
	if err := g.Model(&model.User{}).Where("id = ?", user.ID).
		Update("must_change_password", true).Error; err != nil {
		t.Fatalf("置位 must_change_password 失败: %v", err)
	}

	// 登录响应必须带 mustChangePassword。
	w := doJSON(r, http.MethodPost, "/api/auth/login", map[string]string{
		"account": "forced@example.com", "password": "initial-pass-1",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("登录失败: %d %s", w.Code, w.Body.String())
	}
	if decodeBody(t, w)["mustChangePassword"] != true {
		t.Fatalf("登录响应必须带 mustChangePassword=true: %s", w.Body.String())
	}
	loginCookie := findCookie(w, RefreshCookieName)
	if loginCookie == nil {
		t.Fatal("登录未下发 refresh cookie")
	}
	loginToken := decodeSession(t, w).AccessToken

	// 业务接口被拦。
	w = doAuthJSON(r, http.MethodGet, "/api/canvases", loginToken, nil)
	if w.Code != http.StatusForbidden || errorCode(t, w) != "PASSWORD_CHANGE_REQUIRED" {
		t.Fatalf("强制改密期间业务接口应 403 PASSWORD_CHANGE_REQUIRED, got %d %s", w.Code, w.Body.String())
	}

	// 刷新放行并轮换 cookie。
	w = doJSON(r, http.MethodPost, "/api/auth/refresh", nil, loginCookie)
	if w.Code != http.StatusOK {
		t.Fatalf("强制改密期间刷新应放行, got %d %s", w.Code, w.Body.String())
	}
	refreshedCookie := findCookie(w, RefreshCookieName)
	refreshedToken := decodeSession(t, w).AccessToken

	// 改密接口本身放行：旧密码错误时是 401 而不是 403，说明没有被强制改密中间件拦下。
	w = doAuthJSON(r, http.MethodPost, "/api/me/password", refreshedToken, map[string]string{
		"oldPassword": "wrong-password", "newPassword": "brand-new-pass-1",
	})
	if w.Code != http.StatusUnauthorized || errorCode(t, w) != "INVALID_CREDENTIALS" {
		t.Fatalf("旧密码错误应 401 INVALID_CREDENTIALS, got %d %s", w.Code, w.Body.String())
	}

	w = doAuthJSON(r, http.MethodPost, "/api/me/password", refreshedToken, map[string]string{
		"oldPassword": "initial-pass-1", "newPassword": "brand-new-pass-1",
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("强制改密应成功, got %d %s", w.Code, w.Body.String())
	}
	newCookie := findCookie(w, RefreshCookieName)
	if newCookie == nil {
		t.Fatal("改密后应下发新的 refresh cookie")
	}

	// 标志已清、业务接口恢复正常。
	var row model.User
	if err := g.First(&row, "id = ?", user.ID).Error; err != nil {
		t.Fatalf("读取用户失败: %v", err)
	}
	if row.MustChangePassword {
		t.Fatal("改密成功后 must_change_password 必须清掉")
	}
	w = doJSON(r, http.MethodPost, "/api/auth/refresh", nil, newCookie)
	if w.Code != http.StatusOK {
		t.Fatalf("改密后刷新应正常, got %d %s", w.Code, w.Body.String())
	}
	w = doAuthJSON(r, http.MethodGet, "/api/canvases", decodeSession(t, w).AccessToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("改密后业务接口应恢复, got %d %s", w.Code, w.Body.String())
	}

	// 改密前签发的 refresh token 全部失效。
	for name, cookie := range map[string]*http.Cookie{"登录": loginCookie, "刷新": refreshedCookie} {
		w = doJSON(r, http.MethodPost, "/api/auth/refresh", nil, cookie)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("改密后%s签发的旧 refresh token 应失效, got %d %s", name, w.Code, w.Body.String())
		}
	}

	// 登出放行。
	w = doJSON(r, http.MethodPost, "/api/auth/logout", nil, newCookie)
	if w.Code != http.StatusNoContent {
		t.Fatalf("强制改密期间登出应放行, got %d %s", w.Code, w.Body.String())
	}
}

func TestPickUsernameFollowsEmailLocalPart(t *testing.T) {
	g := newTestDB(t)
	createUser(t, g, "alice@example.com", "alice", "password123", false)
	createUser(t, g, "alice2@example.com", "alice2", "password123", false)

	name, err := pickUsername(g, "alice@example.com")
	if err != nil {
		t.Fatalf("生成用户名失败: %v", err)
	}
	if name != "alice3" {
		t.Fatalf("冲突时应取最小可用数字后缀, got %s", name)
	}
	// 本地部分里的 . 与 + 不是注册规则允许的用户名字符，需要剔除；不足 3 位补齐。
	for email, want := range map[string]string{
		"a.b+c@example.com": "abc",
		"ab@example.com":    "abu",
		"名字@example.com":    "user",
	} {
		got, err := pickUsername(g, email)
		if err != nil {
			t.Fatalf("生成 %s 的用户名失败: %v", email, err)
		}
		if got != want {
			t.Fatalf("%s 应生成用户名 %s, got %s", email, want, got)
		}
	}
}

func TestAdminResetPasswordSetsMustChangeAndRevokesTokens(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newAdminUsersRouter(t, g, cfg)
	token := createAdminToken(t, g, cfg, "creator4@example.com", "creator4")
	target := createUser(t, g, "target@example.com", "targetuser", "password123", true)
	rt := model.RefreshToken{
		ID:        uuid.New(),
		UserID:    target.ID,
		TokenHash: "target-refresh-hash",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := g.Create(&rt).Error; err != nil {
		t.Fatalf("写入 refresh token 失败: %v", err)
	}

	w := doAuthJSON(r, http.MethodPost, "/api/admin/users/"+target.ID.String()+"/password", token, map[string]string{
		"password": "admin-set-pass-1",
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("管理员重置密码应 204, got %d %s", w.Code, w.Body.String())
	}
	var row model.User
	if err := g.First(&row, "id = ?", target.ID).Error; err != nil {
		t.Fatalf("读取用户失败: %v", err)
	}
	if !row.MustChangePassword {
		t.Fatal("管理员重置密码后必须置位 must_change_password")
	}
	if !auth.CheckPassword(row.PasswordHash, "admin-set-pass-1") {
		t.Fatal("重置后的密码应可通过校验")
	}
	var revoked model.RefreshToken
	if err := g.First(&revoked, "id = ?", rt.ID).Error; err != nil {
		t.Fatalf("读取 refresh token 失败: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Fatal("重置密码必须撤销该用户 refresh token")
	}
}

// TestAdminResetOwnPasswordKeepsUnforced 管理员重置自己的密码：不置位强制改密，
// 其余行为不变——密码生效、refresh token 撤销、审计记录实际值而不是硬编码的 true。
func TestAdminResetOwnPasswordKeepsUnforced(t *testing.T) {
	g := newTestDB(t)
	cfg := testConfig()
	r := newAdminUsersRouter(t, g, cfg)
	admin := createUser(t, g, "selfreset@example.com", "selfreset", "password123", true)
	promoteAdmin(t, g, &admin)
	token := accessToken(t, cfg, &admin)
	rt := model.RefreshToken{
		ID:        uuid.New(),
		UserID:    admin.ID,
		TokenHash: "self-reset-refresh-hash",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := g.Create(&rt).Error; err != nil {
		t.Fatalf("写入 refresh token 失败: %v", err)
	}

	w := doAuthJSON(r, http.MethodPost, "/api/admin/users/"+admin.ID.String()+"/password", token, map[string]string{
		"password": "self-set-pass-1",
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("自重置应 204, got %d %s", w.Code, w.Body.String())
	}
	var row model.User
	if err := g.First(&row, "id = ?", admin.ID).Error; err != nil {
		t.Fatalf("读取用户失败: %v", err)
	}
	if row.MustChangePassword {
		t.Fatal("管理员重置自己的密码不应置位 must_change_password")
	}
	if !auth.CheckPassword(row.PasswordHash, "self-set-pass-1") {
		t.Fatal("自重置后的密码应可通过校验")
	}
	var revoked model.RefreshToken
	if err := g.First(&revoked, "id = ?", rt.ID).Error; err != nil {
		t.Fatalf("读取 refresh token 失败: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Fatal("自重置也必须撤销该用户 refresh token")
	}
	var audit model.AdminAuditLog
	if err := g.First(&audit, "action = ? AND target_id = ?", "user.password_reset", admin.ID.String()).Error; err != nil {
		t.Fatalf("自重置必须写审计: %v", err)
	}
	if !strings.Contains(string(audit.AfterSummary), `"mustChangePassword":false`) {
		t.Fatalf("审计必须记录实际值 false: %s", audit.AfterSummary)
	}
}
