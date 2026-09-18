package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/moderation"
	"github.com/infinite-canvas/server/internal/provider"
)

// AITaskService 负责视频任务的创建后生命周期：后台主动轮询、进程重启恢复与失败退还。
// 上游轮询不能等前端查询时代查：用户关掉页面后任务必须仍然跑完并落库。
type AITaskService struct {
	db         *gorm.DB
	upstream   *UpstreamService
	requests   *AIRequestService
	media      *MediaWriteService
	moderation *ModerationService
}

func NewAITaskService(db *gorm.DB, upstream *UpstreamService, media *MediaWriteService) *AITaskService {
	return &AITaskService{
		db:       db,
		upstream: upstream,
		requests: NewAIRequestService(db),
		media:    media,
	}
}

// SetModeration 注入审核服务：视频产物在落正式存储前先过帧审核。
func (s *AITaskService) SetModeration(moderation *ModerationService) { s.moderation = moderation }

const maxPollFailures = 5

// RecoverPending 在服务启动时把非终态任务重新挂上轮询。
func (s *AITaskService) RecoverPending(ctx context.Context) (int, error) {
	var tasks []model.AITask
	if err := s.db.WithContext(ctx).Where("status = ?", "pending").Find(&tasks).Error; err != nil {
		return 0, err
	}
	return len(tasks), nil
}

// PollPendingOnce 轮询一轮所有非终态任务，返回本轮处理的数量。
func (s *AITaskService) PollPendingOnce(ctx context.Context, now time.Time) (int, error) {
	var tasks []model.AITask
	if err := s.db.WithContext(ctx).Where("status = ?", "pending").Limit(100).Find(&tasks).Error; err != nil {
		return 0, err
	}
	handled := 0
	for i := range tasks {
		task := &tasks[i]
		if err := s.PollTask(ctx, task, now); err != nil {
			slog.Warn("视频任务轮询失败", "task", task.ID, "err", err)
		}
		handled++
	}
	s.cleanupTerminal(ctx, now)
	return handled, nil
}

// PollTask 查询一次任务状态并收敛终态。单次失败只累加计数，连续失败 5 次才算任务失败。
func (s *AITaskService) PollTask(ctx context.Context, task *model.AITask, now time.Time) error {
	if task.Status != "pending" {
		return nil
	}
	built, err := s.providerFor(task.Provider)
	if err != nil {
		// 渠道被停用等配置问题不应无限重试，直接判失败并退款。
		return s.failTask(ctx, task, err.Error())
	}
	pollCtx, cancel := context.WithTimeout(ctx, s.upstream.Timeouts().VideoPoll)
	defer cancel()
	state, err := built.PollVideo(pollCtx, provider.VideoTask{Provider: task.Provider, UpstreamTaskID: task.UpstreamTaskID})
	if err != nil {
		task.PollFailures++
		if task.PollFailures >= maxPollFailures {
			return s.failTask(ctx, task, "上游任务连续查询失败")
		}
		return s.db.Model(&model.AITask{}).Where("id = ?", task.ID).Update("poll_failures", task.PollFailures).Error
	}
	switch state.Status {
	case "succeeded":
		return s.succeedTask(ctx, task, state, now)
	case "failed":
		message := state.Error
		if message == "" {
			message = "视频生成失败"
		}
		return s.failTask(ctx, task, message)
	default:
		// 任务总时长上限：超过即判失败并退还。
		if now.After(task.CreatedAt.Add(s.upstream.Timeouts().VideoTask)) {
			return s.failTask(ctx, task, "视频任务超时")
		}
		return s.db.Model(&model.AITask{}).Where("id = ?", task.ID).Updates(map[string]any{
			"poll_failures": 0,
		}).Error
	}
}

