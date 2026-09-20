package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"gorm.io/datatypes"

	"github.com/infinite-canvas/server/internal/model"
)

// ErrPromotionConflict 表示保存折扣活动时与既有规则存在可同时命中、
// 具体度与优先级又完全相同的冲突，调用方映射为 409 PROMOTION_CONFLICT。
var ErrPromotionConflict = errors.New("折扣活动与既有规则冲突")

// ConstraintOption 是参数白名单中的一个取值，value 是传给上游与校验用的实际值。
type ConstraintOption struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// ModelConstraints 描述模型接受哪些参数。键名一律用单数，与请求体参数名逐个对应。
// 数组型的键是取值白名单；n 是范围约束；features 是能力开关字符串数组。
type ModelConstraints struct {
	Size       []ConstraintOption `json:"size"`
	Ratio      []ConstraintOption `json:"ratio"`
	Resolution []ConstraintOption `json:"resolution"`
	Quality    []ConstraintOption `json:"quality"`
	Duration   []ConstraintOption `json:"duration"`
	N          *IntConstraint     `json:"n"`
	Features   []string           `json:"features"`
}

// IntConstraint 是数值参数（如 n）的范围约束，当前只配上限。
type IntConstraint struct {
	Max int `json:"max"`
}

// FlexibleOption 允许 JSON 里写 "1024x1024" 或 { "value": "1024x1024", "label": "1K" }。
type FlexibleOption struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// UnmarshalJSON 兼容四种写法：字符串、数字、{ value, label }，以及数字形式的 value。
// 时长这类约束天然是数字数组，解析层统一转成字符串参与白名单与价格矩阵匹配。
func (o *FlexibleOption) UnmarshalJSON(raw []byte) error {
	var plain string
	if err := json.Unmarshal(raw, &plain); err == nil {
		o.Value = plain
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		o.Value = number.String()
		return nil
	}
	var full struct {
		Value any    `json:"value"`
		Label string `json:"label"`
	}
	if err := json.Unmarshal(raw, &full); err != nil {
		return err
	}
	switch typed := full.Value.(type) {
	case string:
		o.Value = typed
	case float64:
		o.Value = json.Number(strconv.FormatFloat(typed, 'f', -1, 64)).String()
	default:
		o.Value = fmt.Sprint(full.Value)
	}
	o.Label = full.Label
	return nil
}

func (o FlexibleOption) MarshalJSON() ([]byte, error) {
	if o.Label == "" {
		return json.Marshal(o.Value)
	}
	return json.Marshal(map[string]string{"value": o.Value, "label": o.Label})
}

// rawConstraints 与 FlexibleOption 配合解析目录里的 constraints 字段。
type rawConstraints struct {
	Size       []FlexibleOption `json:"size"`
	Ratio      []FlexibleOption `json:"ratio"`
	Resolution []FlexibleOption `json:"resolution"`
	Quality    []FlexibleOption `json:"quality"`
	Duration   []FlexibleOption `json:"duration"`
	N          *IntConstraint   `json:"n"`
	Features   []string         `json:"features"`
}

// RawOption 返回原始形态，保持入库时的形状（字符串或对象）不动。
func (o FlexibleOption) RawOption() any {
	if o.Label == "" {
		return o.Value
	}
	return ConstraintOption{Value: o.Value, Label: o.Label}
}

