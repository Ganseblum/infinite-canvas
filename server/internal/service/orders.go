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
)

// ErrOrderAlreadyPaid 表示取消一个已支付的订单，调用方映射为 409 ORDER_ALREADY_PAID。
var ErrOrderAlreadyPaid = errors.New("订单已支付")

// ErrAccountPendingDeletion 表示注销冷静期内调用下单，调用方映射为 403。
var ErrAccountPendingDeletion = errors.New("账号处于注销冷静期")

// OrderService 负责订单创建、列表、取消与支付到账。
// 渠道差异全部收敛在 internal/payment，本层只认统一后的结构。
type OrderService struct {
	db       *gorm.DB
	registry *PaymentRegistry
	credits  *CreditService
}

func NewOrderService(db *gorm.DB, registry *PaymentRegistry) *OrderService {
	return &OrderService{db: db, registry: registry, credits: NewCreditService(db)}
}

// CreateOrderResult 是下单成功后的返回结构。
type CreateOrderResult struct {
	Order   *model.Order   `json:"order"`
	Payment payment.Params `json:"payment"`
}

// CreateOrder 建单并生成支付参数。价格、两个桶与权益天数在建单时从档位快照到订单行。
func (s *OrderService) CreateOrder(ctx context.Context, user model.PlatformUser, packageID, providerName string) (*CreateOrderResult, error) {
	if user.Status == "pending_deletion" {
		return nil, ErrAccountPendingDeletion
	}
	provider, ok := s.registry.Get(providerName)
	if !ok {
		return nil, ErrProviderUnavailable
	}
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
	// 提交给支付渠道时换算成分，因此价格必须是 10000 微元的整数倍（整分）。
	if pack.PriceMicros <= 0 || pack.PriceMicros%10000 != 0 {
		return nil, errors.New("档位价格必须是整分（10000 微元的整数倍）")
	}

	order := &model.Order{
		ID:              uuid.New(),
		UserID:          user.ID,
		Provider:        provider.Name(),
		PackageID:       pack.ID,
		PriceMicros:     pack.PriceMicros,
		Currency:        pack.Currency,
		PurchasedMicros: pack.PriceMicros,
		GrantedMicros:   pack.BonusMicros,
		EntitlementDays: pack.EntitlementDays,
		Status:          "pending",
	}
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
	return &CreateOrderResult{Order: order, Payment: params}, nil
}

// ErrPackageNotFound 表示档位不存在或已下架。
var ErrPackageNotFound = errors.New("充值档位不存在或已下架")

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

// markPaid 在一个事务内完成状态跃迁、双桶入账、权益延长与流水写入。
// 重复回调命中已支付状态时直接返回成功，不做任何写入。
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
		return s.credits.Purchase(ctx, tx, &locked, now)
	})
}

func (s *OrderService) markFailed(ctx context.Context, order *model.Order) error {
	return s.db.WithContext(ctx).Model(&model.Order{}).
		Where("id = ? AND status = ?", order.ID, "pending").
		Update("status", "failed").Error
}

// ExpirePendingOrders 把超过 30 分钟未支付的订单置为 failed。
// 置失败前必须向渠道主动查询一次真实状态：用户可能在最后一秒付款而回调还在路上。
func (s *OrderService) ExpirePendingOrders(ctx context.Context, now time.Time, timeout time.Duration) (int, error) {
	var orders []model.Order
	if err := s.db.WithContext(ctx).
		Where("status = ? AND created_at < ?", "pending", now.Add(-timeout)).
		Limit(200).Find(&orders).Error; err != nil {
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
			// 渠道侧仍然可支付，本地不判失败。
		}
	}
	return expired, nil
}
