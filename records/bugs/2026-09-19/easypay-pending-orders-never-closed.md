---
bug_id: BUG-20260919-PAYMENT-001
title: 易支付渠道未支付订单超时后永不关单，超时扫描查单无限堆积
status: verified
severity: P2
error_kind: logic-error
attribution: product_code
provenance: external
project: infinite-canvas
files:
  - server/internal/service/orders.go
  - server/internal/payment/easypay.go
  - server/internal/service/orders_test.go
affected_slices:
  - server/internal/service/orders.go:ExpirePendingOrders
  - server/internal/payment/easypay.go:QueryOrder 未知状态归一
source_revision: infinite-canvas@a43ddaf
fix_revision: infinite-canvas@68d6b97
reviewer: independent-reviewer
review_decision: approved
regression_status: pass
closure_criteria: server 模块 go test ./internal/service/... ./internal/billing/... -count=1 两包 ok，含 TestExpirePendingOrdersClosesUnpaid 与 TestExpirePendingOrdersQueryErrorKeepsPending；基线 a43ddaf 上 revert 绑定实验编译失败证明用例绑定新行为
recorded_at: 2026-09-19T00:00:00Z
updated_at: 2026-09-19T00:00:00Z
---

本记录是支付订单生命周期三项修复之一（同一诊断会话发现，同一修复 commit）。修复 commit `68d6b97`（基线 `a43ddaf`，分支 fix-payment-order-lifecycle），一次改动 7 个文件：server/internal/service/orders.go、server/internal/service/orders_test.go、server/internal/billing/order.go、server/internal/billing/billing_test.go、server/cmd/server/main.go、CHANGELOG.md、docs/content/docs/progress/pending-test.mdx。关联记录：BUG-20260919-PAYMENT-002（丢钱告警）、BUG-20260919-PAYMENT-003（重复下单防误付）。

## 现象

- 渠道为易支付（彩虹协议）时，用户放弃支付（关闭收银台页面、不完成付款）的订单永远停留在 pending。服务端每 5 分钟一轮的超时扫描（server/cmd/server/main.go 中「订单超时扫描」定时任务）对同一批超时订单反复向渠道发起查单，状态永不收敛、查单请求无限堆积。
- 原扫描查询每轮最多取 200 条且不带排序：当积压超过 200 条时，数据库返回顺序不保证，后积压的订单可能长期轮不到查单（饥饿）。
- 涉及边界值（均为既有约定，本次未改）：扫描间隔 5 分钟、判定窗口 30 分钟、单轮上限 200 条。
- 定级 P2：功能性缺陷与查单资源浪费，无直接资损（订单本就未支付）。

## 根因

三个因素叠加，形成「未支付订单 → 渠道查单归一为 pending → 扫描分支空操作 → 本地状态不变 → 下一轮再查单」的死循环：

1. 协议事实：彩虹易支付的查单接口对未支付订单不返回 TRADE_CLOSED。server/internal/payment/easypay.go 的 QueryOrder 把未知/未支付状态统一归一为 "pending"（本次修复未触碰 payment 渠道文件，下列片段即基线内容，同样存在于当前工作区）：

   ```go
   // server/internal/payment/easypay.go:186-191（未改动；即 infinite-canvas@a43ddaf:server/internal/payment/easypay.go）
   	case tradeStatus == "TRADE_CLOSED" || status == "TRADE_CLOSED":
   		return CallbackResult{OutTradeNo: order.ID.String(), Status: "closed"}, nil
   	default:
   		// 状态未知时保持待支付，由调用方决定后续，不误杀。
   		return CallbackResult{OutTradeNo: order.ID.String(), Status: "pending"}, nil
   	}
   ```

2. server/internal/service/orders.go 的 ExpirePendingOrders 原实现只在渠道明确返回 closed 时关单；paid 之外的一切状态（含 pending/未知）落入 default 空操作分支（注释原话「渠道侧仍然可支付，本地不判失败」）。对易支付的未支付单，渠道永远不会报 closed，default 分支永远命中。
3. 查询只带 Limit(200)，无 Order 排序。

## 问题代码

基线 a43ddaf 的原实现（片段核对自本次审查的原始 diff；主工作区另一分支副本在基线之后仅推进 main-site 首页样式与 blog 角色常量两个提交，均未触及支付域，其工作副本内容与片段一致）。地址：infinite-canvas@a43ddaf:server/internal/service/orders.go（ExpirePendingOrders，原 :300-341）。

查询无排序（原 :300-306）：

```go
func (s *OrderService) ExpirePendingOrders(ctx context.Context, now time.Time, timeout time.Duration) (int, error) {
	var orders []model.Order
	if err := s.db.WithContext(ctx).
		Where("status = ? AND created_at < ?", "pending", now.Add(-timeout)).
		Limit(200).Find(&orders).Error; err != nil {
		return 0, err
	}
```

状态分支：仅 closed 关单，pending/未知状态空操作（原 :319-337）：

