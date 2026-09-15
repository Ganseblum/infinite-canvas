package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/infinite-canvas/server/internal/model"
)

const (
	// BillingModeCredits 走点数预扣；BillingModeFreeTrial 消耗一次免费额度。
	BillingModeCredits   = "credits"
	BillingModeFreeTrial = "free_trial"

	quoteTTL = 5 * time.Minute
)

// ParamNotSupportedError 表示参数不在该模型 constraints 允许的范围内。
// Param 与 Allowed 供 handler 写入错误响应，前端据此重新渲染可选项。
type ParamNotSupportedError struct {
	Param   string
	Allowed []string
}

func (e *ParamNotSupportedError) Error() string {
	return fmt.Sprintf("参数 %s 不在允许范围内", e.Param)
}

// ErrQuoteStale 表示报价凭证过期、参数不匹配或价格版本已变化。
var ErrQuoteStale = errors.New("报价已失效，请重新报价")

// QuoteService 负责参数校验、价格解析与报价凭证签发校验。
type QuoteService struct {
	catalog *CatalogService
	quota   *QuotaService
	secret  []byte
	trials  map[string]string // capability -> usage metric
}

// NewQuoteService 构造报价服务。secret 用于给 quoteToken 做 HMAC 签名。
func NewQuoteService(catalog *CatalogService, quota *QuotaService, secret string) *QuoteService {
	return &QuoteService{
		catalog: catalog,
		quota:   quota,
		secret:  []byte(secret),
		trials: map[string]string{
			"image": MetricFreeImageTrial,
			"video": MetricFreeVideoTrial,
		},
	}
}

// QuoteParams 是生成请求里需要参与报价与校验的业务参数。
type QuoteParams map[string]string

// ValidateParams 按 constraints 做白名单校验；n 允许缺省并由上限约束。
func ValidateParams(constraints ModelConstraints, params QuoteParams, n *int) error {
	for key, value := range params {
		if key == "n" {
			continue
		}
		allowed := constraints.OptionValues(key)
		if len(allowed) == 0 {
			continue
		}
		if !contains(allowed, value) {
			return &ParamNotSupportedError{Param: key, Allowed: allowed}
		}
	}
	if n != nil && constraints.N != nil && constraints.N.Max > 0 && *n > constraints.N.Max {
		return &ParamNotSupportedError{Param: "n", Allowed: []string{fmt.Sprintf("1-%d", constraints.N.Max)}}
	}
	if n != nil && *n > 15 {
		return &ParamNotSupportedError{Param: "n", Allowed: []string{"1-15"}}
	}
	return nil
}

// ResolveUnitCost 从价格矩阵里解析单次（未乘 n）价格。参数缺省时按维度的第一个合法值补全。
func ResolveUnitCost(cost CreditCost, constraints ModelConstraints, params QuoteParams) (int64, error) {
	if len(cost.Prices) == 0 {
		return 0, errors.New("模型缺少价格矩阵")
	}
	lookup := map[string]string{}
	for dimension, value := range params {
		lookup[dimension] = value
	}
	for _, dimension := range cost.Dimensions {
		if _, ok := lookup[dimension]; ok {
			continue
		}
		values := constraints.OptionValues(dimension)
		if len(values) == 0 {
			return 0, fmt.Errorf("计价维度 %s 没有可选值", dimension)
		}
		lookup[dimension] = values[0]
	}
	for _, price := range cost.Prices {
		matches := true
		for _, dimension := range cost.Dimensions {
			if price.Params[dimension] != lookup[dimension] {
				matches = false
				break
			}
		}
		if matches {
			return price.CostMicros, nil
		}
	}
	return 0, errors.New("当前参数组合没有配置价格")
}

// Quote 是一次报价的完整结果。
type Quote struct {
	BillingMode      string           `json:"billingMode"`
	BaseCostMicros   int64            `json:"baseCostMicros"`
	FinalCostMicros  int64            `json:"finalCostMicros"`
	Discount         *AppliedDiscount `json:"discount"`
	PriceVersion     int              `json:"priceVersion"`
	PromotionVersion *int             `json:"promotionVersion"`
	FreeTrialsLeft   int64            `json:"freeTrialsLeft,omitempty"`
	Token            string           `json:"quoteToken"`
	ExpiresAt        time.Time        `json:"expiresAt"`
}

// quoteTokenPayload 是签名覆盖的载荷，生成时逐项核对，避免旧价被复用。
type quoteTokenPayload struct {
	Model            string      `json:"model"`
	Capability       string      `json:"capability"`
	ParamsHash       string      `json:"paramsHash"`
	Params           QuoteParams `json:"params"`
	N                int         `json:"n"`
	BillingMode      string      `json:"billingMode"`
	BaseCostMicros   int64       `json:"baseCostMicros"`
	FinalCostMicros  int64       `json:"finalCostMicros"`
	PriceVersion     int         `json:"priceVersion"`
	PromotionID      string      `json:"promotionId,omitempty"`
	PromotionVersion int         `json:"promotionVersion,omitempty"`
	PromotionEnabled bool        `json:"promotionEnabled"`
	ExpiresAt        time.Time   `json:"expiresAt"`
}

