package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/identity"
	"github.com/infinite-canvas/server/internal/platform/membership"
	"github.com/infinite-canvas/server/internal/testutil"
)

// newAdminMembershipRouter 按生产同样的中间件链注册会员订阅四条路由与用户列表/详情，
// 供本文件用例验证发放、作废与用户聚合字段的契约。
func newAdminMembershipRouter(t *testing.T, g *gorm.DB, cfg *config.Config) *gin.Engine {
	t.Helper()
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("设置可信代理失败: %v", err)
	}
	secret := []byte(cfg.JWTSecret)
	adminH := NewAdminHandler(g, cfg, testutil.NewFakeStorage("local"))
	admin := r.Group("/api/admin",
		middleware.Auth(secret),
		middleware.RequireActiveUser(identity.NewService(g)),
		middleware.LoadAdminAccess(identity.NewService(g), g),
	)
	admin.GET("/users", middleware.RequirePermission(authz.PermUsersRead), adminH.ListUsers)
	admin.GET("/users/:id", middleware.RequirePermission(authz.PermUsersRead), adminH.GetUser)
	admin.GET("/membership/subscriptions", middleware.RequirePermission(authz.PermMembershipRead), adminH.ListSubscriptions)
	admin.POST("/membership/grant", middleware.RequirePermission(authz.PermMembershipWrite), adminH.GrantSubscription)
	admin.POST("/membership/compensate", middleware.RequirePermission(authz.PermMembershipWrite), adminH.CompensateSubscription)
	admin.DELETE("/membership/subscriptions/:id", middleware.RequirePermission(authz.PermMembershipWrite), adminH.RevokeSubscription)
	return r
}

