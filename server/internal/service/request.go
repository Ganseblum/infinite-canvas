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
	"github.com/infinite-canvas/server/internal/platform/billing"
)

// ErrFreeTrialTaken 表示报价后免费额度已被并发请求占用（billing.ConsumeFreeTrial
// 返回 false），调用方映射为 QUOTE_STALE。
var ErrFreeTrialTaken = errors.New("免费额度已被占用")

// ReserveResult 是一次预扣或免费额度占用的结果。
type ReserveResult struct {
	UsedFreeTrial  bool
	ConsumedMicros int64
	Transactions   []model.CreditTransaction
}

// trialMetricFor 把能力映射到免费试用计数指标。
func trialMetricFor(capability string) string {
	switch capability {
	case "image":
		return billing.MetricFreeImageTrial
	case "video":
		return billing.MetricFreeVideoTrial
	default:
		return ""
	}
}

// AIRequestService 管理生成请求的幂等、快照与失败收敛。
type AIRequestService struct {
	db      *gorm.DB
	billing *billing.Service
}

func NewAIRequestService(db *gorm.DB) *AIRequestService {
	return &AIRequestService{db: db, billing: billing.NewService(db, model.ProductCanvas)}
}

// CreateOrGet 依据幂等 key 建请求行。冲突时返回既有请求与 false，由调用方决定返回结果还是提示进行中。
// 事务内调用时传 tx，保证请求行与预扣同事务；传 nil 时走服务自身连接。
func (s *AIRequestService) CreateOrGet(tx *gorm.DB, request *model.AIRequest) (*model.AIRequest, bool, error) {
	if tx == nil {
		tx = s.db
	}
	if request.IdempotencyKey == nil {
		if err := tx.Create(request).Error; err != nil {
			return nil, false, err
		}
		return request, true, nil
	}
	err := tx.Create(request).Error
	if err == nil {
		return request, true, nil
	}
	if !billing.IsDuplicateKey(err) {
		return nil, false, err
	}
	var existing model.AIRequest
	if err := tx.Where("user_id = ? AND idempotency_key = ?", request.UserID, *request.IdempotencyKey).First(&existing).Error; err != nil {
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

// MarkFailed 收敛请求为失败；需要退还时按请求 id（bizKey）退全部未退消费流水。
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

// Refund 按请求 id 定位该次预扣的全部消费流水退款（billing 域按 ref_id 退，
// refund_of_transaction_id 唯一索引保证重复退还只生效一次）。
// 免费试用请求没有点数流水：按同一计数器条件递减一次（差异清单 #124，
// 失败不应永久消耗试用——点数退了，试用也要退）。
func (s *AIRequestService) Refund(ctx context.Context, request *model.AIRequest) error {
	if request.UsedFreeTrial {
		return s.refundFreeTrial(ctx, request)
	}
	if err := s.billing.Refund(s.db, request.UserID, request.ID.String()); err != nil {
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
	if err := s.billing.RefundFreeTrial(s.db, request.UserID, metric); err != nil {
		slog.Error("生成失败退还免费试用失败", "request", request.ID, "err", err)
		_ = s.db.Model(&model.AIRequest{}).Where("id = ?", request.ID).Update("refund_pending", true).Error
		return err
	}
	return s.db.Model(&model.AIRequest{}).Where("id = ?", request.ID).Update("refund_pending", false).Error
}

// MarkConsumeTransactions 把预扣产生的消费流水 id 写入请求行，供审计追溯。
func (s *AIRequestService) MarkConsumeTransactions(tx *gorm.DB, requestID uuid.UUID, transactions []model.CreditTransaction) error {
	if len(transactions) == 0 {
		return nil
	}
	if tx == nil {
		tx = s.db
	}
	ids := make([]uuid.UUID, 0, len(transactions))
	for _, transaction := range transactions {
		ids = append(ids, transaction.ID)
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return tx.Model(&model.AIRequest{}).Where("id = ?", requestID).Update("consume_transaction_ids", raw).Error
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