func canonicalParams(params QuoteParams) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	canonical := ""
	for _, key := range keys {
		canonical += key + "=" + params[key] + "\x00"
	}
	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s *QuoteService) sign(payload quoteTokenPayload) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(raw)
	signature := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (s *QuoteService) verify(token string) (quoteTokenPayload, error) {
	var payload quoteTokenPayload
	rawPart, sigPart, ok := splitToken(token)
	if !ok {
		return payload, ErrQuoteStale
	}
	raw, err := base64.RawURLEncoding.DecodeString(rawPart)
	if err != nil {
		return payload, ErrQuoteStale
	}
	signature, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return payload, ErrQuoteStale
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(raw)
	if !hmac.Equal(mac.Sum(nil), signature) {
		return payload, ErrQuoteStale
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, ErrQuoteStale
	}
	return payload, nil
}

func splitToken(token string) (string, string, bool) {
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			return token[:i], token[i+1:], i > 0 && i < len(token)-1
		}
	}
	return "", "", false
}

// BuildQuote 按当前目录与活动解析报价并签发凭证。报价是纯查询，不写流水、不占余额。
func (s *QuoteService) BuildQuote(ctx context.Context, userID uuid.UUID, catalogItem model.ModelCatalog, capability string, params QuoteParams, n int, now time.Time) (Quote, error) {
	constraints, err := ParseConstraints(catalogItem.Constraints)
	if err != nil {
		return Quote{}, err
	}
	cost, err := ParseCreditCost(catalogItem.CreditCost)
	if err != nil {
		return Quote{}, err
	}
	if n <= 0 {
		n = 1
	}
	if err := ValidateParams(constraints, params, &n); err != nil {
		return Quote{}, err
	}
	unitCost, err := ResolveUnitCost(cost, constraints, params)
	if err != nil {
		return Quote{}, err
	}
	baseCost := unitCost * int64(n)

	// 免费额度优先于折扣：命中时按 0 元报价，不解析活动。
	if free, remaining := s.FreeTrialFor(userID, catalogItem, capability); free {
		_ = free
		payload := quoteTokenPayload{
			Model:            catalogItem.Name,
			Capability:       capability,
			ParamsHash:       canonicalParams(params),
			Params:           params,
			N:                n,
			BillingMode:      BillingModeFreeTrial,
			BaseCostMicros:   0,
			FinalCostMicros:  0,
			PriceVersion:     cost.Version,
			PromotionEnabled: s.catalog.PromotionEnabled(),
			ExpiresAt:        now.Add(quoteTTL),
		}
		token, err := s.sign(payload)
		if err != nil {
			return Quote{}, err
		}
		return Quote{
			BillingMode:     BillingModeFreeTrial,
			BaseCostMicros:  0,
			FinalCostMicros: 0,
			PriceVersion:    cost.Version,
			FreeTrialsLeft:  remaining,
			Token:           token,
			ExpiresAt:       payload.ExpiresAt,
		}, nil
	}

	promotions, err := s.catalog.PromotionsFor(catalogItem.ID)
	if err != nil {
		return Quote{}, err
	}
	promotion := ResolveMatch(promotions, params, now, s.catalog.PromotionEnabled())
	finalCost := baseCost
	var discount *AppliedDiscount
	if promotion != nil {
		finalCost = ApplyDiscount(baseCost, promotion.DiscountBPS)
		discount = &AppliedDiscount{
			PromotionID: promotion.ID.String(),
			Name:        promotion.Name,
			DiscountBPS: promotion.DiscountBPS,
			EndsAt:      promotion.EndsAt,
		}
	}
	payload := quoteTokenPayload{
		Model:            catalogItem.Name,
		Capability:       capability,
		ParamsHash:       canonicalParams(params),
		Params:           params,
		N:                n,
		BillingMode:      BillingModeCredits,
		BaseCostMicros:   baseCost,
		FinalCostMicros:  finalCost,
		PriceVersion:     cost.Version,
		PromotionEnabled: s.catalog.PromotionEnabled(),
	}
	if promotion != nil {
		payload.PromotionID = promotion.ID.String()
		payload.PromotionVersion = promotion.Version
	}
	expiresAt := now.Add(quoteTTL)
	if promotion != nil && promotion.EndsAt.Before(expiresAt) {
		expiresAt = promotion.EndsAt
	}
	payload.ExpiresAt = expiresAt
	token, err := s.sign(payload)
	if err != nil {
		return Quote{}, err
	}
	quote := Quote{
		BillingMode:     BillingModeCredits,
		BaseCostMicros:  baseCost,
		FinalCostMicros: finalCost,
		Discount:        discount,
		PriceVersion:    cost.Version,
		Token:           token,
		ExpiresAt:       expiresAt,
	}
	if promotion != nil {
		quote.PromotionVersion = &payload.PromotionVersion
	}
	return quote, nil
}