// errFields 取统一错误响应里的 fields 字段说明。
func errFields(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body struct {
		Error struct {
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析错误响应失败: %v body=%s", err, w.Body.String())
	}
	return body.Error.Fields
}

// insertSubscription 直接写入订阅夹具，created_at 显式指定以便验证列表排序与分页。
func insertSubscription(t *testing.T, g *gorm.DB, userID uuid.UUID, planID, status string, startedAt, periodEnd, createdAt time.Time) model.MembershipSubscription {
	t.Helper()
	sub := model.MembershipSubscription{
		ID:        uuid.New(),
		UserID:    userID,
		PlanID:    planID,
		Status:    status,
		StartedAt: startedAt,
		PeriodEnd: periodEnd,
		SourceRef: "fixture",
		CreatedAt: createdAt,
	}
	if err := g.Create(&sub).Error; err != nil {
		t.Fatalf("写入订阅夹具失败: %v", err)
	}
	return sub
}

func TestAdminListSubscriptionsFiltersAndPaginates(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminMembershipRouter(t, g, cfg)
	token := createAdminToken(t, g, cfg, "lister@example.com", "lister")
	user1 := testutil.CreateUser(t, g, "member1@example.com", "member1", "password123", true)
	user2 := testutil.CreateUser(t, g, "member2@example.com", "member2", "password123", true)

	base := time.Now().Add(-24 * time.Hour)
	subA := insertSubscription(t, g, user1.ID, "paid", "active", base, base.AddDate(0, 0, 30), base)
	insertSubscription(t, g, user1.ID, "paid", "active", base.Add(time.Hour), base.AddDate(0, 0, 60), base.Add(time.Hour))
	subC := insertSubscription(t, g, user1.ID, "paid", "ended", base.Add(-time.Hour), base, base.Add(2*time.Hour))
	subD := insertSubscription(t, g, user2.ID, "paid", "active", base, base.AddDate(0, 0, 30), base.Add(3*time.Hour))

	// 无筛选：按 created_at 倒序，最新在前。
	w := testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/membership/subscriptions", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("订阅列表应 200, got %d %s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	if body["total"] != float64(4) || body["page"] != float64(1) || body["size"] != float64(20) {
		t.Fatalf("分页元信息错误: %s", w.Body.String())
	}
	items := testutil.DecodeItems(t, w)
	if len(items) != 4 || items[0]["id"] != subD.ID.String() {
		t.Fatalf("列表应按 created_at 倒序: %s", w.Body.String())
	}
	first := items[0]
	if first["userId"] != user2.ID.String() || first["userEmail"] != user2.Email || first["userUsername"] != user2.Username {
		t.Fatalf("列表项必须带出用户邮箱与用户名: %s", w.Body.String())
	}
	if first["planId"] != "paid" || first["planName"] != "付费" || first["status"] != "active" {
		t.Fatalf("列表项必须带出档位名与状态: %s", w.Body.String())
	}
	for _, key := range []string{"startedAt", "periodEnd", "createdAt", "sourceRef"} {
		if first[key] == nil || first[key] == "" {
			t.Fatalf("列表项缺少 %s: %s", key, w.Body.String())
		}
	}

	// userId 精确筛选 + 分页：user1 共 3 条，第 2 页剩 1 条（最旧的 subA）。
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/membership/subscriptions?userId="+user1.ID.String()+"&page=2&size=2", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("userId 筛选应 200, got %d %s", w.Code, w.Body.String())
	}
	body = testutil.DecodeBody(t, w)
	items = testutil.DecodeItems(t, w)
	if body["total"] != float64(3) || len(items) != 1 || items[0]["id"] != subA.ID.String() {
		t.Fatalf("userId 筛选分页错误: %s", w.Body.String())
	}

	// planId 精确筛选。
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/membership/subscriptions?planId=paid", token, nil)
	if testutil.DecodeBody(t, w)["total"] != float64(4) {
		t.Fatalf("planId=paid 应命中 4 条: %s", w.Body.String())
	}
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/membership/subscriptions?planId=free", token, nil)
	if testutil.DecodeBody(t, w)["total"] != float64(0) {
		t.Fatalf("planId=free 应命中 0 条: %s", w.Body.String())
	}

	// status 筛选。
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/membership/subscriptions?status=ended", token, nil)
	items = testutil.DecodeItems(t, w)
	if testutil.DecodeBody(t, w)["total"] != float64(1) || items[0]["id"] != subC.ID.String() || items[0]["status"] != "ended" {
		t.Fatalf("status=ended 应只命中已作废行: %s", w.Body.String())
	}
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/membership/subscriptions?status=active", token, nil)
	if testutil.DecodeBody(t, w)["total"] != float64(3) {
		t.Fatalf("status=active 应命中 3 条: %s", w.Body.String())
	}

	// 非法 status 与非法 userId。
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/membership/subscriptions?status=cancelled", token, nil)
	if w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "VALIDATION_FAILED" || errFields(t, w)["status"] == "" {
		t.Fatalf("非法 status 应 400 VALIDATION_FAILED 带字段说明, got %d %s", w.Code, w.Body.String())
	}
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/membership/subscriptions?userId=not-a-uuid", token, nil)
	if w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "VALIDATION_FAILED" || errFields(t, w)["userId"] == "" {
		t.Fatalf("非法 userId 应 400 VALIDATION_FAILED 带字段说明, got %d %s", w.Code, w.Body.String())
	}
}