func (s *AITaskService) succeedTask(ctx context.Context, task *model.AITask, state provider.VideoState, now time.Time) error {
	if state.Video == nil {
		return s.failTask(ctx, task, "上游未返回视频")
	}
	maxFile, err := s.maxFileBytes(task.UserID)
	if err != nil {
		return s.failTask(ctx, task, err.Error())
	}
	var data []byte
	mimeType := state.Video.MimeType
	if len(state.Video.Data) > 0 {
		data = state.Video.Data
	} else if state.Video.URL != "" {
		downloaded, contentType, err := s.upstream.Download(ctx, state.Video.URL, maxFile, 300*time.Second)
		if err != nil {
			return s.failTask(ctx, task, "视频结果下载失败")
		}
		data = downloaded
		if contentType != "" {
			mimeType = contentType
		}
	}
	if len(data) == 0 {
		return s.failTask(ctx, task, "上游未返回视频")
	}
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = "video/mp4"
	}
	// 视频产物审核：先落隔离区再抽帧送审，任一帧超阈值整条拒绝。
	// 隔离原件在被拒后保留 24 小时供人工复核释放，与图片链路同口径（差异清单 #8）。
	var videoQuarantineKey string
	var videoRecordID uuid.UUID
	videoModerationStatus := "skipped"
	if s.moderation != nil && s.moderation.Enabled() {
		quarantine, err := s.moderation.Quarantine().Put(ctx, task.UserID, data, mimeType)
		if err != nil {
			return err
		}
		videoQuarantineKey = quarantine.Key
		if err := s.db.Model(&model.AITask{}).Where("id = ?", task.ID).Update("status", "moderating").Error; err != nil {
			return err
		}
		verdict, err := moderateVideo(ctx, s.moderation, task.UserID, data, mimeType)
		decision := verdict.Decision
		if err != nil && !errors.Is(err, ErrContentRejected) && !errors.Is(err, ErrModerationUnavailable) {
			decision = moderation.DecisionError
		}
		if decision == "" {
			decision = moderation.DecisionError
		}
		// 最终结论写成绑定隔离原件的产物记录，管理端复核与释放以它为准；
		// 帧级记录仅作过程留痕。
		expiresAt := time.Now().Add(s.moderation.quarantine.ttl)
		record, recordErr := s.moderation.record(ctx, task.UserID, moderation.StageArtifact, moderation.ContentVideo,
			moderation.HashContent(data), moderation.Result{
				Decision:   decision,
				RiskLabels: verdict.RiskLabels,
			}, quarantine.Key, &expiresAt)
		if recordErr != nil {
			slog.Error("写入视频审核记录失败", "task", task.ID, "err", recordErr)
		} else if record != nil {
			videoRecordID = record.ID
			s.moderation.SetQuarantineBytes(ctx, record.ID, quarantine.Bytes)
		}
		switch {
		case errors.Is(err, ErrContentRejected):
			// 原件留在隔离区供人工复核，failTask 收敛生成记录与退款。
			return s.failTask(ctx, task, "视频内容未通过审核")
		case errors.Is(err, ErrModerationUnavailable):
			return s.failTask(ctx, task, "内容审核服务暂不可用")
		case err != nil:
			return s.failTask(ctx, task, "视频审核失败")
		}
		videoModerationStatus = decision
	}
	storageKey := "video:" + randomStorageKey()
	file, err := s.media.Save(ctx, SaveGeneratedMediaInput{
		UserID:     task.UserID,
		StorageKey: storageKey,
		MimeType:   mimeType,
		Data:       data,
		MaxBytes:   maxFile,
		Moderation: videoModerationStatus,
	})
	if err != nil {
		return s.failTask(ctx, task, "视频落盘失败")
	}
	if videoQuarantineKey != "" {
		// 已转正式存储：删除隔离原件并清空审核记录上的 key，避免管理端残留可预览项。
		_ = s.moderation.Quarantine().Delete(ctx, task.UserID, videoQuarantineKey)
		s.moderation.ClearQuarantineKey(ctx, videoRecordID)
	}
	if err != nil {
		return s.failTask(ctx, task, "视频落盘失败")
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		// 审核开启时任务会先被置为 moderating，收敛条件必须同时匹配两种中间态，
		// 否则条件更新匹配 0 行且不报错，任务永远停在 moderating（差异清单 #3）。
		if err := tx.Model(&model.AITask{}).Where("id = ? AND status IN ?", task.ID, []string{"pending", "moderating"}).Updates(map[string]any{
			"status":      "succeeded",
			"storage_key": file.StorageKey,
			"error":       "",
		}).Error; err != nil {
			return err
		}
		// 保留 pending 时写入的任务句柄，前端刷新后仍能恢复轮询与展示。
		existing := taskResult(context.Background(), tx, task.GenerationID)
		existing["video"] = map[string]any{
			"storageKey": file.StorageKey,
			"bytes":      file.Bytes,
			"mimeType":   file.MimeType,
		}
		result, _ := json.Marshal(existing)
		if err := tx.Model(&model.Generation{}).
			Where("id = ?", task.GenerationID).
			Updates(map[string]any{"status": "success", "result": result}).Error; err != nil {
			return err
		}
		return tx.Model(&model.AIRequest{}).Where("id = ?", task.RequestID).
			Update("status", "succeeded").Error
	})
	if err != nil {
		return err
	}
	task.Status = "succeeded"
	task.StorageKey = file.StorageKey
	return nil
}

// failTask 把任务置为终态、收敛生成记录与请求，并按需退还点数。
func (s *AITaskService) failTask(ctx context.Context, task *model.AITask, message string) error {
	request, err := s.requestByID(ctx, task.RequestID)
	if err != nil {
		return err
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		// 与成功收敛同口径：moderating 也要能落到 failed，退款与前端终态才成立。
		if err := tx.Model(&model.AITask{}).Where("id = ? AND status IN ?", task.ID, []string{"pending", "moderating"}).
			Updates(map[string]any{"status": "failed", "error": message}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Generation{}).
			Where("id = ?", task.GenerationID).
			Updates(map[string]any{"status": "failed"}).Error; err != nil {
			return err
		}
		return tx.Model(&model.AIRequest{}).Where("id = ? AND status = ?", task.RequestID, "running").
			Update("status", "failed").Error
	})
	if err != nil {
		return err
	}
	task.Status = "failed"
	task.Error = message
	// 退还走唯一索引幂等，重复收敛不会双退。
	if request != nil {
		if err := s.requests.Refund(ctx, request); err != nil {
			slog.Error("视频任务退款失败", "task", task.ID, "err", err)
		}
	}
	return nil
}

