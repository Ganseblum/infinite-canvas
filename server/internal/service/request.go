package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
)

// ErrFreeTrialTaken 表示报价后免费额度已被并发请求占用，调用方映射为 QUOTE_STALE。
var ErrFreeTrialTaken = errors.New("免费额度已被占用")

// ReserveResult 是一次预扣或免费额度占用的结果。
type ReserveResult struct {
	UsedFreeTrial  bool
	ConsumedMicros int64
	Transactions   []model.CreditTransaction
}

// ConsumeFreeTrial 原子占用一次免费额度。
// 计数用「条件自增 + 判 RowsAffected」实现：先查后加在并发下会全部读到未满
// 再各自 +1（差异清单 #32，真环境实测超发）。计数只增不减，删除生成历史不返还；
// 生成失败的退还走 RefundFreeTrial。
func (s *QuotaService) ConsumeFreeTrial(tx *gorm.DB, userID uuid.UUID, metric string) error {
	limit := freeTrialLimit(metric)
	if limit == 0 {
		return errors.New("未知的免费额度类型")
	}
	updatedAt := time.Now()
	if err := s.ensureUsageRow(tx, userID, metric, updatedAt); err != nil {
		return err
	}
	res := tx.Exec(
		`UPDATE usage_records SET value = value + 1, updated_at = ?
		 WHERE user_id = ? AND metric = ? AND period = ? AND value < ?`,
		updatedAt, userID, metric, PeriodTotal, limit,
	)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrFreeTrialTaken
	}
	return nil
}

func freeTrialLimit(metric string) int64 {
	switch metric {
	case MetricFreeImageTrial:
		return FreeImageTrialLimit
	case MetricFreeVideoTrial:
		return FreeVideoTrialLimit
	default:
		return 0
	}
}

// ensureUsageRow 保证计数行存在（0 值幂等写入），MySQL 与 SQLite 各用一条 upsert。
func (s *QuotaService) ensureUsageRow(tx *gorm.DB, userID uuid.UUID, metric string, updatedAt time.Time) error {
	if err := tx.Exec(
		`INSERT INTO usage_records (user_id, metric, period, value, updated_at) VALUES (?, ?, ?, 0, ?)
		 ON DUPLICATE KEY UPDATE value = value`,
		userID, metric, PeriodTotal, updatedAt,
	).Error; err == nil {
		return nil
	}
	return tx.Exec(
		`INSERT INTO usage_records (user_id, metric, period, value, updated_at) VALUES (?, ?, ?, 0, ?)
		 ON CONFLICT(user_id, metric, period) DO UPDATE SET value = value`,
		userID, metric, PeriodTotal, updatedAt,
	).Error
}

// Reserve 按报价预扣点数：先扣 granted 再扣 purchased，返回消费流水供失败退还。
func (s *CreditService) ReserveForQuote(ctx context.Context, userID uuid.UUID, amountMicros int64, refID, note string) ([]model.CreditTransaction, error) {
	return s.Reserve(ctx, userID, amountMicros, refID, note)
}

// AIRequestService 管理生成请求的幂等、快照与失败收敛。
type AIRequestService struct {
	db      *gorm.DB
	credits *CreditService
	quota   *QuotaService
}

func NewAIRequestService(db *gorm.DB) *AIRequestService {
	return &AIRequestService{db: db, credits: NewCreditService(db), quota: NewQuotaService(db)}
}

// CreateOrGet 依据幂等 key 建请求行。冲突时返回既有请求与 false，由调用方决定返回结果还是提示进行中。
func (s *AIRequestService) CreateOrGet(request *model.AIRequest) (*model.AIRequest, bool, error) {
	if request.IdempotencyKey == nil {
		if err := s.db.Create(request).Error; err != nil {
			return nil, false, err
		}
		return request, true, nil
	}
	err := s.db.Create(request).Error
	if err == nil {
		return request, true, nil
	}
	if !IsDuplicateKey(err) {
		return nil, false, err
	}
	var existing model.AIRequest
	if err := s.db.Where("user_id = ? AND idempotency_key = ?", request.UserID, *request.IdempotencyKey).First(&existing).Error; err != nil {
		return nil, false, err
	}
	return &existing, false, nil
}

// MarkSucceeded 收敛请求为成功并记录耗时。事务内调用时传 tx，避免在单连接库上自锁。
func (s *AIRequestService) MarkSucceeded(tx *gorm.DB, requestID uuid.UUID, durationMs int) error {
	if tx == nil {
		tx = s.db
	}
	return tx.Model(&model.AIRequest{}).Where("id = ?", requestID).Updates(map[string]any{
		"status":      "succeeded",
		"duration_ms": durationMs,
	}).Error
}