// ParseConstraints 解析 constraints JSON。同时兼容字符串数组与 { value, label } 数组。
func ParseConstraints(raw datatypes.JSON) (ModelConstraints, error) {
	var parsed rawConstraints
	if len(raw) == 0 {
		return ModelConstraints{}, nil
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ModelConstraints{}, fmt.Errorf("constraints 解析失败: %w", err)
	}
	out := ModelConstraints{
		N:        parsed.N,
		Features: parsed.Features,
	}
	for _, item := range parsed.Size {
		out.Size = append(out.Size, ConstraintOption{Value: item.Value, Label: item.Label})
	}
	for _, item := range parsed.Ratio {
		out.Ratio = append(out.Ratio, ConstraintOption{Value: item.Value, Label: item.Label})
	}
	for _, item := range parsed.Resolution {
		out.Resolution = append(out.Resolution, ConstraintOption{Value: item.Value, Label: item.Label})
	}
	for _, item := range parsed.Quality {
		out.Quality = append(out.Quality, ConstraintOption{Value: item.Value, Label: item.Label})
	}
	for _, item := range parsed.Duration {
		out.Duration = append(out.Duration, ConstraintOption{Value: item.Value, Label: item.Label})
	}
	return out, nil
}

// OptionValues 取某个约束键的全部合法取值。
func (c ModelConstraints) OptionValues(key string) []string {
	switch key {
	case "size":
		return optionValues(c.Size)
	case "ratio":
		return optionValues(c.Ratio)
	case "resolution":
		return optionValues(c.Resolution)
	case "quality":
		return optionValues(c.Quality)
	case "duration":
		return optionValues(c.Duration)
	default:
		return nil
	}
}

func optionValues(options []ConstraintOption) []string {
	values := make([]string, 0, len(options))
	for _, option := range options {
		values = append(values, option.Value)
	}
	return values
}

// HasFeature 判断模型是否声明了某项能力（如 mask、referenceImage）。
func (c ModelConstraints) HasFeature(name string) bool {
	for _, feature := range c.Features {
		if feature == name {
			return true
		}
	}
	return false
}

// PriceParams 是价格组合的参数键值。JSON 里数字与字符串混写（时长天然是数字），
// 统一转成字符串参与匹配，保证 constraints 与价格矩阵两侧的键值形态一致。
type PriceParams map[string]string

func (p *PriceParams) UnmarshalJSON(raw []byte) error {
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		return err
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		switch typed := value.(type) {
		case string:
			out[key] = typed
		case float64:
			out[key] = strconv.FormatFloat(typed, 'f', -1, 64)
		default:
			out[key] = fmt.Sprint(value)
		}
	}
	*p = out
	return nil
}

// PriceEntry 是价格矩阵里的一条组合报价。
type PriceEntry struct {
	Params     PriceParams `json:"params"`
	CostMicros int64       `json:"costMicros"`
}

// CreditCost 是模型的基础价格矩阵。
type CreditCost struct {
	Version    int          `json:"version"`
	Dimensions []string     `json:"dimensions"`
	Prices     []PriceEntry `json:"prices"`
}

// ParseCreditCost 解析 credit_cost JSON。
func ParseCreditCost(raw datatypes.JSON) (CreditCost, error) {
	var cost CreditCost
	if len(raw) == 0 {
		return cost, nil
	}
	if err := json.Unmarshal(raw, &cost); err != nil {
		return cost, fmt.Errorf("credit_cost 解析失败: %w", err)
	}
	return cost, nil
}