func TestAdminGrantSubscriptionWritesRowQuotaAndAudit(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminMembershipRouter(t, g, cfg)
	token := createAdminToken(t, g, cfg, "granter@example.com", "granter")
	user := testutil.CreateUser(t, g, "member@example.com", "member", "password123", true)

	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/membership/grant", token, map[string]string{
		"userId": user.ID.String(), "planId": "paid", "reason": "运营活动赠送",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("发放应 200, got %d %s", w.Code, w.Body.String())
	}
	sub, ok := testutil.DecodeBody(t, w)["subscription"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 subscription 对象: %s", w.Body.String())
	}
	if sub["userId"] != user.ID.String() || sub["userEmail"] != user.Email || sub["planId"] != "paid" || sub["planName"] != "付费" {
		t.Fatalf("发放响应形状错误: %s", w.Body.String())
	}
	if sub["status"] != "active" || sub["sourceRef"] != "运营活动赠送" {
		t.Fatalf("发放响应应带 active 状态与 reason 来源: %s", w.Body.String())
	}
	end, err := time.Parse(time.RFC3339Nano, sub["periodEnd"].(string))
	if err != nil {
		t.Fatalf("periodEnd 必须是 RFC3339: %v", err)
	}
	lower, upper := time.Now().AddDate(0, 0, 29), time.Now().AddDate(0, 0, 31)
	if end.Before(lower) || end.After(upper) {
		t.Fatalf("paid 30 天周期错误: %v", end)
	}

	var row model.MembershipSubscription
	if err := g.First(&row, "id = ?", sub["id"]).Error; err != nil {
		t.Fatalf("订阅未写库: %v", err)
	}
	if row.Status != "active" || row.SourceRef != "运营活动赠送" {
		t.Fatalf("库中订阅状态/来源错误: %+v", row)
	}
	// 发放必须同步回写档位存储配额（paid 1GB）。
	var account model.StorageAccount
	if err := g.First(&account, "user_id = ?", user.ID).Error; err != nil {
		t.Fatalf("读取存储账户失败: %v", err)
	}
	if account.QuotaBytes != 1<<30 {
		t.Fatalf("发放后配额应回写为 paid 档位 1GB, got %d", account.QuotaBytes)
	}

	var admin model.PlatformUser
	if err := g.First(&admin, "email = ?", "granter@example.com").Error; err != nil {
		t.Fatalf("读取管理员失败: %v", err)
	}
	var audit model.AdminAuditLog
	if err := g.First(&audit, "action = ?", "membership.grant").Error; err != nil {
		t.Fatalf("发放必须写审计: %v", err)
	}
	if audit.TargetType != "membership_subscription" || audit.TargetID != sub["id"].(string) {
		t.Fatalf("审计目标错误: %+v", audit)
	}
	if audit.ActorUserID != admin.ID || audit.Reason != "运营活动赠送" {
		t.Fatalf("审计应记录操作者与原因: %+v", audit)
	}
	if !strings.Contains(string(audit.AfterSummary), `"planId":"paid"`) {
		t.Fatalf("审计摘要应记录订阅快照: %s", audit.AfterSummary)
	}
}

func TestAdminGrantSubscriptionValidation(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminMembershipRouter(t, g, cfg)
	token := createAdminToken(t, g, cfg, "granter2@example.com", "granter2")
	user := testutil.CreateUser(t, g, "member2@example.com", "member2", "password123", true)

	cases := []struct {
		name   string
		body   map[string]string
		field  string
		expect string
	}{
		{"free 档位不可购买", map[string]string{"userId": user.ID.String(), "planId": "free", "reason": "x"}, "planId", "档位不存在或不可购买"},
		{"档位不存在", map[string]string{"userId": user.ID.String(), "planId": "ghost", "reason": "x"}, "planId", "档位不存在或不可购买"},
		{"用户不存在", map[string]string{"userId": uuid.NewString(), "planId": "paid", "reason": "x"}, "userId", "用户不存在"},
		{"userId 非法", map[string]string{"userId": "abc", "planId": "paid", "reason": "x"}, "userId", "userId 不合法"},
		{"原因缺失", map[string]string{"userId": user.ID.String(), "planId": "paid", "reason": " "}, "reason", "必须填写原因"},
	}
	for _, tc := range cases {
		w := testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/membership/grant", token, tc.body)
		if w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "VALIDATION_FAILED" {
			t.Fatalf("%s 应 400 VALIDATION_FAILED, got %d %s", tc.name, w.Code, w.Body.String())
		}
		if got := errFields(t, w)[tc.field]; got != tc.expect {
			t.Fatalf("%s 的 fields.%s 应为 %q, got %q", tc.name, tc.field, tc.expect, got)
		}
	}

	// 空请求体：三个字段的错误一起返回。
	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/membership/grant", token, map[string]string{})
	fields := errFields(t, w)
	if w.Code != http.StatusBadRequest || fields["userId"] == "" || fields["planId"] == "" || fields["reason"] == "" {
		t.Fatalf("空请求体应 400 并带全部字段说明, got %d %s", w.Code, w.Body.String())
	}

	var count int64
	g.Model(&model.MembershipSubscription{}).Count(&count)
	if count != 0 {
		t.Fatalf("校验失败的请求不应写订阅行, count=%d", count)
	}
}

