package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/payment"
	"github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/platform/membership"
)

// ErrOrderAlreadyPaid 表示取消一个已支付的订单，调用方映射为 409 ORDER_ALREADY_PAID。
var ErrOrderAlreadyPaid = errors.New("订单已支付")

// ErrAccountPendingDeletion 表示注销冷静期内调用下单，调用方映射为 403。
var ErrAccountPendingDeletion = errors.New("账号处于注销冷静期")

// OrderService 负责订单创建、列表、取消与支付到账。
// 渠道差异全部收敛在 internal/payment，本层只认统一后的结构。
type OrderService struct {
	db         *gorm.DB
	registry   *PaymentRegistry
	billing    *billing.Service
	membership *membership.Service
	// OnPaidOrderClosed 在渠道报支付成功但订单已是终态（failed/refunded）时触发，
	// 用于丢钱告警：订单不再入账，需人工核对补账。回调在 markPaid 事务提交后触发，
	// 参数是加锁后读到的订单行快照（Status 为原始终态）与回调携带的渠道单号；为 nil 时不触发。
	OnPaidOrderClosed func(order *model.Order, providerOrderID string)
}

func NewOrderService(db *gorm.DB, registry *PaymentRegistry) *OrderService {
	return &OrderService{
		db:         db,
		registry:   registry,
		billing:    billing.NewService(db, model.ProductCanvas),
		membership: membership.NewService(db),
	}
}

// DB 与 Registry 供 billing 域 handler 复用同一个 OrderService 单例时取回依赖。
func (s *OrderService) DB() *gorm.DB               { return s.db }
func (s *OrderService) Registry() *PaymentRegistry { return s.registry }

// CreateOrderResult 是下单成功后的返回结构。
type CreateOrderResult struct {
	Order   *model.Order   `json:"order"`
	Payment payment.Params `json:"payment"`
}

// CreateOrder 建单并生成支付参数。价格与权益天数在建单时从档位快照到订单行。
// packageID 与 planID 二选一：点数包走 credit_packages，会员订单走 membership_plans。
func (s *OrderService) CreateOrder(ctx context.Context, user model.PlatformUser, packageID, planID, providerName string) (*CreateOrderResult, error) {
	if user.Status == "pending_deletion" {
		return nil, ErrAccountPendingDeletion
	}
	provider, ok := s.registry.Get(providerName)
	if !ok {
		return nil, ErrProviderUnavailable
	}
	order, err := s.buildOrder(ctx, user, packageID, planID)
	if err != nil {
		return nil, err
	}
	order.Provider = provider.Name()
	if err := s.db.WithContext(ctx).Create(order).Error; err != nil {
		return nil, err
	}

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
	// 新单已可支付，best-effort 关闭同用户其它待支付旧单，防止旧支付链接被误付；
	// 关闭失败只记日志，不影响新单返回。
	if err := s.db.WithContext(ctx).Model(&model.Order{}).
		Where("user_id = ? AND status = ? AND id <> ?", user.ID, "pending", order.ID).
		Update("status", "failed").Error; err != nil {
		slog.Warn("关闭同用户旧待支付订单失败", "userID", user.ID, "newOrder", order.ID, "err", err)
	}
	return &CreateOrderResult{Order: order, Payment: params}, nil
}

// buildOrder 按「点数包或会员档位二选一」组装订单行，价格在建单时快照。
func (s *OrderService) buildOrder(ctx context.Context, user model.PlatformUser, packageID, planID string) (*model.Order, error) {
	order := &model.Order{
		ID:      uuid.New(),
		UserID:  user.ID,
		Product: model.ProductCanvas,
		Status:  "pending",
	}
	switch {
	case packageID != "" && planID != "":
		return nil, ErrOrderTargetConflict
	case planID != "":
		var plan model.MembershipPlan
		if err := s.db.WithContext(ctx).First(&plan, "id = ?", planID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, ErrPlanNotPurchasable
			}
			return nil, err
		}
		if !plan.Enabled || plan.DurationDays <= 0 {
			return nil, ErrPlanNotPurchasable
		}
		// 提交给支付渠道时换算成分，价格必须是 10000 微元的整数倍（整分）。
		if plan.PriceMicros <= 0 || plan.PriceMicros%10000 != 0 {
			return nil, errors.New("档位价格必须是整分（10000 微元的整数倍）")
		}
		order.PlanID = &plan.ID
		order.PriceMicros = plan.PriceMicros
		order.Currency = plan.Currency
		order.EntitlementDays = plan.DurationDays
	default:
		var pack model.CreditPackage
		if err := s.db.WithContext(ctx).First(&pack, "id = ?", packageID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, ErrPackageNotFound
			}
			return nil, err
		}
		if !pack.Enabled {
			return nil, ErrPackageNotFound
		}
		if pack.PriceMicros <= 0 || pack.PriceMicros%10000 != 0 {
			return nil, errors.New("档位价格必须是整分（10000 微元的整数倍）")
		}
		order.PackageID = pack.ID
		order.PriceMicros = pack.PriceMicros
		order.Currency = pack.Currency
		order.PurchasedMicros = pack.PriceMicros
		order.GrantedMicros = pack.BonusMicros
	}
	return order, nil
}

// ErrPackageNotFound 表示点数包不存在或已下架。
var ErrPackageNotFound = errors.New("充值档位不存在或已下架")

// ErrPlanNotPurchasable 表示会员档位不存在、未上架或不可购买。
var ErrPlanNotPurchasable = errors.New("会员档位不可购买")

