package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/moderation"
)

// ErrContentRejected 表示内容未通过审核，调用方映射为 422 CONTENT_REJECTED。
var ErrContentRejected = errors.New("内容未通过审核")

// ErrModerationUnavailable 表示审核服务不可用且 fail mode 为 reject，映射为 503。
var ErrModerationUnavailable = errors.New("审核服务暂不可用")

// ModerationService 负责缓存、审核记录、fail mode 与告警。
// 四个触发点共用同一份实现，保证同一次内容在不同入口得到一致处理。
type ModerationService struct {
	db         *gorm.DB
	provider   moderation.Provider
	enabled    bool
	failMode   string
	policy     string
	timeout    time.Duration
	cacheTTL   time.Duration
	quarantine *QuarantineService

	mu    sync.Mutex
	cache map[string]cachedVerdict
}

type cachedVerdict struct {
	decision  string
	labels    []string
	expiresAt time.Time
}

type ModerationConfig struct {
	Enabled  bool
	FailMode string
	Policy   string
	Timeout  time.Duration
	CacheTTL time.Duration
}

func NewModerationService(db *gorm.DB, provider moderation.Provider, quarantine *QuarantineService, cfg ModerationConfig) *ModerationService {
	return &ModerationService{
		db:         db,
		provider:   provider,
		enabled:    cfg.Enabled,
		failMode:   cfg.FailMode,
		policy:     cfg.Policy,
		timeout:    cfg.Timeout,
		cacheTTL:   cfg.CacheTTL,
		quarantine: quarantine,
		cache:      map[string]cachedVerdict{},
	}
}

// Enabled 返回审核总开关状态。
func (s *ModerationService) Enabled() bool { return s.enabled && s.provider != nil }

// Quarantine 返回隔离区服务，供产物落盘与预览复用。
func (s *ModerationService) Quarantine() *QuarantineService { return s.quarantine }

// Verdict 是一次审核的最终结论。
type Verdict struct {
	Decision   string
	RiskLabels []string
	RecordID   uuid.UUID
	// Cached 表示命中通过缓存，未调用服务商。
	Cached bool
}

// Check 送审并根据 fail mode 给出结论。审核关闭时直接放行且不写记录。
func (s *ModerationService) Check(ctx context.Context, userID uuid.UUID, stage moderation.Stage, contentType moderation.ContentType, text string, data []byte, mimeType string) (Verdict, error) {
	if !s.Enabled() {
		return Verdict{Decision: moderation.DecisionPassed}, nil
	}
	hash := moderation.HashContent([]byte(text + string(data)))
	if decision, labels, ok := s.fromCache(userID, hash); ok {
		record, err := s.record(ctx, userID, stage, contentType, hash, moderation.Result{
			Decision:   decision,
			RiskLabels: labels,
			Summary:    map[string]any{"cached": true},
		}, "", nil)
		if err != nil {
			slog.Error("写入审核缓存记录失败", "err", err)
		}
		return Verdict{Decision: decision, RiskLabels: labels, RecordID: record.ID, Cached: true}, nil
	}

	checkCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	result, err := s.provider.Moderate(checkCtx, moderation.Request{
		Stage:       stage,
		ContentType: contentType,
		Text:        text,
		Data:        data,
		MimeType:    mimeType,
		ContentHash: hash,
	})
	if err != nil {
		return s.handleUnavailable(ctx, userID, stage, contentType, hash, err)
	}

	record, err := s.record(ctx, userID, stage, contentType, hash, result, "", nil)
	if err != nil {
		slog.Error("写入审核记录失败", "err", err)
	}
	s.cacheVerdict(userID, hash, result)
	if result.Decision == moderation.DecisionRejected {
		return Verdict{Decision: result.Decision, RiskLabels: result.RiskLabels, RecordID: record.ID}, ErrContentRejected
	}
	return Verdict{Decision: result.Decision, RiskLabels: result.RiskLabels, RecordID: record.ID}, nil
}