func TestAdminCompensateSubscriptionAuditsDistinctAction(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminMembershipRouter(t, g, cfg)
	token := createAdminToken(t, g, cfg, "compensator@example.com", "compensator")
	user := testutil.CreateUser(t, g, "member3@example.com", "member3", "password123", true)

	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/membership/compensate", token, map[string]string{
		"userId": user.ID.String(), "planId": "paid", "reason": "故障补偿",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("补偿应 200, got %d %s", w.Code, w.Body.String())
	}
	sub, ok := testutil.DecodeBody(t, w)["subscription"].(map[string]any)
	if !ok || sub["planId"] != "paid" || sub["sourceRef"] != "故障补偿" {
		t.Fatalf("补偿响应形状错误: %s", w.Body.String())
	}
	var audit model.AdminAuditLog
	if err := g.First(&audit, "action = ?", "membership.compensate").Error; err != nil {
		t.Fatalf("补偿必须写审计: %v", err)
	}
	if audit.Reason != "故障补偿" {
		t.Fatalf("补偿审计应记录原因: %+v", audit)
	}
	var grantAudits int64
	g.Model(&model.AdminAuditLog{}).Where("action = ?", "membership.grant").Count(&grantAudits)
	if grantAudits != 0 {
		t.Fatalf("补偿不应写 membership.grant 审计, count=%d", grantAudits)
	}
}

