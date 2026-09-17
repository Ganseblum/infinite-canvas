package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/infinite-canvas/server/internal/model"
)

func intPtr(value int) *int { return &value }

// TestValidateParamsNConstraint 覆盖 constraints.n.max 生效与无约束两种配置。
func TestValidateParamsNConstraint(t *testing.T) {
	capped := ModelConstraints{N: &IntConstraint{Max: 20}}
	cases := []struct {
		name        string
		constraints ModelConstraints
		n           *int
		wantErr     bool
		wantAllowed string
	}{
		{"目录允许 20 时 20 通过", capped, intPtr(20), false, ""},
		{"超过目录上限被拒绝", capped, intPtr(21), true, "1-20"},
		{"目录上限较小时同样生效", ModelConstraints{N: &IntConstraint{Max: 4}}, intPtr(5), true, "1-4"},
		{"上限为 0 视为无约束", ModelConstraints{N: &IntConstraint{Max: 0}}, intPtr(20), false, ""},
		{"无 n 约束时不设上限", ModelConstraints{}, intPtr(20), false, ""},
		{"无 n 约束时超过原 15 条硬上限也通过", ModelConstraints{}, intPtr(100), false, ""},
		{"n 缺省时不做上限校验", ModelConstraints{N: &IntConstraint{Max: 4}}, nil, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateParams(tc.constraints, QuoteParams{}, tc.n)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("不应报错, got %v", err)
				}
				return
			}
			var paramErr *ParamNotSupportedError
			if !errors.As(err, &paramErr) {
				t.Fatalf("应返回 ParamNotSupportedError, got %v", err)
			}
			if paramErr.Param != "n" {
				t.Fatalf("Param = %q, want n", paramErr.Param)
			}
			if len(paramErr.Allowed) != 1 || paramErr.Allowed[0] != tc.wantAllowed {
				t.Fatalf("Allowed = %v, want [%s]", paramErr.Allowed, tc.wantAllowed)
			}
		})
	}
}

// TestQuoteAcceptsNUpToConstraintMax 在报价层验证删掉 n>15 硬上限后的实际行为，
// 同时确认 n<=0 的下限归一仍然保留。
func TestQuoteAcceptsNUpToConstraintMax(t *testing.T) {
	g := newServiceDB(t)
	catalog := NewCatalogService(g, func() bool { return true })
	quotes := NewQuoteService(catalog, NewQuotaService(g), "quote-secret")
	user := createUserRow(t, g)

	item := model.ModelCatalog{
		ID: uuid.New(), Name: "batch-image", DisplayName: "批量图", Capability: "image", Provider: "openai",
		Constraints: []byte(`{"size":["1024x1024"],"n":{"max":20}}`),
		CreditCost:  []byte(`{"version":1,"dimensions":["size"],"prices":[{"params":{"size":"1024x1024"},"costMicros":100000}]}`),
		Enabled:     true,
	}
	if err := g.Create(&item).Error; err != nil {
		t.Fatalf("写入模型失败: %v", err)
	}
	now := time.Now()
	params := QuoteParams{"size": "1024x1024"}

	quote, err := quotes.BuildQuote(context.Background(), user.ID, item, "image", params, 20, now)
	if err != nil {
		t.Fatalf("目录允许 20 时不应拒绝: %v", err)
	}
	if quote.BaseCostMicros != 2000000 {
		t.Fatalf("20 张应按单价乘 20 计价, got %d", quote.BaseCostMicros)
	}

	if _, err := quotes.BuildQuote(context.Background(), user.ID, item, "image", params, 21, now); err == nil {
		t.Fatalf("超过 constraints.n.max 应被拒绝")
	} else {
		var paramErr *ParamNotSupportedError
		if !errors.As(err, &paramErr) || paramErr.Param != "n" {
			t.Fatalf("应返回 n 的 ParamNotSupportedError, got %v", err)
		}
	}

	// n<=0 的下限归一保留：报价按 n=1 签发，凭证也按 n=1 校验通过。
	lower, err := quotes.BuildQuote(context.Background(), user.ID, item, "image", params, 0, now)
	if err != nil {
		t.Fatalf("n=0 应按下限归一到 1: %v", err)
	}
	if lower.BaseCostMicros != 100000 {
		t.Fatalf("n=0 应归一到 1 张计价, got %d", lower.BaseCostMicros)
	}
	if _, err := quotes.VerifyQuote(context.Background(), user.ID, lower.Token, item, "image", params, 1, now); err != nil {
		t.Fatalf("n<=0 应归一到 1: %v", err)
	}
}