// VerifyQuote 校验凭证与当前参数、价格是否仍然一致。
func (s *QuoteService) VerifyQuote(ctx context.Context, userID uuid.UUID, token string, catalogItem model.ModelCatalog, capability string, params QuoteParams, n int, now time.Time) (quoteTokenPayload, error) {
	payload, err := s.verify(token)
	if err != nil {
		return payload, err
	}
	if payload.Model != catalogItem.Name || payload.Capability != capability {
		return payload, ErrQuoteStale
	}
	if canonicalParams(params) != payload.ParamsHash || payload.N != n {
		return payload, ErrQuoteStale
	}
	if !payload.ExpiresAt.After(now) {
		return payload, ErrQuoteStale
	}
	constraints, err := ParseConstraints(catalogItem.Constraints)
	if err != nil {
		return payload, ErrQuoteStale
	}
	cost, err := ParseCreditCost(catalogItem.CreditCost)
	if err != nil {
		return payload, ErrQuoteStale
	}
	if cost.Version != payload.PriceVersion {
		return payload, ErrQuoteStale
	}
	if payload.BillingMode == BillingModeFreeTrial {
		// 免费额度仍然适用才放行；已被并发请求占用时由预扣阶段改判为 QUOTE_STALE。
		if free, _ := s.FreeTrialFor(userID, catalogItem, capability); !free {
			return payload, ErrQuoteStale
		}
		return payload, nil
	}
	unitCost, err := ResolveUnitCost(cost, constraints, params)
	if err != nil {
		return payload, ErrQuoteStale
	}
	if unitCost*int64(n) != payload.BaseCostMicros {
		return payload, ErrQuoteStale
	}
	// 重新解析当前活动：活动开始、停用、修改、结束或全局开关变化都让旧 token 失效。
	if payload.PromotionEnabled != s.catalog.PromotionEnabled() {
		return payload, ErrQuoteStale
	}
	promotions, err := s.catalog.PromotionsFor(catalogItem.ID)
	if err != nil {
		return payload, err
	}
	promotion := ResolveMatch(promotions, params, now, s.catalog.PromotionEnabled())
	if promotion == nil {
		if payload.PromotionID != "" {
			return payload, ErrQuoteStale
		}
		if payload.FinalCostMicros != payload.BaseCostMicros {
			return payload, ErrQuoteStale
		}
		return payload, nil
	}
	if promotion.ID.String() != payload.PromotionID || promotion.Version != payload.PromotionVersion {
		return payload, ErrQuoteStale
	}
	if ApplyDiscount(payload.BaseCostMicros, promotion.DiscountBPS) != payload.FinalCostMicros {
		return payload, ErrQuoteStale
	}
	return payload, nil
}

// FreeTrialFor 判断本次请求是否走免费额度，并返回剩余次数。
// 免费额度优先于折扣，且只对标记了 free_trial_eligible 的模型生效。
func (s *QuoteService) FreeTrialFor(userID uuid.UUID, catalogItem model.ModelCatalog, capability string) (bool, int64) {
	if !catalogItem.FreeTrialEligible {
		return false, 0
	}
	metric, ok := s.trials[capability]
	if !ok {
		return false, 0
	}
	remaining, err := s.quota.FreeTrialRemaining(userID, metric)
	if err != nil || remaining <= 0 {
		return false, 0
	}
	return true, remaining
}

// QuotePayload 暴露给 handler 校验后的计费信息。
type QuotePayload struct {
	BillingMode      string
	BaseCostMicros   int64
	FinalCostMicros  int64
	PriceVersion     int
	PromotionID      *uuid.UUID
	PromotionVersion *int
	PromotionEnabled bool
	Params           QuoteParams
	N                int
}

// ToPayload 把校验结果转成 handler 需要的形状。
func (p quoteTokenPayload) ToPayload() QuotePayload {
	payload := QuotePayload{
		BillingMode:      p.BillingMode,
		BaseCostMicros:   p.BaseCostMicros,
		FinalCostMicros:  p.FinalCostMicros,
		PriceVersion:     p.PriceVersion,
		PromotionEnabled: p.PromotionEnabled,
		Params:           p.Params,
		N:                p.N,
	}
	if p.PromotionID != "" {
		if id, err := uuid.Parse(p.PromotionID); err == nil {
			payload.PromotionID = &id
		}
	}
	if p.PromotionVersion != 0 {
		version := p.PromotionVersion
		payload.PromotionVersion = &version
	}
	return payload
}
