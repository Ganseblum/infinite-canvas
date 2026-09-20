package office

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/platform/billing"
)

// 计费 M1（brief 裁决）：仅 A5 预检 + 终态扣减两步；预估折点与 run 中检测是 M3。
// 费率待报批，起步口径 1 点/1000 输出 token，经配置可调，不在分支里硬编码。
const (
	// microsPerOutputToken 是每输出 token 折算的微元数（本平台 1 点 = 1e6 微元；
	// 1 点/1000 token ⇒ 每 token 1000 微元）。整数除法必须落在微元粒度，
	// 否则小额 run（几十 token）会被整除成 0 点——2026-09-20 联调实测踩坑。
	microsPerOutputToken int64 = 1000
)

// PrecheckCredits A5 预检（E5）：余额 ≤0 拒绝，返回是否放行。
func (s *Service) PrecheckCredits(ctx context.Context, userID uuid.UUID) error {
	account, err := s.billing.Balance(ctx, userID)
	if err != nil {
		// 账户行缺失按零余额拒绝比放行安全；其余查询错误同样拒绝并记日志。
		slog.Error("office 点数预检读取余额失败", "user_id", userID, "err", err)
		return errCreditsExhausted
	}
	if account.PurchasedMicros+account.GrantedMicros <= 0 {
		return errCreditsExhausted
	}
	return nil
}

// chargeMicros 按 done/终态 tokens 折算应扣微元数（微元粒度，避免整除吞零）。
func chargeMicros(tokensOut int64) int64 {
	if tokensOut <= 0 {
		return 0
	}
	return tokensOut * microsPerOutputToken
}

// chargeCredits 终态扣减：platform/billing 的流水没有以 run_id 为幂等键的唯一约束
// （credit_transactions.ref_id 无唯一索引），幂等闸放在 office 侧——先条件更新
// office_runs.credits_charged，更新命中才调 Reserve，保证恰好扣一次。
// 调用方在终态事务提交后调用；失败不回滚终态（E9：终态照落，扣减由系统补偿，本 M1 留 TODO）。
func (s *Service) chargeCredits(runID string, userID uuid.UUID, tokensOut int64) {
	micros := chargeMicros(tokensOut)
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&OfficeRun{}).
			Where("id = ? AND credits_charged = false", runID).
			Updates(map[string]any{"credits_charged": true, "credits": micros})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil // 已扣过，幂等返回
		}
		if micros <= 0 {
			return nil
		}
		_, err := s.billing.Reserve(tx, userID, micros, runID)
		return err
	})
	if errors.Is(err, billing.ErrInsufficientCredits) {
		// 余额不足不回滚终态：点数对账走人工/补偿（E9 TODO），run 结果保持不变。
		slog.Error("office 终态扣点余额不足，待补偿", "run_id", runID, "user_id", userID, "micros", micros)
		// TODO(E9): 定时补偿任务扫描 credits_charged=false 的终态 run 重试扣减（M1 未做，M3 收口）。
		return
	}
	if err != nil {
		slog.Error("office 终态扣点失败，待补偿", "run_id", runID, "user_id", userID, "err", err)
		// TODO(E9): 同上，重试 5×60s 后转人工。
	}
}
