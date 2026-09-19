---
bug_id: BUG-20260919-PAYMENT-002
title: 渠道对已终态订单重放真实付款回调时款项静默不入账且无告警
status: verified
severity: P1
error_kind: logic-error
attribution: product_code
provenance: external
project: infinite-canvas
files:
  - server/internal/service/orders.go
  - server/internal/billing/order.go
  - server/cmd/server/main.go
  - server/internal/service/orders_test.go
  - server/internal/billing/billing_test.go
affected_slices:
  - server/internal/service/orders.go:markPaid 终态分支
  - server/internal/billing/order.go:NewOrderHandler/NewPaymentHandler 构造器
  - server/cmd/server/main.go:OnPaidOrderClosed 告警回调装配
source_revision: infinite-canvas@a43ddaf
fix_revision: infinite-canvas@68d6b97
reviewer: independent-reviewer
review_decision: approved
regression_status: pass
closure_criteria: server 模块 go test ./internal/service/... ./internal/billing/... -count=1 两包 ok，含 TestMarkPaidStaleTriggersAlertCallback（含 nil 回调不 panic）与 TestMarkPaidIdempotentDoesNotTriggerCallback；revert 绑定实验编译失败证明用例绑定新行为
recorded_at: 2026-09-19T00:00:00Z
updated_at: 2026-09-19T00:00:00Z
---

本记录是支付订单生命周期三项修复之一（同一诊断会话发现，同一修复 commit）。修复 commit `68d6b97`（基线 `a43ddaf`，分支 fix-payment-order-lifecycle），一次改动 7 个文件：server/internal/service/orders.go、server/internal/service/orders_test.go、server/internal/billing/order.go、server/internal/billing/billing_test.go、server/cmd/server/main.go、CHANGELOG.md、docs/content/docs/progress/pending-test.mdx。关联记录：BUG-20260919-PAYMENT-001（超时关单）、BUG-20260919-PAYMENT-003（重复下单防误付）。

## 现象

- 订单已处于终态（failed——用户主动取消或超时扫描关闭；refunded——已退款）之后，用户仍通过旧收银台页面完成真实付款，渠道向 webhook 重放 paid 回调。markPaid 对非 pending/paid 的锁定行静默 return nil：用户钱已付出，点数/会员不入账，服务端无 error 日志、无任何告警，通常到用户投诉才可能被发现。
- 结构性放大：OrderService 在三处各自构造——server/cmd/server/main.go 的定时扫描一处，billing 域的下单 handler 与 webhook handler 各自 NewOrderService 一处。webhook 回调走的是 handler 自建实例：即使只给某一个实例挂丢钱告警回调，webhook 路径也必然收不到，横切行为没有统一挂载点。
- 定级 P1：真实资金已收且无自动恢复路径；因触发需要「终态订单 + 后续真实付款」两个条件叠加成窗口，未定 P0。

## 根因

- markPaid 事务内的终态分支只表达了「不入账」这半边正确语义（防重复到账、防已取消单被扫到账），漏掉了「渠道确已收款、必须人工介入」的半边：既无观测（日志）也无动作（通知）。
- 缺少共享单例机制：NewOrderService 被三处分散调用，任何「挂在实例上的横切回调」都会漏路径。

## 问题代码

基线 a43ddaf 的原实现（片段核对自本次审查的原始 diff；核对方式同 BUG-20260919-PAYMENT-001 的说明）。地址：infinite-canvas@a43ddaf:server/internal/service/orders.go（markPaid，原 :262-274）。

终态分支静默返回：

```go
func (s *OrderService) markPaid(ctx context.Context, order *model.Order, providerOrderID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "id = ?", order.ID).Error; err != nil {
			return err
		}
		if locked.Status == "paid" {
			return nil
		}
		if locked.Status != "pending" {
			// 已取消或已失败的订单不再到账。
			return nil
		}
```

地址：infinite-canvas@a43ddaf:server/internal/billing/order.go（原 :26-28 与 :200-202）。webhook 与下单 handler 各自新建 OrderService 实例：