func TestAdminRevokeSubscriptionEndsImmediatelyAndConflicts(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminMembershipRouter(t, g, cfg)
	token := createAdminToken(t, g, cfg, "revoker@example.com", "revoker")
	user := testutil.CreateUser(t, g, "member4@example.com", "member4", "password123", true)

	// 直接用域服务发放，避免与发放接口耦合。
	if err := membership.NewService(g).Compensate(g, user.ID, "paid", "初始发放"); err != nil {
		t.Fatalf("夹具发放失败: %v", err)
	}
	var sub model.MembershipSubscription
	if err := g.Order("period_end DESC").First(&sub, "user_id = ?", user.ID).Error; err != nil {
		t.Fatalf("读取订阅夹具失败: %v", err)
	}

	w := testutil.DoAuthJSON(r, http.MethodDelete, "/api/admin/membership/subscriptions/"+sub.ID.String(), token, map[string]string{
		"reason": "误发回收",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("作废应 200, got %d %s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	if body["id"] != sub.ID.String() || body["status"] != "ended" {
		t.Fatalf("作废响应应为 {id, status:ended}: %s", w.Body.String())
	}

	var row model.MembershipSubscription
	if err := g.First(&row, "id = ?", sub.ID).Error; err != nil {
		t.Fatalf("读取订阅失败: %v", err)
	}
	if row.Status != "ended" {
		t.Fatalf("订阅应置为 ended: %+v", row)
	}
	if row.PeriodEnd.After(time.Now()) {
		t.Fatalf("作废行 period_end 必须压到执行时刻，否则 ActivePlan 会继续派生 paid: %v", row.PeriodEnd)
	}
	// 最简口径验收：作废后 ActivePlan 立即不再派生 paid（当前实现会落入 sunset 宽限）。
	planID, _, err := membership.NewService(g).ActivePlan(context.Background(), user.ID, time.Now())
	if err != nil {
		t.Fatalf("派生档位失败: %v", err)
	}
	if planID == "paid" {
		t.Fatalf("作废后 ActivePlan 不应再派生 paid, got %s", planID)
	}

	var audit model.AdminAuditLog
	if err := g.First(&audit, "action = ?", "membership.revoke").Error; err != nil {
		t.Fatalf("作废必须写审计: %v", err)
	}
	if audit.TargetID != sub.ID.String() || audit.Reason != "误发回收" {
		t.Fatalf("作废审计目标/原因错误: %+v", audit)
	}
	if !strings.Contains(string(audit.AfterSummary), `"status":"ended"`) {
		t.Fatalf("作废审计摘要应记录 ended: %s", audit.AfterSummary)
	}

	// 已作废再作废 → 409。
	w = testutil.DoAuthJSON(r, http.MethodDelete, "/api/admin/membership/subscriptions/"+sub.ID.String(), token, map[string]string{
		"reason": "再作废",
	})
	if w.Code != http.StatusConflict || testutil.ErrorCode(t, w) != "VALIDATION_FAILED" {
		t.Fatalf("重复作废应 409, got %d %s", w.Code, w.Body.String())
	}

	// 不存在的订阅 → 404；缺 reason → 400。
	w = testutil.DoAuthJSON(r, http.MethodDelete, "/api/admin/membership/subscriptions/"+uuid.NewString(), token, map[string]string{"reason": "x"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("作废不存在的订阅应 404, got %d", w.Code)
	}
	w = testutil.DoAuthJSON(r, http.MethodDelete, "/api/admin/membership/subscriptions/"+sub.ID.String(), token, map[string]string{"reason": " "})
	if w.Code != http.StatusBadRequest || errFields(t, w)["reason"] == "" {
		t.Fatalf("缺少原因应 400 带字段说明, got %d %s", w.Code, w.Body.String())
	}
}

func TestAdminUserResponsesCarryMembershipStorageProducts(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newAdminMembershipRouter(t, g, cfg)
	token := createAdminToken(t, g, cfg, "fieldchecker@example.com", "fieldchecker")
	user := testutil.CreateUser(t, g, "fielduser@example.com", "fielduser", "password123", true)

	w := testutil.DoAuthJSON(r, http.MethodPost, "/api/admin/membership/grant", token, map[string]string{
		"userId": user.ID.String(), "planId": "paid", "reason": "字段验收",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("发放失败: %d %s", w.Code, w.Body.String())
	}

	// 列表行：新聚合键与旧键并存。
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/users?q=fielduser", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("用户列表失败: %d %s", w.Code, w.Body.String())
	}
	items := testutil.DecodeItems(t, w)
	if len(items) != 1 {
		t.Fatalf("用户筛选应命中 1 条: %s", w.Body.String())
	}
	item := items[0]
	if item["planId"] != "paid" || item["paidUntil"] == nil || item["storageBytes"] == nil {
		t.Fatalf("旧键 planId/paidUntil/storageBytes 必须保留: %s", w.Body.String())
	}
	mem, ok := item["membership"].(map[string]any)
	if !ok {
		t.Fatalf("列表行缺少 membership 对象: %s", w.Body.String())
	}
	if mem["planId"] != "paid" || mem["periodEnd"] == nil || mem["graceEndsAt"] != nil {
		t.Fatalf("列表行 membership 聚合错误: %s", w.Body.String())
	}
	stor, ok := item["storage"].(map[string]any)
	if !ok {
		t.Fatalf("列表行缺少 storage 对象: %s", w.Body.String())
	}
	if stor["usedBytes"] != float64(0) || stor["quotaBytes"] != float64(1<<30) {
		t.Fatalf("列表行 storage 聚合错误: %s", w.Body.String())
	}
	products, ok := item["products"].([]any)
	if !ok || len(products) != 1 || products[0] != model.ProductCanvas {
		t.Fatalf("列表行 products 应为 [youc-canvas]: %s", w.Body.String())
	}

	// 详情：同样的三个字段，旧键（含 planName/storageLimit）保留。
	w = testutil.DoAuthJSON(r, http.MethodGet, "/api/admin/users/"+user.ID.String(), token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("用户详情失败: %d %s", w.Code, w.Body.String())
	}
	detail, ok := testutil.DecodeBody(t, w)["user"].(map[string]any)
	if !ok {
		t.Fatalf("详情缺少 user 对象: %s", w.Body.String())
	}
	if detail["planId"] != "paid" || detail["planName"] != "付费" || detail["paidUntil"] == nil {
		t.Fatalf("详情旧键必须保留: %s", w.Body.String())
	}
	mem, ok = detail["membership"].(map[string]any)
	if !ok || mem["planId"] != "paid" || mem["periodEnd"] == nil || mem["graceEndsAt"] != nil {
		t.Fatalf("详情 membership 聚合错误: %s", w.Body.String())
	}
	stor, ok = detail["storage"].(map[string]any)
	if !ok || stor["usedBytes"] != float64(0) || stor["quotaBytes"] != float64(1<<30) {
		t.Fatalf("详情 storage 聚合错误: %s", w.Body.String())
	}
	products, ok = detail["products"].([]any)
	if !ok || len(products) != 1 || products[0] != model.ProductCanvas {
		t.Fatalf("详情 products 应为 [youc-canvas]: %s", w.Body.String())
	}
}