// CheckQuarantined 审核仍留在隔离区的内容并回填记录。产物与上传链路使用。
func (s *ModerationService) CheckQuarantined(ctx context.Context, userID uuid.UUID, stage moderation.Stage, contentType moderation.ContentType, quarantineKey string, data []byte, mimeType string) (Verdict, error) {
	if !s.Enabled() {
		if s.quarantine != nil && quarantineKey != "" {
			_ = s.quarantine.Delete(ctx, userID, quarantineKey)
		}
		return Verdict{Decision: moderation.DecisionPassed}, nil
	}
	hash := moderation.HashContent(data)
	checkCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	result, err := s.provider.Moderate(checkCtx, moderation.Request{
		Stage:       stage,
		ContentType: contentType,
		Data:        data,
		MimeType:    mimeType,
		ContentHash: hash,
	})
	if err != nil {
		return s.handleUnavailable(ctx, userID, stage, contentType, hash, err)
	}
	expiresAt := time.Now().Add(s.quarantine.ttl)
	record, recordErr := s.record(ctx, userID, stage, contentType, hash, result, quarantineKey, &expiresAt)
	if recordErr != nil {
		slog.Error("写入审核记录失败", "err", recordErr)
	}
	if result.Decision == moderation.DecisionRejected {
		// 拒绝时保留隔离原件 24 小时供人工复核，不提交正式存储。
		if recordErr != nil && s.quarantine != nil {
			_ = s.quarantine.Delete(ctx, userID, quarantineKey)
		}
		return Verdict{Decision: result.Decision, RiskLabels: result.RiskLabels, RecordID: record.ID}, ErrContentRejected
	}
	return Verdict{Decision: result.Decision, RiskLabels: result.RiskLabels, RecordID: record.ID}, nil
}

// CheckInput 是平台输入预审入口：文本与参考图一起送审，
// 任一被拒即返回 ErrContentRejected；服务故障按 fail mode 处理。
func (s *ModerationService) CheckInput(ctx context.Context, userID uuid.UUID, prompt string, references []ReferenceInput) (Verdict, error) {
	if !s.Enabled() {
		return Verdict{Decision: moderation.DecisionPassed}, nil
	}
	verdict, err := s.Check(ctx, userID, moderation.StagePrompt, moderation.ContentText, prompt, nil, "text/plain")
	if err != nil {
		return verdict, err
	}
	for _, reference := range references {
		verdict, err = s.Check(ctx, userID, moderation.StageReference, moderation.ContentType(reference.ContentType), "", reference.Data, reference.MimeType)
		if err != nil {
			return verdict, err
		}
	}
	return verdict, nil
}

// ReferenceInput 是待审核的参考素材。
type ReferenceInput struct {
	ContentType string
	MimeType    string
	Data        []byte
}

// CheckArtifact 是平台产物后审入口：内容已在隔离区，审核通过后由调用方提交正式存储。
func (s *ModerationService) CheckArtifact(ctx context.Context, userID uuid.UUID, contentType moderation.ContentType, quarantineKey string, data []byte, mimeType string) (Verdict, error) {
	if !s.Enabled() {
		return Verdict{Decision: moderation.DecisionPassed}, nil
	}
	return s.CheckQuarantined(ctx, userID, moderation.StageArtifact, contentType, quarantineKey, data, mimeType)
}

// CheckAudioArtifactPlaceholder 是音频产物的占位审核：当前没有音频审核模型，
// 与图片/视频链路一致先落隔离区并写产物记录（provider=none），结论按通过处理，
// 不阻塞音频发放；接入音频审核模型后把此方法替换为真实送审（差异清单 #29 占位）。
func (s *ModerationService) CheckAudioArtifactPlaceholder(ctx context.Context, userID uuid.UUID, quarantineKey string, data []byte, mimeType string) (Verdict, error) {
	if !s.Enabled() {
		if s.quarantine != nil && quarantineKey != "" {
			_ = s.quarantine.Delete(ctx, userID, quarantineKey)
		}
		return Verdict{Decision: moderation.DecisionPassed}, nil
	}
	expiresAt := time.Now().Add(s.quarantine.ttl)
	record, err := s.record(ctx, userID, moderation.StageArtifact, moderation.ContentAudio,
		moderation.HashContent(data), moderation.Result{
			Decision: moderation.DecisionPassed,
			Summary:  map[string]any{"provider": "none", "note": "音频审核占位：暂无音频审核模型，产物经隔离区转正"},
		}, quarantineKey, &expiresAt)
	if err != nil {
		slog.Error("写入音频占位审核记录失败", "err", err)
	}
	var recordID uuid.UUID
	if record != nil {
		recordID = record.ID
	}
	return Verdict{Decision: moderation.DecisionPassed, RecordID: recordID}, nil
}