```go
		switch result.Status {
		case "paid":
			// 与回调同口径：无条件比对金额，0 或解析失败不再短路（差异清单 #7）。
			if result.PriceMicros != order.PriceMicros {
				slog.Error("主动查询金额与本地订单不一致，拒绝到账", "order", order.ID)
				continue
			}
			if err := s.markPaid(ctx, order, result.ProviderOrderID); err != nil {
				slog.Error("超时订单补到账失败", "order", order.ID, "err", err)
			}
		case "closed":
			if err := s.markFailed(ctx, order); err != nil {
				slog.Error("置失败订单失败", "order", order.ID, "err", err)
			} else {
				expired++
			}
		default:
			// 渠道侧仍然可支付，本地不判失败。
		}
```

## 修复

- default 分支由空操作改为关单：超时（30 分钟）且渠道查单确认未支付（pending 或未知状态）时 markFailed；渠道报 paid 仍按回调同口径补到账；渠道查单报错仍保留 pending（Warn 日志，等下一轮扫描重试）。
- 查询补 Order("created_at ASC")，保证积压按创建时间从早到晚逐轮收敛。

修复后代码（本仓库工作区，即修复 commit 68d6b97 的内容）：

```go
// server/internal/service/orders.go:325-334
// ExpirePendingOrders 把超过 timeout 未支付的订单关单（置为 failed）。
// 置失败前必须向渠道主动查询一次真实状态：渠道报 paid 时按回调同口径补到账；
// 报 closed 或渠道侧仍未支付（pending/未知状态）说明支付窗口已过，直接置为 failed，
// 防止旧支付链接被误付；渠道查询出错时保留 pending，等下一轮扫描重试。
func (s *OrderService) ExpirePendingOrders(ctx context.Context, now time.Time, timeout time.Duration) (int, error) {
	var orders []model.Order
	if err := s.db.WithContext(ctx).
		Where("status = ? AND created_at < ?", "pending", now.Add(-timeout)).
		Order("created_at ASC").Limit(200).Find(&orders).Error; err != nil {
		return 0, err
	}
```

```go
// server/internal/service/orders.go:364-371
		default:
			// 渠道侧仍未支付（pending/未知状态）：支付窗口已过，关单防止旧支付链接被误付。
			if err := s.markFailed(ctx, order); err != nil {
				slog.Error("置失败订单失败", "order", order.ID, "err", err)
			} else {
				expired++
			}
		}
```

- 明确保持不动：webhook 路径 HandleCallback 的 `default: return nil`（server/internal/service/orders.go:268-270）——webhook 报 pending 时必须保留订单等待用户支付，与扫描路径语义不同；金额守卫、CancelOrder、payment 三渠道文件与 model 均未改动。

## 审查

- 独立 reviewer（监督者安排的第二遍独立审查，非实现者）裁定 approved；technical-director 先行定稿五项设计红线（HandleCallback default 不动、stale 恒返 nil 不自动补账、金额守卫/CancelOrder 不动、payment 三渠道文件与 model 不动、边界值 30min/5min/200 不动），经 474 行原始 diff（7 文件，295 增 15 删；会话临时产物，未随库归档）逐 hunk 核实无违反。
- 回归证据（server/ 模块下真实执行）：`go build ./...` 无输出退出 0；`go test ./internal/service/... ./internal/billing/... -count=1` 两包 ok，原始输出：

  ```text
  ok  	github.com/infinite-canvas/server/internal/service	0.627s
  ok  	github.com/infinite-canvas/server/internal/billing	0.622s
  ```

- 本缺陷绑定用例（server/internal/service/orders_test.go）：TestExpirePendingOrdersClosesUnpaid（:229，渠道报 pending 与未知状态的两笔超时单均置 failed、返回 expired==2）、TestExpirePendingOrdersQueryErrorKeepsPending（:264，渠道查询报错时订单保持 pending）。
- revert 绑定实验：将三个核心文件 stash 回基线后跑新用例，编译失败证明测试绑定新行为（原始输出存于会话临时目录 `/tmp/payfix-review/revert-fail.log`，临时产物可能已清理，以下为当时捕获的原文）：

  ```text
  # github.com/infinite-canvas/server/internal/service [github.com/infinite-canvas/server/internal/service.test]
  internal/service/orders_test.go:308:9: orders.OnPaidOrderClosed undefined (type *OrderService has no field or method OnPaidOrderClosed)
  internal/service/orders_test.go:326:9: orders.OnPaidOrderClosed undefined (type *OrderService has no field or method OnPaidOrderClosed)
  internal/service/orders_test.go:359:9: orders.OnPaidOrderClosed undefined (type *OrderService has no field or method OnPaidOrderClosed)
  FAIL	github.com/infinite-canvas/server/internal/service [build failed]
  FAIL
  ```

  pop 恢复后复跑 ok（同目录 `revert-ok.log`）：

  ```text
  ok  	github.com/infinite-canvas/server/internal/service	0.254s
  ```

- 残余与已知边界：Order("created_at ASC") 排序行为无专门断言（靠人工核对 diff 保证）；易支付真实商户端到端链路仍未配置，本项人工验收登记在 docs/content/docs/progress/pending-test.mdx「支付订单生命周期修复」一节的超时关单条目。
