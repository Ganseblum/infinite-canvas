package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
)

// CatalogService 负责模型目录与限时折扣的读取与校验。
// 价格与促销的写入全部收口在这里，handler 不允许直接写 model_catalog 或 model_price_promotions。
type CatalogService struct {
	db               *gorm.DB
	promotionEnabled func() bool
}

func NewCatalogService(db *gorm.DB, promotionEnabled func() bool) *CatalogService {
	if promotionEnabled == nil {
		promotionEnabled = func() bool { return true }
	}
	return &CatalogService{db: db, promotionEnabled: promotionEnabled}
}

// ModelPayload 是 /api/models 返回给前端的模型对象。
type ModelPayload struct {
	ID                  uuid.UUID        `json:"id"`
	Name                string           `json:"name"`
	DisplayName         string           `json:"displayName"`
	Capability          string           `json:"capability"`
	Provider            string           `json:"provider"`
	Constraints         json.RawMessage  `json:"constraints"`
	CreditCost          json.RawMessage  `json:"creditCost"`
	EffectivePrices     []EffectivePrice `json:"effectivePrices"`
	NextPricingChangeAt *time.Time       `json:"nextPricingChangeAt"`
	FreeTrialEligible   bool             `json:"freeTrialEligible"`
}

// ListModels 返回 enabled = true 的模型，按 sort 升序，可选能力筛选。
func (s *CatalogService) ListModels(ctx context.Context, capability string) ([]ModelPayload, error) {
	query := s.db.WithContext(ctx).Where("enabled = ?", true)
	if capability != "" {
		if !validCapability(capability) {
			return nil, ErrInvalidCapability
		}
		query = query.Where("capability = ?", capability)
	}
	var models []model.ModelCatalog
	if err := query.Order("sort ASC, name ASC").Find(&models).Error; err != nil {
		return nil, err
	}

	now := time.Now()
	promotionsByModel, err := s.promotionsByModel(ctx)
	if err != nil {
		return nil, err
	}

	payloads := make([]ModelPayload, 0, len(models))
	for _, item := range models {
		payload, err := s.buildPayload(item, promotionsByModel[item.ID], now)
		if err != nil {
			return nil, err
		}
		payloads = append(payloads, payload)
	}
	return payloads, nil
}

// ErrInvalidCapability 表示 capability 不在枚举内。
var ErrInvalidCapability = errors.New("capability 取值非法")

func validCapability(capability string) bool {
	switch capability {
	case "image", "video", "text", "audio":
		return true
	default:
		return false
	}
}

func (s *CatalogService) promotionsByModel(ctx context.Context) (map[uuid.UUID][]model.ModelPricePromotion, error) {
	var promotions []model.ModelPricePromotion
	if err := s.db.WithContext(ctx).Order("priority DESC").Find(&promotions).Error; err != nil {
		return nil, err
	}
	grouped := map[uuid.UUID][]model.ModelPricePromotion{}
	for _, promo := range promotions {
		if promo.Status == "disabled" || promo.Status == "draft" {
			continue
		}
		grouped[promo.ModelID] = append(grouped[promo.ModelID], promo)
	}
	return grouped, nil
}

// PromotionEnabled 返回限时折扣总开关状态。
func (s *CatalogService) PromotionEnabled() bool { return s.promotionEnabled() }

// PromotionsFor 返回某个模型的全部活动（含未生效与已结束，由解析器按时间筛选）。
func (s *CatalogService) PromotionsFor(modelID uuid.UUID) ([]model.ModelPricePromotion, error) {
	var promotions []model.ModelPricePromotion
	err := s.db.Where("model_id = ?", modelID).Order("priority DESC").Find(&promotions).Error
	return promotions, err
}

// EffectivePricesFor 按当前时间与优先级计算某组价格矩阵的展示报价。
func (s *CatalogService) EffectivePricesFor(cost CreditCost, constraints ModelConstraints, promotions []model.ModelPricePromotion, now time.Time) []EffectivePrice {
	enabled := s.promotionEnabled()
	prices := make([]EffectivePrice, 0, len(cost.Prices))
	for _, price := range cost.Prices {
		effective := EffectivePrice{Params: price.Params, BaseCostMicros: price.CostMicros, FinalCostMicros: price.CostMicros}
		if promo := ResolveMatch(promotions, price.Params, now, enabled); promo != nil {
			effective.FinalCostMicros = ApplyDiscount(price.CostMicros, promo.DiscountBPS)
			effective.Discount = &AppliedDiscount{
				PromotionID: promo.ID.String(),
				Name:        promo.Name,
				DiscountBPS: promo.DiscountBPS,
				EndsAt:      promo.EndsAt,
			}
		}
		prices = append(prices, effective)
	}
	return prices
}

// NextPricingChangeAt 返回下一次活动开始或结束时间，供前端在窗口聚焦时刷新价格。
func NextPricingChangeAt(promotions []model.ModelPricePromotion, now time.Time) *time.Time {
	var next *time.Time
	for i := range promotions {
		promo := &promotions[i]
		if promo.Status == "disabled" || promo.Status == "draft" {
			continue
		}
		candidates := []time.Time{promo.StartsAt, promo.EndsAt}
		for _, candidate := range candidates {
			if !candidate.After(now) {
				continue
			}
			// 已经结束或未开始的活动不影响当前价格，但开始/结束时刻仍需提示刷新。
			if next == nil || candidate.Before(*next) {
				at := candidate
				next = &at
			}
		}
	}
	return next
}

