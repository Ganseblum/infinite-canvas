---
bug_id: BUG-20260919-PAYMENT-003
title: 重复下单不取消旧待支付单，旧支付链接可被误付
status: verified
severity: P2
error_kind: logic-error
attribution: product_code
provenance: external
project: infinite-canvas
files:
  - server/internal/service/orders.go
  - server/internal/service/orders_test.go
affected_slices:
  - server/internal/service/orders.go:CreateOrder
source_revision: infinite-canvas@a43ddaf
fix_revision: infinite-canvas@68d6b97
reviewer: independent-reviewer
review_decision: approved
regression_status: pass
closure_criteria: server 模块 go test ./internal/service/... ./internal/billing/... -count=1 两包 ok，含 TestCreateOrderCancelsOtherPendingOrders 与 TestCreateOrderPaymentFailureKeepsOldPending；revert 绑定实验编译失败证明用例绑定新行为
recorded_at: 2026-09-19T00:00:00Z
updated_at: 2026-09-19T00:00:00Z
---

本记录是支付订单生命周期三项修复之一（同一诊断会话发现，同一修复 commit）。修复 commit `68d6b97`（基线 `a43ddaf`，分支 fix-payment-order-lifecycle），一次改动 7 个文件：server/internal/service/orders.go、server/internal/service/orders_test.go、server/internal/billing/order.go、server/internal/billing/billing_test.go、server/cmd/server/main.go、CHANGELOG.md、docs/content/docs/progress/pending-test.mdx。关联记录：BUG-20260919-PAYMENT-001（超时关单）、BUG-20260919-PAYMENT-002（丢钱告警）。

## 现象

- 每次点击「购买」都新建订单（新 uuid、新收银台支付 URL），旧 pending 单不取消、不复用，其收银台链接长期有效。用户重复下单后误用旧链接付款：资金按旧单到账，新单沦为孤儿 pending；用户为同一购买意图可能支付两笔。
- 与关联缺陷叠加：基线上旧 pending 单永不关单（BUG-20260919-PAYMENT-001），误付窗口无限长；若旧单已被取消/关闭为终态，误付还会落入静默丢钱路径（BUG-20260919-PAYMENT-002），修复前无任何告警。
- 定级 P2：重复扣款风险与订单状态混乱，用户的旧单付款在旧单仍为 pending 时本可正常到账，且修复后误付终态单有告警与人工补账通道兜底。

## 根因

CreateOrder 只负责创建新单，从不检查或处置同用户已有的 pending 订单：旧单既不取消也不复用，旧支付链接始终可支付。

## 问题代码

基线 a43ddaf 的 CreateOrder 尾部（地址：infinite-canvas@a43ddaf:server/internal/service/orders.go，原 :68-83；片段核对自本次审查的原始 diff，核对方式同 BUG-20260919-PAYMENT-001 的说明）——函数到此直接返回，对同用户已有 pending 单不做任何处理：

```go
	params, providerOrderID, err := provider.CreatePayment(ctx, order)
	if err != nil {
		// 下单失败时把订单置为 failed，避免留下永远无法支付的垃圾待支付订单。
		_ = s.db.WithContext(ctx).Model(&model.Order{}).Where("id = ?", order.ID).
			Update("status", "failed").Error
		return nil, err
	}
	if providerOrderID != "" {
		order.ProviderOrderID = &providerOrderID
		if err := s.db.WithContext(ctx).Model(&model.Order{}).Where("id = ?", order.ID).
			Update("provider_order_id", providerOrderID).Error; err != nil {
			slog.Error("写入渠道单号失败", "order", order.ID, "err", err)
		}
	}
	return &CreateOrderResult{Order: order, Payment: params}, nil
```

## 修复

- 新单支付参数生成成功后，best-effort 关闭同用户其它 pending 单；关闭失败仅记 Warn 日志，不影响新单返回（server/internal/service/orders.go:90-96，修复后工作区内容）：

  ```go
  	// 新单已可支付，best-effort 关闭同用户其它待支付旧单，防止旧支付链接被误付；
  	// 关闭失败只记日志，不影响新单返回。
  	if err := s.db.WithContext(ctx).Model(&model.Order{}).
  		Where("user_id = ? AND status = ? AND id <> ?", user.ID, "pending", order.ID).
  		Update("status", "failed").Error; err != nil {
  		slog.Warn("关闭同用户旧待支付订单失败", "userID", user.ID, "newOrder", order.ID, "err", err)
  	}
  	return &CreateOrderResult{Order: order, Payment: params}, nil
  ```

- 关单条件为 WHERE user_id = ? AND status = 'pending' AND id <> 新单：同用户 paid/refunded 等非 pending 单与其它用户的订单不受影响。
- 渠道下单失败路径明确不取消旧单：新单自身置 failed（原有逻辑），旧 pending 单保留，用户仍可支付旧链接。

## 审查

- 独立 reviewer（监督者安排的第二遍独立审查，非实现者）裁定 approved；technical-director 五项设计红线（HandleCallback default 不动、stale 恒返 nil 不自动补账、金额守卫/CancelOrder 不动、payment 三渠道文件与 model 不动、边界值 30min/5min/200 不动）经 474 行原始 diff 逐 hunk 核实无违反。
- 回归证据（server/ 模块下真实执行）：`go build ./...` 无输出退出 0；`go test ./internal/service/... ./internal/billing/... -count=1` 两包 ok：

  ```text
  ok  	github.com/infinite-canvas/server/internal/service	0.627s
  ok  	github.com/infinite-canvas/server/internal/billing	0.622s
  ```

- 本缺陷绑定用例（server/internal/service/orders_test.go）：
  - TestCreateOrderCancelsOtherPendingOrders（:374）：下单成功后同用户旧 pending 单置 failed，其它用户的 pending 单与新单本身不受影响。
  - TestCreateOrderPaymentFailureKeepsOldPending（:417）：渠道下单失败时新单置 failed、旧 pending 单保留不取消。
- revert 绑定实验：三个核心文件 stash 回基线后跑新用例，编译失败证明测试绑定新行为（原始输出存于会话临时目录 `/tmp/payfix-review/revert-fail.log`，可能已清理，以下为当时捕获的原文；pop 恢复后复跑 ok）：

  ```text
  # github.com/infinite-canvas/server/internal/service [github.com/infinite-canvas/server/internal/service.test]
  internal/service/orders_test.go:308:9: orders.OnPaidOrderClosed undefined (type *OrderService has no field or method OnPaidOrderClosed)
  internal/service/orders_test.go:326:9: orders.OnPaidOrderClosed undefined (type *OrderService has no field or method OnPaidOrderClosed)
  internal/service/orders_test.go:359:9: orders.OnPaidOrderClosed undefined (type *OrderService has no field or method OnPaidOrderClosed)
  FAIL	github.com/infinite-canvas/server/internal/service [build failed]
  FAIL
  ```

- 残余与已知边界：
  - 「同用户 paid/refunded 单不被自动关单」由 WHERE 条件的 status = 'pending' 保证，无显式用例（人工核对 diff）。
  - 误付一旦发生且旧单已被关单（终态），恢复路径走 BUG-20260919-PAYMENT-002 的管理员告警邮件 + 管理端手工调点补账，不做自动退款或自动补账。
  - 人工验收项登记在 docs/content/docs/progress/pending-test.mdx「支付订单生命周期修复」一节的「下单自动取消旧待支付单」条目。
