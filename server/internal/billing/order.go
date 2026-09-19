package billing

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/httpx"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/payment"
	"github.com/infinite-canvas/server/internal/service"
)

// OrderHandler 负责下单、订单列表与取消。
type OrderHandler struct {
	db       *gorm.DB
	orders   *service.OrderService
	registry *service.PaymentRegistry
}

func NewOrderHandler(db *gorm.DB, registry *service.PaymentRegistry) *OrderHandler {
	return &OrderHandler{db: db, orders: service.NewOrderService(db, registry), registry: registry}
}

type createOrderReq struct {
	PackageID string `json:"packageId"`
	PlanID    string `json:"planId"`
	Provider  string `json:"provider"`
}

func (h *OrderHandler) Create(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	var req createOrderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	// packageId 与 planId 二选一：都传或都不传按 400 VALIDATION 处理。
	switch {
	case req.PackageID != "" && req.PlanID != "":
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"planId": "packageId 与 planId 只能二选一"}))
		return
	case req.PackageID == "" && req.PlanID == "":
		errs.Abort(c, errs.ErrValidation)
		return
	}
	// 渠道是否可选以注册表为准：未配置的渠道自动不上架，也不允许下单。
	if !h.registry.Has(req.Provider) {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"provider": "provider 取值非法"}))
		return
	}
	var user model.PlatformUser
	if err := h.db.First(&user, "id = ?", uid).Error; err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	result, err := h.orders.CreateOrder(c.Request.Context(), user, req.PackageID, req.PlanID, req.Provider)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAccountPendingDeletion):
			errs.Abort(c, errs.ErrAccountPendingDeletion)
		case errors.Is(err, service.ErrPackageNotFound):
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"packageId": "档位不存在或已下架"}))
		case errors.Is(err, service.ErrPlanNotPurchasable):
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"planId": "会员档位不存在或不可购买"}))
		case errors.Is(err, service.ErrProviderUnavailable):
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"provider": "支付渠道未配置"}))
		default:
			slog.Error("创建订单失败", "err", err)
			errs.Abort(c, errs.ErrInternal)
		}
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"order":   OrderPayload(result.Order),
		"payment": gin.H{"type": result.Payment.Type, "payload": result.Payment.Payload},
	})
}

func (h *OrderHandler) List(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	size, err := httpx.ParseCursorSize(c.Query("size"))
	if err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"size": "size 必须是 1-100 的整数"}))
		return
	}
	items, nextCursor, err := h.orders.ListOrders(c.Request.Context(), uid, c.Query("cursor"), size, c.Query("status"))
	if err != nil {
		if errors.Is(err, service.ErrInvalidCursor) {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"cursor": "游标或状态参数不合法"}))
			return
		}
		slog.Error("读取订单列表失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	payloads := make([]gin.H, 0, len(items))
	for i := range items {
		payloads = append(payloads, OrderPayload(&items[i]))
	}
	payload := gin.H{"items": payloads, "nextCursor": nil}
	if nextCursor != "" {
		payload["nextCursor"] = nextCursor
	}
	c.JSON(http.StatusOK, payload)
}

func (h *OrderHandler) Get(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	order, err := h.orders.GetOrder(c.Request.Context(), uid, orderID)
	if err != nil {
		if errors.Is(err, service.ErrOrderNotFound) {
			errs.Abort(c, errs.ErrNotFound)
			return
		}
		slog.Error("读取订单失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"order": OrderPayload(order)})
}

func (h *OrderHandler) Cancel(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	order, err := h.orders.CancelOrder(c.Request.Context(), uid, orderID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrOrderAlreadyPaid):
			errs.Abort(c, errs.ErrOrderAlreadyPaid)
		case errors.Is(err, gorm.ErrRecordNotFound):
			errs.Abort(c, errs.ErrNotFound)
		default:
			slog.Error("取消订单失败", "err", err)
			errs.Abort(c, errs.ErrInternal)
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"order": OrderPayload(order)})
}

func OrderPayload(order *model.Order) gin.H {
	payload := gin.H{
		"id":              order.ID.String(),
		"provider":        order.Provider,
		"packageId":       order.PackageID,
		"priceMicros":     order.PriceMicros,
		"currency":        order.Currency,
		"purchasedMicros": order.PurchasedMicros,
		"grantedMicros":   order.GrantedMicros,
		"entitlementDays": order.EntitlementDays,
		"status":          order.Status,
		"paidAt":          httpx.FormatTimePtr(order.PaidAt),
		"createdAt":       httpx.FormatTime(order.CreatedAt),
		"updatedAt":       httpx.FormatTime(order.UpdatedAt),
	}
	if order.ProviderOrderID != nil {
		payload["providerOrderId"] = *order.ProviderOrderID
	}
	return payload
}

// PaymentHandler 是支付回调入口。公开路由，不鉴权但必须验签。
type PaymentHandler struct {
	registry *service.PaymentRegistry
	orders   *service.OrderService
}

func NewPaymentHandler(db *gorm.DB, registry *service.PaymentRegistry) *PaymentHandler {
	return &PaymentHandler{registry: registry, orders: service.NewOrderService(db, registry)}
}

func (h *PaymentHandler) Webhook(c *gin.Context) {
	providerName := c.Param("provider")
	provider, ok := h.registry.Get(providerName)
	if !ok {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	result, err := provider.VerifyAndParse(c.Request.Context(), c.Request)
	if err != nil {
		if errors.Is(err, payment.ErrInvalidSignature) {
			errs.Abort(c, errs.ErrInvalidSignature)
			return
		}
		slog.Error("解析支付回调失败", "provider", providerName, "err", err)
		errs.Abort(c, errs.ErrInvalidSignature)
		return
	}
	if err := h.orders.HandleCallback(c.Request.Context(), providerName, result); err != nil {
		if errors.Is(err, service.ErrOrderNotFound) {
			// 返回非成功响应让渠道按自身策略重试，覆盖「回调先于下单响应到达」的时序。
			errs.Abort(c, errs.ErrNotFound)
			return
		}
		if errors.Is(err, service.ErrProviderUnavailable) {
			errs.Abort(c, errs.ErrNotFound)
			return
		}
		slog.Error("处理支付回调失败", "provider", providerName, "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	// 响应格式按渠道要求返回，不能套用项目统一错误格式，否则渠道会判定回调失败并持续重试。
	switch providerName {
	case "alipay", "easypay":
		// 易支付（彩虹协议）要求响应体恰好是裸文本 success（不换行），否则会判定通知失败并持续重推。
		c.String(http.StatusOK, "success")
	case "wechat":
		c.JSON(http.StatusOK, gin.H{"code": "SUCCESS", "message": "成功"})
	default:
		c.Status(http.StatusOK)
	}
}