func (s *CatalogService) buildPayload(item model.ModelCatalog, promotions []model.ModelPricePromotion, now time.Time) (ModelPayload, error) {
	constraints, err := ParseConstraints(item.Constraints)
	if err != nil {
		return ModelPayload{}, err
	}
	cost, err := ParseCreditCost(item.CreditCost)
	if err != nil {
		return ModelPayload{}, err
	}
	return ModelPayload{
		ID:                  item.ID,
		Name:                item.Name,
		DisplayName:         item.DisplayName,
		Capability:          item.Capability,
		Provider:            item.Provider,
		Constraints:         json.RawMessage(item.Constraints),
		CreditCost:          json.RawMessage(item.CreditCost),
		EffectivePrices:     s.EffectivePricesFor(cost, constraints, promotions, now),
		NextPricingChangeAt: NextPricingChangeAt(promotions, now),
		FreeTrialEligible:   item.FreeTrialEligible,
	}, nil
}

// ValidatePromotion 校验折扣活动：折扣系数、时间范围、匹配参数必须在模型约束内，
// 以及与既有规则存在「可同时命中且具体度、优先级完全相同」的冲突。
func (s *CatalogService) ValidatePromotion(ctx context.Context, target model.ModelPricePromotion) error {
	if target.DiscountBPS <= 0 || target.DiscountBPS >= 10000 {
		return errors.New("折扣系数必须在 0 到 10000 之间且不能等于原价")
	}
	if !target.EndsAt.After(target.StartsAt) {
		return errors.New("结束时间必须晚于开始时间")
	}
	var catalog model.ModelCatalog
	if err := s.db.WithContext(ctx).First(&catalog, "id = ?", target.ModelID).Error; err != nil {
		return err
	}
	constraints, err := ParseConstraints(catalog.Constraints)
	if err != nil {
		return err
	}
	match, err := parseMatchParams(target.MatchParams)
	if err != nil {
		return err
	}
	for key, value := range match {
		values := constraints.OptionValues(key)
		if len(values) == 0 {
			return errors.New("匹配参数包含该模型 constraints 外的键: " + key)
		}
		if !contains(values, value) {
			return errors.New("匹配参数包含 constraints 外的取值: " + key + "=" + value)
		}
	}

	var existing []model.ModelPricePromotion
	if err := s.db.WithContext(ctx).Where("model_id = ? AND id <> ?", target.ModelID, target.ID).Find(&existing).Error; err != nil {
		return err
	}
	newSpecificity := len(match)
	for _, promo := range existing {
		if promo.Status == "disabled" || promo.Status == "ended" {
			continue
		}
		// 时间不重叠就不可能同时命中。
		if !promo.StartsAt.Before(target.EndsAt) || !target.StartsAt.Before(promo.EndsAt) {
			continue
		}
		otherMatch, err := parseMatchParams(promo.MatchParams)
		if err != nil {
			return err
		}
		if len(otherMatch) != newSpecificity || promo.Priority != target.Priority {
			continue
		}
		if !matchCompatible(match, otherMatch) {
			continue
		}
		return ErrPromotionConflict
	}
	return nil
}

func parseMatchParams(raw []byte) (map[string]string, error) {
	if len(raw) == 0 {
		return map[string]string{}, nil
	}
	match := map[string]string{}
	if err := json.Unmarshal(raw, &match); err != nil {
		return nil, errors.New("匹配参数必须是键值对象")
	}
	return match, nil
}

// matchCompatible 判断两组匹配条件是否存在可以同时命中的参数组合。
func matchCompatible(a, b map[string]string) bool {
	for key, valueA := range a {
		if valueB, ok := b[key]; ok && valueA != valueB {
			return false
		}
	}
	return true
}

// ModelSummary 是管理后台模型列表与编辑需要的完整字段。
type ModelSummary struct {
	ID                uuid.UUID       `json:"id"`
	Name              string          `json:"name"`
	DisplayName       string          `json:"displayName"`
	Capability        string          `json:"capability"`
	Provider          string          `json:"provider"`
	Constraints       json.RawMessage `json:"constraints"`
	CreditCost        json.RawMessage `json:"creditCost"`
	ChannelIDs        json.RawMessage `json:"channelIds"`
	FreeTrialEligible bool            `json:"freeTrialEligible"`
	Enabled           bool            `json:"enabled"`
	Sort              int             `json:"sort"`
	CreatedAt         string          `json:"createdAt"`
	UpdatedAt         string          `json:"updatedAt"`
}

func NewModelSummary(item model.ModelCatalog) ModelSummary {
	return ModelSummary{
		ID:                item.ID,
		Name:              item.Name,
		DisplayName:       item.DisplayName,
		Capability:        item.Capability,
		Provider:          item.Provider,
		Constraints:       json.RawMessage(item.Constraints),
		CreditCost:        json.RawMessage(item.CreditCost),
		ChannelIDs:        json.RawMessage(item.ChannelIDs),
		FreeTrialEligible: item.FreeTrialEligible,
		Enabled:           item.Enabled,
		Sort:              item.Sort,
		CreatedAt:         item.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:         item.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// SortPrices 返回按维度排序后的价格矩阵，便于后台预览。
func SortPrices(cost CreditCost) []PriceEntry {
	prices := append([]PriceEntry(nil), cost.Prices...)
	sort.Slice(prices, func(i, j int) bool {
		return joinParts(priceParts(prices[i])) < joinParts(priceParts(prices[j]))
	})
	return prices
}

func priceParts(entry PriceEntry) []string {
	parts := make([]string, 0, len(entry.Params))
	for _, value := range entry.Params {
		parts = append(parts, value)
	}
	return parts
}