// handleUnavailable 按 fail mode 处理审核服务故障。
func (s *ModerationService) handleUnavailable(ctx context.Context, userID uuid.UUID, stage moderation.Stage, contentType moderation.ContentType, hash string, err error) (Verdict, error) {
	slog.Error("审核服务不可用", "stage", stage, "failMode", s.failMode, "err", err)
	record, recordErr := s.record(ctx, userID, stage, contentType, hash, moderation.Result{
		Decision: moderation.DecisionError,
		Summary:  map[string]any{"error": err.Error()},
	}, "", nil)
	if recordErr != nil {
		slog.Error("写入审核故障记录失败", "err", recordErr)
	}
	if s.failMode == "allow" {
		alert("moderation_unavailable_allowed", map[string]any{"stage": string(stage), "err": err.Error()})
		return Verdict{Decision: moderation.DecisionError, RecordID: record.ID}, nil
	}
	alert("moderation_unavailable_rejected", map[string]any{"stage": string(stage), "err": err.Error()})
	return Verdict{Decision: moderation.DecisionError, RecordID: record.ID}, ErrModerationUnavailable
}

func (s *ModerationService) fromCache(userID uuid.UUID, hash string) (string, []string, bool) {
	if s.cacheTTL <= 0 {
		return "", nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.cache[userID.String()+"\x00"+hash]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(s.cache, userID.String()+"\x00"+hash)
		}
		return "", nil, false
	}
	return entry.decision, entry.labels, true
}

func (s *ModerationService) cacheVerdict(userID uuid.UUID, hash string, result moderation.Result) {
	if s.cacheTTL <= 0 {
		return
	}
	// 只缓存通过结论：拒绝结论可能随策略调整变化，保守处理。
	if result.Decision != moderation.DecisionPassed {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[userID.String()+"\x00"+hash] = cachedVerdict{
		decision:  result.Decision,
		labels:    result.RiskLabels,
		expiresAt: time.Now().Add(s.cacheTTL),
	}
}

// ClearQuarantineKey 在隔离原件已经提交或删除后清空记录上的 key，
// 避免清理任务对不存在的对象重复操作。
func (s *ModerationService) ClearQuarantineKey(ctx context.Context, recordID uuid.UUID) {
	if recordID == uuid.Nil {
		return
	}
	if err := s.db.WithContext(ctx).Model(&model.ModerationRecord{}).Where("id = ?", recordID).
		Updates(map[string]any{"quarantine_key": "", "quarantine_bytes": 0}).Error; err != nil {
		slog.Error("清空隔离 key 失败", "record", recordID, "err", err)
	}
}

// SetQuarantineBytes 回填隔离原件的加密字节数，供隔离区容量统计使用
// （差异清单 #25：原先用 SUM(LENGTH(quarantine_key)) 估字节数，恒为「条数 × 键长」）。
func (s *ModerationService) SetQuarantineBytes(ctx context.Context, recordID uuid.UUID, bytes int64) {
	if recordID == uuid.Nil || bytes <= 0 {
		return
	}
	if err := s.db.WithContext(ctx).Model(&model.ModerationRecord{}).Where("id = ?", recordID).
		Update("quarantine_bytes", bytes).Error; err != nil {
		slog.Error("回填隔离字节数失败", "record", recordID, "err", err)
	}
}

// InvalidateCache 策略版本变化时清空缓存。
func (s *ModerationService) InvalidateCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = map[string]cachedVerdict{}
}

