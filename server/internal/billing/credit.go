// Package billing 是点数与订单面：余额与流水查询、点数包与会员档位展示、
// 下单、订单查询取消与支付回调入口。扣点、落账等写路径在 platform/billing
// 与 service 层，本包 handler 只做参数校验、错误映射与响应拼装。
package billing

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/cursor"
	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/httpx"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/platform/membership"
	"github.com/infinite-canvas/server/internal/service"
)

// CreditHandler 提供余额、流水与充值档位查询。流水只读，没有任何写接口。
type CreditHandler struct {
	db       *gorm.DB
	credits  *billing.Service
	registry *service.PaymentRegistry
}

func NewCreditHandler(db *gorm.DB, registry *service.PaymentRegistry) *CreditHandler {
	return &CreditHandler{db: db, credits: billing.NewService(db, model.ProductCanvas), registry: registry}
}

// GetBalance 返回余额摘要（购买/赠送分桶）与最近 5 条流水；无账本行按零余额处理。
func (h *CreditHandler) GetBalance(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	balance, err := h.credits.Balance(c.Request.Context(), uid)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		balance = model.CreditAccount{UserID: uid}
	} else if err != nil {
		slog.Error("读取点数余额失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	recent, err := h.credits.RecentTransactions(c.Request.Context(), uid, 5)
	if err != nil {
		slog.Error("读取最近流水失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"purchasedMicros": balance.PurchasedMicros,
		"grantedMicros":   balance.GrantedMicros,
		"totalMicros":     balance.PurchasedMicros + balance.GrantedMicros,
		"paidUntil":       httpx.FormatTimePtr(membership.LatestPaidUntil(h.db, uid)),
		"recent":          transactionPayloads(recent),
	})
}

// ListTransactions 游标分页返回点数流水，可按 type 筛选。
func (h *CreditHandler) ListTransactions(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	size, err := httpx.ParseCursorSize(c.Query("size"))
	if err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"size": "size 必须是 1-100 的整数"}))
		return
	}
	items, nextCursor, err := h.credits.ListTransactions(c.Request.Context(), uid, c.Query("cursor"), size, c.Query("type"))
	if err != nil {
		if errors.Is(err, cursor.ErrInvalidCursor) {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"cursor": "游标或筛选参数不合法"}))
			return
		}
		slog.Error("读取点数流水失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	payload := gin.H{"items": transactionPayloads(items), "nextCursor": nil}
	if nextCursor != "" {
		payload["nextCursor"] = nextCursor
	}
	c.JSON(http.StatusOK, payload)
}

func transactionPayloads(items []model.CreditTransaction) []gin.H {
	payloads := make([]gin.H, 0, len(items))
	for _, item := range items {
		payload := gin.H{
			"id":                 item.ID.String(),
			"bucket":             item.Bucket,
			"type":               item.Type,
			"amountMicros":       item.AmountMicros,
			"balanceAfterMicros": item.BalanceAfterMicros,
			"createdAt":          httpx.FormatTime(item.CreatedAt),
		}
		if item.RefType != "" {
			payload["refType"] = item.RefType
		}
		if item.RefID != "" {
			payload["refId"] = item.RefID
		}
		if item.Note != "" {
			payload["note"] = item.Note
		}
		payloads = append(payloads, payload)
	}
	return payloads
}

// ListPackages 返回已上架的点数包，按 sort 升序；同时返回当前已配置的支付渠道，
// 前端据此只展示可用渠道，而不是让用户点进去才失败。
// 点数包不再携带会员时长（D5 拆分）：entitlementDays 键保留形状、固定输出 0。
func (h *CreditHandler) ListPackages(c *gin.Context) {
	var packs []model.CreditPackage
	if err := h.db.Where("enabled = ?", true).Order("sort ASC, id ASC").Find(&packs).Error; err != nil {
		slog.Error("读取充值档位失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]gin.H, 0, len(packs))
	for _, pack := range packs {
		items = append(items, gin.H{
			"id":              pack.ID,
			"name":            pack.Name,
			"priceMicros":     pack.PriceMicros,
			"purchasedMicros": pack.PriceMicros,
			"bonusMicros":     pack.BonusMicros,
			"entitlementDays": 0,
			"currency":        pack.Currency,
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "providers": h.registry.Names()})
}

// ListPlans 返回会员档位定义，供定价页展示三档存储与保留期。
// 可购买的档位（DurationDays>0）追加 priceMicros/durationDays 两个加法键。
func (h *CreditHandler) ListPlans(c *gin.Context) {
	var plans []model.MembershipPlan
	if err := h.db.Where("enabled = ?", true).Order("sort ASC, id ASC").Find(&plans).Error; err != nil {
		slog.Error("读取档位失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]gin.H, 0, len(plans))
	for _, plan := range plans {
		item := gin.H{
			"id":            plan.ID,
			"name":          plan.Name,
			"storageBytes":  plan.StorageBytes,
			"maxFileBytes":  plan.MaxFileBytes,
			"retentionDays": plan.RetentionDays,
		}
		if plan.DurationDays > 0 {
			item["priceMicros"] = plan.PriceMicros
			item["durationDays"] = plan.DurationDays
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