```go
func NewOrderHandler(db *gorm.DB, registry *service.PaymentRegistry) *OrderHandler {
	return &OrderHandler{db: db, orders: service.NewOrderService(db, registry), registry: registry}
}
```

```go
func NewPaymentHandler(db *gorm.DB, registry *service.PaymentRegistry) *PaymentHandler {
	return &PaymentHandler{registry: registry, orders: service.NewOrderService(db, registry)}
}
```

## 修复

行为红线（经审查确认未变）：终态订单收到 paid 回调时外部行为与原先完全一致——绝不入账、webhook 仍按渠道要求返回 success 终止渠道重试、不做自动补账；金额守卫与 CancelOrder 未动。新增的是观测与人工介入通道：

- markPaid 终态分支记 error 日志，把锁定行快照带出事务，事务提交后触发 OnPaidOrderClosed 回调（server/internal/service/orders.go:279-317）：

  ```go
  func (s *OrderService) markPaid(ctx context.Context, order *model.Order, providerOrderID string) error {
  	var stale *model.Order
  	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
  		var locked model.Order
  		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "id = ?", order.ID).Error; err != nil {
  			return err
  		}
  		if locked.Status == "paid" {
  			return nil
  		}
  		if locked.Status != "pending" {
  			// 已取消或已失败的订单不再到账；把 stale 快照带出事务，提交后触发告警回调。
  			slog.Error("渠道报支付成功但订单已是终态，款项未入账，需人工核对补账",
  				"order", locked.ID, "userID", locked.UserID, "status", locked.Status,
  				"priceMicros", locked.PriceMicros, "providerOrderID", providerOrderID)
  			snapshot := locked
  			stale = &snapshot
  			return nil
  		}
  		now := time.Now()
  		updates := map[string]any{"status": "paid", "paid_at": now}
  		if providerOrderID != "" {
  			updates["provider_order_id"] = providerOrderID
  		}
  		if err := tx.Model(&model.Order{}).Where("id = ?", locked.ID).Updates(updates).Error; err != nil {
  			return err
  		}
  		locked.Status = "paid"
  		locked.PaidAt = &now
  		if err := s.billing.Purchase(tx, &locked, now); err != nil {
  			return err
  		}
  		return s.membership.GrantFromOrder(tx, &locked, now)
  	})
  	if err == nil && stale != nil && s.OnPaidOrderClosed != nil {
  		s.OnPaidOrderClosed(stale, providerOrderID)
  	}
  	return err
  }
  ```

- server/cmd/server/main.go 把三处分散的 NewOrderService 收敛为共享单例，并注入 OnPaidOrderClosed 闭包：调 notifyAdmins 给全部 admin 角色邮箱发告警邮件（含订单号、用户 ID、金额微元、渠道、渠道单号、订单当前状态，提示到管理端手工调点数补账）（:198-212）：

  ```go
  	// 共享订单服务单例：下单、支付回调与超时扫描复用同一实例，丢钱告警回调只挂一处。
  	// 渠道报 paid 但订单已是终态（failed/refunded）时款项未入账，邮件告警全部 admin，
  	// 由管理员到管理端手工调点数补账（不做自动补账）。
  	orderService := service.NewOrderService(gormDB, paymentRegistry)
  	orderService.OnPaidOrderClosed = func(order *model.Order, providerOrderID string) {
  		if providerOrderID == "" && order.ProviderOrderID != nil {
  			providerOrderID = *order.ProviderOrderID
  		}
  		notifyAdmins(gormDB, mailer, "支付异常告警：渠道报已支付但订单已关闭，款项未入账",
  			fmt.Sprintf("渠道报订单 %s 支付成功，但该订单本地状态已是 %s，款项未入账，可能丢钱。\n用户 ID：%s\n金额：%d 微元\n渠道：%s\n渠道单号：%s\n订单号：%s\n请到管理端核对该笔支付流水，并手工调点数补账。\n",
  				order.ID, order.Status, order.UserID, order.PriceMicros, order.Provider, providerOrderID, order.ID))
  	}
  	creditHandler := billing.NewCreditHandler(gormDB, paymentRegistry)
  	orderHandler := billing.NewOrderHandler(orderService)
  	paymentHandler := billing.NewPaymentHandler(orderService)
  ```