func (s *AITaskService) requestByID(ctx context.Context, id uuid.UUID) (*model.AIRequest, error) {
	var request model.AIRequest
	if err := s.db.WithContext(ctx).First(&request, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &request, nil
}

// providerFor 按任务里记录的 provider 名称找到对应格式的渠道。
func (s *AITaskService) providerFor(name string) (provider.Provider, error) {
	var channels []model.PlatformChannel
	if err := s.db.Where("enabled = ?", true).Find(&channels).Error; err != nil {
		return nil, err
	}
	for _, channel := range channels {
		if channel.APIFormat != name {
			continue
		}
		built, err := s.upstream.ProviderFor(channel)
		if err != nil {
			continue
		}
		return built, nil
	}
	return nil, fmt.Errorf("没有可用的 %s 渠道", name)
}

func (s *AITaskService) maxFileBytes(userID uuid.UUID) (int64, error) {
	_, plan, err := NewQuotaService(s.db).DerivePlan(context.Background(), userID, time.Now())
	if err != nil {
		return 0, err
	}
	return plan.MaxFileBytes, nil
}

// cleanupTerminal 清理超过 24 小时的终态任务，前端在此期间随时能查到结果。
func (s *AITaskService) cleanupTerminal(ctx context.Context, now time.Time) {
	_ = s.db.WithContext(ctx).
		Where("status <> ? AND updated_at < ?", "pending", now.Add(-24*time.Hour)).
		Delete(&model.AITask{}).Error
}

// TaskView 是任务查询接口的返回结构。
type TaskView struct {
	TaskID       string
	Status       string
	StorageKey   string
	MimeType     string
	Bytes        int64
	Error        string
	PollAfterMs  int
	GenerationID string
}

// ViewTask 读取任务详情，任务归属当前用户，查别人的任务返回 ErrOrderNotFound 等价的 404。
func (s *AITaskService) ViewTask(ctx context.Context, userID, taskID uuid.UUID) (*AITaskView, error) {
	var task model.AITask
	err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", taskID, userID).First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	var file model.MediaFile
	if task.StorageKey != "" {
		_ = s.db.WithContext(ctx).Where("user_id = ? AND storage_key = ?", userID, task.StorageKey).First(&file).Error
	}
	var request model.AIRequest
	_ = s.db.WithContext(ctx).First(&request, "id = ?", task.RequestID).Error
	view := &AITaskView{
		TaskID:          task.ID.String(),
		Status:          task.Status,
		StorageKey:      task.StorageKey,
		MimeType:        file.MimeType,
		Bytes:           file.Bytes,
		Error:           task.Error,
		PollAfterMs:     task.PollAfterMs(),
		GenerationID:    task.GenerationID.String(),
		BaseCostMicros:  request.BaseCostMicros,
		FinalCostMicros: request.FinalCostMicros,
		RefundedMicros:  refundedMicros(task, request),
	}
	if request.PromotionID != nil {
		view.PromotionID = request.PromotionID.String()
	}
	return view, nil
}

func refundedMicros(task model.AITask, request model.AIRequest) int64 {
	if task.Status == "failed" {
		return request.FinalCostMicros
	}
	return 0
}

// AITaskView 是任务查询的完整返回。
type AITaskView struct {
	TaskID          string
	Status          string
	StorageKey      string
	MimeType        string
	Bytes           int64
	Error           string
	PollAfterMs     int
	GenerationID    string
	BaseCostMicros  int64
	FinalCostMicros int64
	RefundedMicros  int64
	PromotionID     string
}

// ErrTaskNotFound 表示任务不存在或不属于当前用户。
var ErrTaskNotFound = errors.New("任务不存在")

// taskResult 读取生成记录里已有的 result，避免收敛时丢掉前端需要的字段。
func taskResult(ctx context.Context, db *gorm.DB, generationID uuid.UUID) map[string]any {
	result := map[string]any{}
	var generation model.Generation
	if err := db.WithContext(ctx).First(&generation, "id = ?", generationID).Error; err != nil {
		return result
	}
	if len(generation.Result) > 0 {
		_ = json.Unmarshal(generation.Result, &result)
	}
	return result
}

// randomStorageKey 生成产物对象 id，与媒体接口的 storageKey 规则一致。
func randomStorageKey() string {
	value := uuid.NewString()
	out := make([]byte, 0, 21)
	for i := 0; i < len(value) && len(out) < 21; i++ {
		if value[i] == '-' {
			continue
		}
		out = append(out, value[i])
	}
	return string(out)
}

// StartTaskPoller 启动后台轮询循环，启动时先恢复一次。
func (s *AITaskService) StartTaskPoller(ctx context.Context, interval time.Duration, onRecovered func(int)) {
	if count, err := s.RecoverPending(ctx); err == nil && count > 0 && onRecovered != nil {
		onRecovered(count)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.PollPendingOnce(ctx, time.Now()); err != nil {
				slog.Error("视频任务轮询失败", "err", err)
			}
		}
	}
}