// MarkFailed 收敛请求为失败；需要退还时按原消费流水逐条退回。
// 状态收敛是条件更新：只在 running → failed 执行一次，并发收敛/多实例不会重复退款。
func (s *AIRequestService) MarkFailed(ctx context.Context, request *model.AIRequest, upstreamStatus int) error {
	updates := map[string]any{"status": "failed"}
	if upstreamStatus > 0 {
		updates["upstream_status"] = upstreamStatus
	}
	res := s.db.Model(&model.AIRequest{}).Where("id = ? AND status = ?", request.ID, "running").Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	return s.Refund(ctx, request)
}

// Refund 按请求里记录的消费流水退款。唯一索引保证重复退还只生效一次。
// 免费试用请求没有点数流水：按同一计数器条件递减一次（差异清单 #124，
// 失败不应永久消耗试用——点数退了，试用也要退）。
func (s *AIRequestService) Refund(ctx context.Context, request *model.AIRequest) error {
	if request.UsedFreeTrial {
		return s.refundFreeTrial(ctx, request)
	}
	consumeIDs := s.consumeIDs(request)
	if len(consumeIDs) == 0 {
		return nil
	}
	if err := s.credits.Refund(ctx, request.UserID, consumeIDs, "生成失败退还"); err != nil {
		// 退还失败先记为待处理，由管理后台人工重试，不引入定时任务。
		slog.Error("生成失败退还点数失败", "request", request.ID, "err", err)
		_ = s.db.Model(&model.AIRequest{}).Where("id = ?", request.ID).Update("refund_pending", true).Error
		return err
	}
	return s.db.Model(&model.AIRequest{}).Where("id = ?", request.ID).Update("refund_pending", false).Error
}

func (s *AIRequestService) refundFreeTrial(ctx context.Context, request *model.AIRequest) error {
	metric := trialMetricFor(request.Capability)
	if metric == "" {
		return nil
	}
	if err := s.quota.RefundFreeTrial(s.db, request.UserID, metric); err != nil {
		slog.Error("生成失败退还免费试用失败", "request", request.ID, "err", err)
		_ = s.db.Model(&model.AIRequest{}).Where("id = ?", request.ID).Update("refund_pending", true).Error
		return err
	}
	return s.db.Model(&model.AIRequest{}).Where("id = ?", request.ID).Update("refund_pending", false).Error
}

func trialMetricFor(capability string) string {
	switch capability {
	case "image":
		return MetricFreeImageTrial
	case "video":
		return MetricFreeVideoTrial
	default:
		return ""
	}
}

func (s *AIRequestService) consumeIDs(request *model.AIRequest) []uuid.UUID {
	if len(request.ConsumeTransactionIDs) == 0 {
		return nil
	}
	var ids []uuid.UUID
	if err := json.Unmarshal(request.ConsumeTransactionIDs, &ids); err != nil {
		return nil
	}
	return ids
}

// MarkConsumeTransactions 把预扣产生的消费流水 id 写入请求行。
func (s *AIRequestService) MarkConsumeTransactions(requestID uuid.UUID, transactions []model.CreditTransaction) error {
	if len(transactions) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(transactions))
	for _, transaction := range transactions {
		ids = append(ids, transaction.ID)
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return s.db.Model(&model.AIRequest{}).Where("id = ?", requestID).Update("consume_transaction_ids", raw).Error
}

// ConvergeStaleRunning 在服务启动时收敛因进程重启滞留的 running 请求：
// 超过「整体超时的两倍」即置为 failed 并按需退还，退款靠唯一索引幂等。
func (s *AIRequestService) ConvergeStaleRunning(ctx context.Context, timeoutFor func(string) time.Duration, now time.Time) (int, error) {
	var requests []model.AIRequest
	if err := s.db.WithContext(ctx).Where("status = ?", "running").Limit(500).Find(&requests).Error; err != nil {
		return 0, err
	}
	converged := 0
	for i := range requests {
		request := &requests[i]
		timeout := timeoutFor(request.Capability)
		if timeout <= 0 {
			timeout = 3 * time.Minute
		}
		if request.UpdatedAt.Add(2 * timeout).After(now) {
			continue
		}
		if err := s.MarkFailed(ctx, request, 0); err != nil {
			slog.Error("收敛滞留请求失败", "request", request.ID, "err", err)
			continue
		}
		converged++
	}
	return converged, nil
}

// RetryPendingRefunds 重试所有 refund_pending 的请求，供管理后台人工触发。
func (s *AIRequestService) RetryPendingRefunds(ctx context.Context) (int, error) {
	var requests []model.AIRequest
	if err := s.db.WithContext(ctx).Where("refund_pending = ?", true).Limit(200).Find(&requests).Error; err != nil {
		return 0, err
	}
	retried := 0
	for i := range requests {
		if err := s.Refund(ctx, &requests[i]); err == nil {
			retried++
		}
	}
	return retried, nil
}