- billing.NewOrderHandler / NewPaymentHandler 构造器改为直接接收 *service.OrderService，复用共享单例（server/internal/billing/order.go:26-29 与 :196-199）：

  ```go
  // NewOrderHandler 复用共享的 OrderService 单例：下单、回调与超时扫描走同一实例，告警回调只挂一处。
  func NewOrderHandler(orders *service.OrderService) *OrderHandler {
  	return &OrderHandler{db: orders.DB(), orders: orders, registry: orders.Registry()}
  }
  ```

- notifyAdmins 本体（server/cmd/server/main.go:554-568）沿用工单/对账既有的管理员告警通道：查询全部 admin 角色邮箱逐个投递，无管理员或发送失败不影响主流程。
- OrderService 为此新增 DB() / Registry() 取依赖入口（server/internal/service/orders.go:47-49），供 billing handler 复用单例。

## 审查

- 独立 reviewer（监督者安排的第二遍独立审查，非实现者）裁定 approved；technical-director 五项设计红线（HandleCallback default 不动、stale 恒返 nil 不自动补账、金额守卫/CancelOrder 不动、payment 三渠道文件与 model 不动、边界值 30min/5min/200 不动）经 474 行原始 diff 逐 hunk 核实无违反。
- 回归证据（server/ 模块下真实执行）：`go build ./...` 无输出退出 0；`go test ./internal/service/... ./internal/billing/... -count=1` 两包 ok：

  ```text
  ok  	github.com/infinite-canvas/server/internal/service	0.627s
  ok  	github.com/infinite-canvas/server/internal/billing	0.622s
  ```

- 本缺陷绑定用例（server/internal/service/orders_test.go）：
  - TestMarkPaidStaleTriggersAlertCallback（:292）：failed 订单收到 paid 回调返回成功、状态保持 failed、告警回调恰触发一次且参数（订单快照/渠道单号）正确；refunded 订单同样不入账；回调为 nil 时不 panic。
  - TestMarkPaidIdempotentDoesNotTriggerCallback（:345）：paid 到 paid 的重复回调幂等，不触发告警（只有 stale 分支触发）。
  - billing_test.go（:53-55）改为单例装配后覆盖 webhook 与下单 handler 共用同一 OrderService。
- revert 绑定实验：三个核心文件 stash 回基线后跑新用例，编译失败恰好落在本缺陷新增的回调 API 上，证明测试绑定新行为（原始输出存于会话临时目录 `/tmp/payfix-review/revert-fail.log`，可能已清理，以下为当时捕获的原文；pop 恢复后复跑 ok）：

  ```text
  # github.com/infinite-canvas/server/internal/service [github.com/infinite-canvas/server/internal/service.test]
  internal/service/orders_test.go:308:9: orders.OnPaidOrderClosed undefined (type *OrderService has no field or method OnPaidOrderClosed)
  internal/service/orders_test.go:326:9: orders.OnPaidOrderClosed undefined (type *OrderService has no field or method OnPaidOrderClosed)
  internal/service/orders_test.go:359:9: orders.OnPaidOrderClosed undefined (type *OrderService has no field or method OnPaidOrderClosed)
  FAIL	github.com/infinite-canvas/server/internal/service [build failed]
  FAIL
  ```

- 残余风险（留档）：
  - 告警无去重：stale 订单每次被渠道重放 paid 回调都会再触发一封告警邮件。
  - stale 订单收到 paid 回调后 webhook 返回 success 的行为无 HTTP 层用例（仅服务层断言返回 nil error）。
  - 丢钱告警的人工验收场景（配置邮件通道后构造「下单 → 取消订单 → 渠道重放 paid 回调」，确认管理员收到邮件且订单状态与余额不变）登记在 docs/content/docs/progress/pending-test.mdx「支付订单生命周期修复」一节，待验收。
- 影响关联：BUG-20260919-PAYMENT-001 的超时关单与 BUG-20260919-PAYMENT-003 的自动关旧单都会把更多订单置为终态，使「终态订单收到真实付款」窗口内的订单量上升；本告警是这些终态单收到真实付款时的统一兜底通道。