// ErrOrderTargetConflict 表示 packageId 与 planId 同时传给下单接口。
var ErrOrderTargetConflict = errors.New("packageId 与 planId 只能二选一")

// ListOrders 游标分页返回当前用户的订单。
func (s *OrderService) ListOrders(ctx context.Context, userID uuid.UUID, cursor string, size int, status string) ([]model.Order, string, error) {
	query := s.db.WithContext(ctx).Where("user_id = ?", userID)
	if status != "" {
		if !validOrderStatus(status) {
			return nil, "", ErrInvalidCursor
		}
		query = query.Where("status = ?", status)
	}
	if cursor != "" {
		createdAt, id, err := DecodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		query = query.Where("(created_at < ?) OR (created_at = ? AND id < ?)", createdAt, createdAt, id)
	}
	var items []model.Order
	if err := query.Order("created_at DESC, id DESC").Limit(size + 1).Find(&items).Error; err != nil {
		return nil, "", err
	}
	nextCursor := ""
	if len(items) > size {
		items = items[:size]
		last := items[len(items)-1]
		nextCursor = EncodeCursor(last.CreatedAt, last.ID)
	}
	return items, nextCursor, nil
}

func validOrderStatus(status string) bool {
	switch status {
	case "pending", "paid", "failed", "refunded":
		return true
	default:
		return false
	}
}

// CancelOrder 取消待支付订单，只有 pending 状态能取消。
func (s *OrderService) CancelOrder(ctx context.Context, userID, orderID uuid.UUID) (*model.Order, error) {
	result := s.db.WithContext(ctx).Model(&model.Order{}).
		Where("id = ? AND user_id = ? AND status = ?", orderID, userID, "pending").
		Update("status", "failed")
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		var order model.Order
		if err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", orderID, userID).First(&order).Error; err != nil {
			return nil, err
		}
		if order.Status == "paid" {
			return nil, ErrOrderAlreadyPaid
		}
		return &order, nil
	}
	var order model.Order
	if err := s.db.WithContext(ctx).First(&order, "id = ?", orderID).Error; err != nil {
		return nil, err
	}
	return &order, nil
}

// GetOrder 读取当前用户的一条订单，跨用户访问返回 ErrOrderNotFound。
func (s *OrderService) GetOrder(ctx context.Context, userID, orderID uuid.UUID) (*model.Order, error) {
	var order model.Order
	err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", orderID, userID).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrOrderNotFound
	}
	return &order, err
}

// ErrOrderNotFound 表示订单不存在或不属于当前用户（跨用户访问一律 404）。
var ErrOrderNotFound = errors.New("订单不存在")

// HandleCallback 处理渠道回调。验签失败由调用方处理；这里只负责定位订单、校验金额与到账。
func (s *OrderService) HandleCallback(ctx context.Context, providerName string, result payment.CallbackResult) error {
	if !s.registry.Has(providerName) {
		return ErrProviderUnavailable
	}
	orderID, err := uuid.Parse(result.OutTradeNo)
	if err != nil {
		return ErrOrderNotFound
	}
	var order model.Order
	if err := s.db.WithContext(ctx).First(&order, "id = ?", orderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrOrderNotFound
		}
		return err
	}

	switch result.Status {
	case "paid":
		// 金额只与本地快照比较，绝不能覆盖本地记录；无条件比对——
		// 「解析结果为 0」不再短路校验，防止新渠道把解析失败静默成 0 到账（差异清单 #7）。
		if result.PriceMicros != order.PriceMicros {
			slog.Error("支付回调金额与本地订单不一致，拒绝到账",
				"order", order.ID, "provider", providerName,
				"callbackMicros", result.PriceMicros, "localMicros", order.PriceMicros)
			return errors.New("回调金额与本地订单不一致")
		}
		return s.markPaid(ctx, &order, result.ProviderOrderID)
	case "closed":
		return s.markFailed(ctx, &order)
	default:
		return nil
	}
}

// markPaid 在一个事务内完成状态跃迁、双桶入账、会员发放与流水写入。
// 重复回调命中已支付状态时直接返回成功，不做任何写入。
// 加点（billing.Purchase）与发会员（membership.GrantFromOrder）在同一事务，
// 任一失败整体回滚，下次回调幂等重放（异常矩阵路径二）。
// 渠道报支付成功但订单已是终态（failed/refunded）时同样返回成功（防渠道重试）、绝不入账：
// 记 error 日志，并在事务提交后触发 OnPaidOrderClosed 丢钱告警。
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

func (s *OrderService) markFailed(ctx context.Context, order *model.Order) error {
	return s.db.WithContext(ctx).Model(&model.Order{}).
		Where("id = ? AND status = ?", order.ID, "pending").
		Update("status", "failed").Error
}

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
	expired := 0
	for i := range orders {
		order := &orders[i]
		provider, ok := s.registry.Get(order.Provider)
		if !ok {
			continue
		}
		result, err := provider.QueryOrder(ctx, order)
		if err != nil {
			slog.Warn("超时订单主动查询失败，保留待支付状态", "order", order.ID, "err", err)
			continue
		}
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
			// 渠道侧仍未支付（pending/未知状态）：支付窗口已过，关单防止旧支付链接被误付。
			if err := s.markFailed(ctx, order); err != nil {
				slog.Error("置失败订单失败", "order", order.ID, "err", err)
			} else {
				expired++
			}
		}
	}
	return expired, nil
}