// ValidateCreditCost 校验价格矩阵：维度必须是 constraints 键的子集，
// 笛卡尔积每个组合有且只有一个价格，不允许负价格。
func ValidateCreditCost(cost CreditCost, constraints ModelConstraints) error {
	if cost.Version < 1 {
		return errors.New("价格矩阵缺少版本号")
	}
	// 文本/对话这类按次计价的模型没有参数维度，允许 dimensions 为空、只配一条价格。
	if len(cost.Dimensions) == 0 {
		if len(cost.Prices) != 1 || cost.Prices[0].CostMicros < 0 {
			return errors.New("无维度价格矩阵必须且只能有一条非负价格")
		}
		return nil
	}
	valuesByDim := map[string][]string{}
	for _, dim := range cost.Dimensions {
		values := constraints.OptionValues(dim)
		if len(values) == 0 {
			return fmt.Errorf("计价维度 %s 不是该模型 constraints 的子集", dim)
		}
		valuesByDim[dim] = values
	}
	expected := 1
	for _, dim := range cost.Dimensions {
		expected *= len(valuesByDim[dim])
	}
	seen := map[string]bool{}
	for _, price := range cost.Prices {
		if price.CostMicros < 0 {
			return errors.New("价格不能为负数")
		}
		if len(price.Params) != len(cost.Dimensions) {
			return fmt.Errorf("价格组合 %v 的维度数量与 dimensions 不一致", price.Params)
		}
		parts := make([]string, 0, len(cost.Dimensions))
		for _, dim := range cost.Dimensions {
			value, ok := price.Params[dim]
			if !ok {
				return fmt.Errorf("价格组合缺少维度 %s", dim)
			}
			if !contains(valuesByDim[dim], value) {
				return fmt.Errorf("价格组合包含 constraints 外的取值 %s=%s", dim, value)
			}
			parts = append(parts, value)
		}
		key := joinParts(parts)
		if seen[key] {
			return fmt.Errorf("价格组合重复: %s", key)
		}
		seen[key] = true
	}
	if len(seen) != expected {
		return fmt.Errorf("价格矩阵不完整：应有 %d 个组合，实际 %d 个", expected, len(seen))
	}
	return nil
}

// joinParts 把各维取值排序后用 \x00 拼成组合键：\x00 不会出现在参数值里，
// 排序保证键序无关，用作价格组合去重与排序的稳定键。
func joinParts(parts []string) string {
	sorted := append([]string(nil), parts...)
	sort.Strings(sorted)
	out := ""
	for _, part := range sorted {
		out += "\x00" + part
	}
	return out
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// EffectivePrice 是折扣解析后的单条报价。
type EffectivePrice struct {
	Params          map[string]string `json:"params"`
	BaseCostMicros  int64             `json:"baseCostMicros"`
	FinalCostMicros int64             `json:"finalCostMicros"`
	Discount        *AppliedDiscount  `json:"discount"`
}

type AppliedDiscount struct {
	PromotionID string    `json:"promotionId"`
	Name        string    `json:"name"`
	DiscountBPS int       `json:"discountBps"`
	EndsAt      time.Time `json:"endsAt"`
}

// ResolveMatch 按「先比具体度、再比优先级」选出一条生效活动。
// 多条活动同时命中时不叠加折扣，只取一条。
func ResolveMatch(promotions []model.ModelPricePromotion, params map[string]string, now time.Time, enabled bool) *model.ModelPricePromotion {
	if !enabled {
		return nil
	}
	var best *model.ModelPricePromotion
	bestSpecificity := -1
	for i := range promotions {
		promo := &promotions[i]
		if !promotionActive(promo, now) {
			continue
		}
		match, ok := matchParams(promo.MatchParams, params)
		if !ok {
			continue
		}
		specificity := len(match)
		if best == nil || specificity > bestSpecificity ||
			(specificity == bestSpecificity && promo.Priority > best.Priority) {
			best = promo
			bestSpecificity = specificity
		}
	}
	return best
}

func promotionActive(promo *model.ModelPricePromotion, now time.Time) bool {
	if promo.Status == "disabled" || promo.Status == "ended" || promo.Status == "draft" {
		return false
	}
	return !now.Before(promo.StartsAt) && now.Before(promo.EndsAt)
}

func matchParams(raw datatypes.JSON, params map[string]string) (map[string]string, bool) {
	var match map[string]string
	if len(raw) == 0 {
		return map[string]string{}, true
	}
	if err := json.Unmarshal(raw, &match); err != nil {
		return nil, false
	}
	for key, expected := range match {
		if params[key] != expected {
			return nil, false
		}
	}
	return match, true
}

// ApplyDiscount 计算折后价，最终微元向上取整。
func ApplyDiscount(baseCostMicros int64, bps int) int64 {
	if bps <= 0 || bps >= 10000 {
		return baseCostMicros
	}
	return int64(math.Ceil(float64(baseCostMicros) * float64(bps) / 10000))
}