// record 写入一条审核记录。审核记录只追加服务商事实，人工改判通过复核字段表达。
func (s *ModerationService) record(ctx context.Context, userID uuid.UUID, stage moderation.Stage, contentType moderation.ContentType, hash string, result moderation.Result, quarantineKey string, expiresAt *time.Time) (*model.ModerationRecord, error) {
	labels, _ := json.Marshal(result.RiskLabels)
	if labels == nil {
		labels = []byte("[]")
	}
	summary, _ := json.Marshal(result.Summary)
	if summary == nil {
		summary = []byte("{}")
	}
	reviewStatus := "not_required"
	if result.Decision == moderation.DecisionRejected {
		reviewStatus = "pending"
	}
	record := &model.ModerationRecord{
		ID:                  uuid.New(),
		UserID:              userID,
		Stage:               string(stage),
		ContentType:         string(contentType),
		ContentHash:         hash,
		Provider:            s.providerName(),
		ProviderRequestID:   result.ProviderRequestID,
		PolicyVersion:       s.policy,
		ProviderResult:      summary,
		Decision:            result.Decision,
		RiskLabels:          labels,
		QuarantineKey:       quarantineKey,
		QuarantineExpiresAt: expiresAt,
		ReviewStatus:        reviewStatus,
	}
	if err := s.db.WithContext(ctx).Create(record).Error; err != nil {
		return record, err
	}
	return record, nil
}

func (s *ModerationService) providerName() string {
	if s.provider == nil {
		return "none"
	}
	return s.provider.Name()
}

// alert 输出结构化告警日志，供运维按关键字接告警规则。
func alert(event string, fields map[string]any) {
	args := []any{"event", event}
	for key, value := range fields {
		args = append(args, key, value)
	}
	slog.Error("MODERATION_ALERT", args...)
}

// ModerationStats 是管理后台的审核统计。
type ModerationStats struct {
	Total           int64            `json:"total"`
	Passed          int64            `json:"passed"`
	Rejected        int64            `json:"rejected"`
	Errors          int64            `json:"errors"`
	PendingReview   int64            `json:"pendingReview"`
	RejectedRate    float64          `json:"rejectedRate"`
	ErrorRate       float64          `json:"errorRate"`
	LabelCounts     map[string]int64 `json:"labelCounts"`
	ReviewRate      float64          `json:"reviewRate"`
	QuarantineCount int64            `json:"quarantineCount"`
}

// Stats 汇总命中率、失败率、标签分布、复核积压与改判率。
func (s *ModerationService) Stats(ctx context.Context) (ModerationStats, error) {
	stats := ModerationStats{LabelCounts: map[string]int64{}}
	if err := s.db.WithContext(ctx).Model(&model.ModerationRecord{}).Count(&stats.Total).Error; err != nil {
		return stats, err
	}
	s.db.WithContext(ctx).Model(&model.ModerationRecord{}).Where("decision = ?", moderation.DecisionPassed).Count(&stats.Passed)
	s.db.WithContext(ctx).Model(&model.ModerationRecord{}).Where("decision = ?", moderation.DecisionRejected).Count(&stats.Rejected)
	s.db.WithContext(ctx).Model(&model.ModerationRecord{}).Where("decision = ?", moderation.DecisionError).Count(&stats.Errors)
	s.db.WithContext(ctx).Model(&model.ModerationRecord{}).Where("review_status = ?", "pending").Count(&stats.PendingReview)
	if stats.Total > 0 {
		stats.RejectedRate = float64(stats.Rejected) / float64(stats.Total)
		stats.ErrorRate = float64(stats.Errors) / float64(stats.Total)
	}
	var records []model.ModerationRecord
	s.db.WithContext(ctx).Model(&model.ModerationRecord{}).Where("decision = ?", moderation.DecisionRejected).Limit(500).Find(&records)
	var approved int64
	for _, record := range records {
		var labels []string
		if err := json.Unmarshal(record.RiskLabels, &labels); err == nil {
			for _, label := range labels {
				stats.LabelCounts[label]++
			}
		}
		if record.ReviewStatus == "approved" {
			approved++
		}
	}
	if stats.Rejected > 0 {
		stats.ReviewRate = float64(approved) / float64(stats.Rejected)
	}
	if s.quarantine != nil {
		count, _, err := s.quarantine.Stats(ctx, time.Now())
		if err == nil {
			stats.QuarantineCount = count
		}
	}
	return stats, nil
}
