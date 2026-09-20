// Package ai 是 AI 生成与媒体域：报价、图像/语音/视频/对话四类生成接口与媒体读写下发。
// 各 handler 按固定顺序串起校验、报价、审核、并发槽位、预扣与退还；上游协议差异由
// internal/provider 收敛，本域只面对统一的请求与结果。
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/httpx"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/moderation"
	"github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/platform/membership"
	platformstorage "github.com/infinite-canvas/server/internal/platform/storage"
	"github.com/infinite-canvas/server/internal/provider"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/storage"
	"github.com/infinite-canvas/server/internal/watermark"
)

// AIHandler 是报价与全部生成/查询接口的入口。
// 顺序固定为：鉴权 → 模型和参数校验 → 报价凭证校验 → 邮箱与存储预检 → 并发槽位 →
// 预扣或占用免费额度 → 调用上游 → 落盘与写生成记录 → 失败退还。
type AIHandler struct {
	db         *gorm.DB
	catalog    *service.CatalogService
	quotes     *service.QuoteService
	requests   *service.AIRequestService
	media      *service.MediaWriteService
	upstream   *service.UpstreamService
	store      storage.Storage
	slots      *concurrencySlots
	tasks      *service.AITaskService
	moderation *service.ModerationService
	billing    *billing.Service
	membership *membership.Service
	usage      *platformstorage.Service
	appURL     string
	wm         imageWatermarker // 生成落盘水印挂钩（T5），main.go 注入 *watermark.Service
	wmEnabled  func() bool      // 水印总开关，生产恒为 watermark.Enabled
}

// imageWatermarker 是生成落盘挂钩对图片水印能力的最小依赖：生产注入 *watermark.Service，
// 测试注入可报错的替身，用于锁死 fail-closed 语义。
type imageWatermarker interface {
	Image(src []byte, srcMime string) (out []byte, outMime string, err error)
}

func NewAIHandler(db *gorm.DB, catalog *service.CatalogService, quotes *service.QuoteService, upstream *service.UpstreamService, store storage.Storage, appURL string, moderation *service.ModerationService) *AIHandler {
	media := service.NewMediaWriteService(db, store)
	return &AIHandler{
		db:         db,
		catalog:    catalog,
		quotes:     quotes,
		requests:   service.NewAIRequestService(db),
		media:      media,
		upstream:   upstream,
		store:      store,
		slots:      newConcurrencySlots(),
		tasks:      service.NewAITaskService(db, upstream, media),
		moderation: moderation,
		billing:    billing.NewService(db, model.ProductCanvas),
		membership: membership.NewService(db),
		usage:      platformstorage.NewService(db, model.ProductCanvas),
		appURL:     appURL,
		wmEnabled:  watermark.Enabled,
	}
}

// SetWatermark 注入生成落盘水印挂钩（T5）。enabled 传 watermark.Enabled，
// 测试可替换以分别覆盖开关两态；未注入 wm 或开关为关时整段跳过，行为与现状一致。
func (h *AIHandler) SetWatermark(wm imageWatermarker, enabled func() bool) {
	if enabled == nil {
		enabled = watermark.Enabled
	}
	h.wm = wm
	h.wmEnabled = enabled
}

// Slots 供健康检查与测试观察并发占用。
func (h *AIHandler) Slots() *concurrencySlots { return h.slots }

// ===== 报价 =====

type quoteRequest struct {
	Model      string         `json:"model"`
	Capability string         `json:"capability"`
	Params     map[string]any `json:"params"`
}

// Quote 生成一次报价并附带余额与缺口信息，只读不落库；报价凭证由后续生成接口携带校验。
func (h *AIHandler) Quote(c *gin.Context) {
	user, ok := h.currentUser(c)
	if !ok {
		return
	}
	var req quoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	catalogItem, ok := h.loadModel(c, req.Model, req.Capability)
	if !ok {
		return
	}
	params := stringifyParams(req.Params)
	n := parseIntDefault(req.Params["n"], 1)
	quote, err := h.quotes.BuildQuote(c.Request.Context(), user.ID, catalogItem, req.Capability, params, n, time.Now())
	if err != nil {
		h.abortQuoteError(c, err)
		return
	}

	available, err := h.availableMicros(user.ID)
	if err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	shortfall := quote.FinalCostMicros - available
	if shortfall < 0 {
		shortfall = 0
	}
	payload := gin.H{
		"billingMode":      quote.BillingMode,
		"baseCostMicros":   quote.BaseCostMicros,
		"discount":         quote.Discount,
		"finalCostMicros":  quote.FinalCostMicros,
		"finalCostPoints":  quote.FinalCostMicros,
		"finalCostYuan":    microsToYuan(quote.FinalCostMicros),
		"availableMicros":  available,
		"affordable":       shortfall == 0,
		"shortfallMicros":  shortfall,
		"priceVersion":     quote.PriceVersion,
		"promotionVersion": quote.PromotionVersion,
		"quoteToken":       quote.Token,
		"expiresAt":        httpx.FormatTime(quote.ExpiresAt),
	}
	if quote.BillingMode == service.BillingModeFreeTrial {
		payload["freeTrialsLeft"] = quote.FreeTrialsLeft
	}
	c.JSON(http.StatusOK, payload)
}

// ===== 图像 =====

type imageRequest struct {
	Model          string   `json:"model"`
	Prompt         string   `json:"prompt"`
	N              *int     `json:"n"`
	Size           string   `json:"size"`
	Quality        string   `json:"quality"`
	Background     string   `json:"background"`
	References     []string `json:"references"`
	Mask           string   `json:"mask"`
	QuoteToken     string   `json:"quoteToken"`
	IdempotencyKey string   `json:"idempotencyKey"`
	SessionID      string   `json:"sessionId"`
}

func (h *AIHandler) Images(c *gin.Context) {
	user, ok := h.currentUser(c)
	if !ok {
		return
	}
	var req imageRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Prompt) == "" {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if !requireIdempotencyKey(c, req.IdempotencyKey) {
		return
	}
	catalogItem, ok := h.loadModel(c, req.Model, "image")
	if !ok {
		return
	}
	if !h.checkReferences(c, catalogItem, req.References, req.Mask) {
		return
	}
	params := map[string]string{}
	if req.Size != "" {
		params["size"] = req.Size
	}
	if req.Quality != "" {
		params["quality"] = req.Quality
	}
	n := 1
	if req.N != nil {
		n = *req.N
	}
	if n < 1 {
		n = 1
	}
	payload, ok := h.verifyQuote(c, user, catalogItem, "image", params, n, req.QuoteToken)
	if !ok {
		return
	}
	// 参考素材先读出来，既用于审核也用于后续生成；读取失败不进入预扣。
	refs, err := h.loadInlineMedia(user.ID, req.References)
	if err != nil {
		// 存储层错误可能带对象路径，只回固定文案，原始错误进日志（差异清单 #120）。
		slog.Error("读取参考素材失败", "err", err)
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"references": "参考素材不可用，请检查参考素材后重试"}))
		return
	}
	// 输入预审必须在预扣之前：被拒输入不占免费次数、不写流水、不调上游。
	if !h.moderateInput(c, user, req.Prompt, refs) {
		return
	}
	if !h.precheck(c, user) {
		return
	}
	// 并发槽位在进入上游前占用，defer 释放，避免任何提前返回泄漏槽位。
	if !h.slots.allowRate(user.ID.String()) {
		h.abortConcurrency(c)
		return
	}
	if !h.slots.acquire("image", user.ID.String(), 3) {
		h.abortConcurrency(c)
		return
	}
	defer h.slots.release("image", user.ID.String())

	request, reserved, ok := h.beginRequest(c, user, catalogItem, "image", payload, req.IdempotencyKey, requestDims{SessionID: req.SessionID, Params: params, Spec: req.Size})
	if !ok {
		return
	}

	started := time.Now()
	var mask *provider.InlineMedia
	if req.Mask != "" {
		maskMedia, err := h.loadInlineMedia(user.ID, []string{req.Mask})
		if err != nil || len(maskMedia) == 0 {
			h.failRequest(c, request, reserved, errors.New("蒙版文件不可读"), 0)
			return
		}
		mask = &maskMedia[0]
	}

	result, err := h.callImages(c.Request.Context(), catalogItem, provider.ImageRequest{
		Model:      catalogItem.Name,
		Prompt:     req.Prompt,
		N:          n,
		Size:       req.Size,
		Quality:    req.Quality,
		Background: req.Background,
		References: refs,
		Mask:       mask,
	})
	if err != nil {
		h.failRequest(c, request, reserved, err, service.UpstreamStatus(err))
		return
	}
	if err := h.persistImages(c, user, request, catalogItem, req.Prompt, result, n, started); err != nil {
		if errors.Is(err, service.ErrContentRejected) {
			// 产物拒绝不退点：上游成本已经发生；误判走人工复核与赠送补偿。
			_ = h.markRequestRejected(request)
			errs.Abort(c, errs.ErrContentRejected)
			return
		}
		if errors.Is(err, service.ErrModerationUnavailable) {
			h.failRequest(c, request, reserved, err, 0)
			errs.Abort(c, errs.ErrModerationUnavailable)
			return
		}
		h.failRequest(c, request, reserved, err, 0)
		return
	}
}

// writeRejectedGeneration 为被拒产物补一条生成记录，状态与审核结论都标记为拒绝。
func (h *AIHandler) writeRejectedGeneration(user model.PlatformUser, request *model.AIRequest, catalogItem model.ModelCatalog, prompt string, durationMs int) {
	generation := &model.Generation{
		ID:               uuid.New(),
		UserID:           user.ID,
		Kind:             "image",
		Status:           "failed",
		Prompt:           prompt,
		Model:            catalogItem.Name,
		Config:           request.PricingSnapshot,
		Result:           []byte(`{}`),
		DurationMs:       durationMs,
		ModerationStatus: "rejected",
	}
	if err := h.db.Create(generation).Error; err != nil {
		slog.Error("写入被拒生成记录失败", "err", err)
	}
}

// markRequestRejected 把请求与生成记录标记为拒绝，保留已发生的消费。
func (h *AIHandler) markRequestRejected(request *model.AIRequest) error {
	if request == nil {
		return nil
	}
	if err := h.db.Model(&model.AIRequest{}).Where("id = ?", request.ID).Update("status", "failed").Error; err != nil {
		return err
	}
	return h.db.Model(&model.Generation{}).
		Where("user_id = ? AND kind = ? AND status = ?", request.UserID, "image", "pending").
		Updates(map[string]any{"status": "failed", "moderation_status": "rejected"}).Error
}

// callImages 按模型绑定的渠道依次尝试：可重试错误先原渠道重试一次，仍失败才切换渠道，
// 最多尝试 2 个渠道；能力不支持立即终止不做切换。
func (h *AIHandler) callImages(ctx context.Context, catalogItem model.ModelCatalog, req provider.ImageRequest) (provider.ImageResult, error) {
	channels, err := h.channelsFor(catalogItem)
	if err != nil {
		return provider.ImageResult{}, err
	}
	timeout := h.upstream.Timeouts()
	ctx, cancel := context.WithTimeout(ctx, timeout.ImageTotal)
	defer cancel()
	var lastErr error
	retried := false  // 当前渠道是否已原渠道重试过
	usedChannels := 0 // 已尝试的渠道数，上限 2
	for _, channel := range channels {
		if usedChannels >= 2 {
			break
		}
		usedChannels++
		result, err := channel.Provider.Images(ctx, req)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if errors.Is(err, provider.ErrCapabilityUnsupported) {
			break
		}
		// 尚未产生任何输出时才按重试表原渠道重试一次，仍失败再切渠道。
		if !retried && service.IsRetryableUpstream(lastErr) {
			retried = true
			if result, retryErr := channel.Provider.Images(ctx, req); retryErr == nil {
				return result, nil
			} else {
				lastErr = retryErr
			}
		}
		// 故障转移以最近一次失败为准，不能拿首次失败的错误判定。
		if !service.ShouldFailover(lastErr) {
			break
		}
		retried = false // 切到新渠道后重试预算重新计算
		slog.Warn("图像请求切换渠道", "channel", channel.Channel.ID, "err", lastErr)
	}
	return provider.ImageResult{}, lastErr
}

// persistImages 下载或解码产物、审核通过后落盘、写生成记录并返回响应。
// 产物先进入隔离区；审核拒绝时不进入正式存储，点数不自动退还。
func (h *AIHandler) persistImages(c *gin.Context, user model.PlatformUser, request *model.AIRequest, catalogItem model.ModelCatalog, prompt string, result provider.ImageResult, n int, started time.Time) error {
	maxFile, err := h.maxFileBytes(user.ID)
	if err != nil {
		return err
	}
	moderating := h.moderation != nil && h.moderation.Enabled()
	images := make([]gin.H, 0, len(result.Images))
	for _, image := range result.Images {
		var media gin.H
		if moderating {
			media, err = h.moderateAndStoreArtifact(c, user, "image", image, maxFile, 60*time.Second)
		} else {
			media, err = h.materialize(c.Request.Context(), user.ID, "image", image, maxFile, 60*time.Second)
		}
		if err != nil {
			if errors.Is(err, service.ErrContentRejected) {
				// 拒绝也要留一条生成记录，让用户看得到这次生成发生过、点数为何没退。
				h.writeRejectedGeneration(user, request, catalogItem, prompt, int(time.Since(started).Milliseconds()))
			}
			return err
		}
		images = append(images, media)
	}
	if len(images) == 0 {
		return errors.New("上游未返回图片")
	}

	durationMs := int(time.Since(started).Milliseconds())
	resultPayload, _ := json.Marshal(gin.H{"images": images})
	generation := &model.Generation{
		ID:               uuid.New(),
		UserID:           user.ID,
		Kind:             "image",
		Status:           "success",
		Prompt:           prompt,
		Model:            catalogItem.Name,
		Config:           request.PricingSnapshot,
		Result:           resultPayload,
		DurationMs:       durationMs,
		ModerationStatus: moderationStatusOf(moderating),
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(generation).Error; err != nil {
			return err
		}
		return h.requests.MarkSucceeded(tx, request.ID, durationMs)
	}); err != nil {
		return err
	}

	remaining, err := h.availableMicros(user.ID)
	if err != nil {
		return err
	}
	c.JSON(http.StatusOK, gin.H{
		"model":        catalogItem.Name,
		"credits":      h.creditsPayload(request, remaining),
		"images":       images,
		"generationId": generation.ID.String(),
		"durationMs":   durationMs,
	})
	return nil
}

// moderateAndStoreArtifact 把产物写入隔离区、送审，通过后才提交正式存储。
// 拒绝时返回 ErrContentRejected，由调用方按「产物拒绝不自动退点」处理。
func (h *AIHandler) moderateAndStoreArtifact(c *gin.Context, user model.PlatformUser, prefix string, image provider.GeneratedImage, maxBytes int64, downloadTimeout time.Duration) (gin.H, error) {
	data := image.Data
	mimeType := image.MimeType
	if len(data) == 0 && image.URL != "" {
		downloaded, contentType, err := h.upstream.Download(c.Request.Context(), image.URL, maxBytes, downloadTimeout)
		if err != nil {
			return nil, err
		}
		data = downloaded
		if contentType != "" {
			mimeType = contentType
		}
	}
	if len(data) == 0 {
		return nil, errors.New("上游产物为空")
	}
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = defaultMime(prefix)
	}
	quarantine, err := h.moderation.Quarantine().Put(c.Request.Context(), user.ID, data, mimeType)
	if err != nil {
		return nil, err
	}
	verdict, err := h.moderation.CheckArtifact(c.Request.Context(), user.ID, moderation.StageArtifact, moderation.ContentType(prefix), quarantine.Key, data, mimeType)
	h.moderation.SetQuarantineBytes(c.Request.Context(), verdict.RecordID, quarantine.Bytes)
	if err != nil {
		return nil, err
	}
	// 审核通过：先写正式存储，再删除隔离原件。媒体行的审核状态与生成记录同口径，
	// 不再恒为 skipped（差异清单 #12）。落盘统一走 saveGeneratedImage（水印挂钩在此执行，
	// 审核送审仍然使用原始 data，水印只发生在审核通过之后）。
	file, err := h.saveGeneratedImage(c.Request.Context(), user.ID, fmt.Sprintf("%s:%s", prefix, randomKey()), mimeType, data, maxBytes, artifactModerationStatus(h.moderation, verdict))
	if err != nil {
		return nil, err
	}
	_ = h.moderation.Quarantine().Delete(c.Request.Context(), user.ID, quarantine.Key)
	// 隔离原件已删除，同步清空审核记录上的 key，管理端不再显示点开 404 的残留项（差异清单 #26）。
	h.moderation.ClearQuarantineKey(c.Request.Context(), verdict.RecordID)
	return gin.H{
		"storageKey": file.StorageKey,
		"bytes":      file.Bytes,
		"mimeType":   file.MimeType,
	}, nil
}

func moderationStatusOf(moderating bool) string {
	if moderating {
		return "passed"
	}
	return "skipped"
}

// artifactModerationStatus 把产物审核结论同步进 media_files：审核开启用实际结论，
// 未启用保持 skipped（「这条数据产生时审核链路未覆盖」），失败放行记 error 侧的结论。
func artifactModerationStatus(m *service.ModerationService, verdict service.Verdict) string {
	if m == nil || !m.Enabled() || verdict.Decision == "" {
		return "skipped"
	}
	return verdict.Decision
}

// nonEmptyRefs 过滤 trim 后为空的引用，保证按长度切片不会越界（差异清单 #41）。
func nonEmptyRefs(refs []string) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref) != "" {
			out = append(out, ref)
		}
	}
	return out
}

// storeAudioArtifact 音频产物占位审核：当前没有音频审核模型，与图片/视频链路一致
// 先落隔离区并写产物审核记录（provider=none），不阻塞发放；接入音频审核模型后
// 在此替换为真实送审（差异清单 #29 占位）。审核未启用时保持旧行为直接落盘。
func (h *AIHandler) storeAudioArtifact(ctx context.Context, userID uuid.UUID, data []byte, mimeType string, url string, maxBytes int64, downloadTimeout time.Duration) (gin.H, error) {
	if len(data) == 0 && url != "" {
		downloaded, contentType, err := h.upstream.Download(ctx, url, maxBytes, downloadTimeout)
		if err != nil {
			return nil, err
		}
		data = downloaded
		if contentType != "" {
			mimeType = contentType
		}
	}
	if len(data) == 0 {
		return nil, errors.New("上游产物为空")
	}
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = "audio/mpeg"
	}
	if h.moderation == nil || !h.moderation.Enabled() {
		storageKey := "audio:" + randomKey()
		file, err := h.media.Save(ctx, service.SaveGeneratedMediaInput{
			UserID:     userID,
			StorageKey: storageKey,
			MimeType:   mimeType,
			Data:       data,
			MaxBytes:   maxBytes,
			Moderation: "skipped",
		})
		if err != nil {
			return nil, err
		}
		return gin.H{"storageKey": file.StorageKey, "bytes": file.Bytes, "mimeType": file.MimeType}, nil
	}
	quarantine, err := h.moderation.Quarantine().Put(ctx, userID, data, mimeType)
	if err != nil {
		return nil, err
	}
	verdict, err := h.moderation.CheckAudioArtifactPlaceholder(ctx, userID, quarantine.Key, data, mimeType)
	h.moderation.SetQuarantineBytes(ctx, verdict.RecordID, quarantine.Bytes)
	if err != nil {
		return nil, err
	}
	storageKey := "audio:" + randomKey()
	file, err := h.media.Save(ctx, service.SaveGeneratedMediaInput{
		UserID:     userID,
		StorageKey: storageKey,
		MimeType:   mimeType,
		Data:       data,
		MaxBytes:   maxBytes,
		Moderation: verdict.Decision,
	})
	if err != nil {
		return nil, err
	}
	_ = h.moderation.Quarantine().Delete(ctx, userID, quarantine.Key)
	h.moderation.ClearQuarantineKey(ctx, verdict.RecordID)
	return gin.H{"storageKey": file.StorageKey, "bytes": file.Bytes, "mimeType": file.MimeType}, nil
}

// materialize 把上游产物（base64 或 URL）转成 storageKey 并落盘。
func (h *AIHandler) materialize(ctx context.Context, userID uuid.UUID, prefix string, image provider.GeneratedImage, maxBytes int64, downloadTimeout time.Duration) (gin.H, error) {
	data := image.Data
	mimeType := image.MimeType
	if len(data) == 0 && image.URL != "" {
		downloaded, contentType, err := h.upstream.Download(ctx, image.URL, maxBytes, downloadTimeout)
		if err != nil {
			return nil, err
		}
		data = downloaded
		if contentType != "" {
			mimeType = contentType
		}
	}
	if len(data) == 0 {
		return nil, errors.New("上游产物为空")
	}
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = defaultMime(prefix)
	}
	storageKey := fmt.Sprintf("%s:%s", prefix, randomKey())
	file, err := h.saveGeneratedImage(ctx, userID, storageKey, mimeType, data, maxBytes, "")
	if err != nil {
		return nil, err
	}
	return gin.H{
		"storageKey": file.StorageKey,
		"bytes":      file.Bytes,
		"mimeType":   file.MimeType,
	}, nil
}

// saveGeneratedImage 是生成图片落盘的唯一汇聚点（T5 水印挂钩），materialize 与
// moderateAndStoreArtifact 审核通过后都经由它写正式存储。水印未注入/开关为关、
// 或归属者为付费档时走现状路径直接存原始字节；免费/日落档严格按 fail-closed 顺序执行：
// ① 烧水印，失败 → 整单失败退款，绝不回退原始字节；
// ② 无水印原件写入 orig 路径，失败 → 整单失败（此时水印版尚未落盘，没有任何干净字节可下发）；
// ③ 水印版落正式存储，失败 → 补偿删除已写的 orig（best-effort）再整单失败。
func (h *AIHandler) saveGeneratedImage(ctx context.Context, userID uuid.UUID, storageKey, mimeType string, data []byte, maxBytes int64, moderation string) (*model.MediaFile, error) {
	if h.wm != nil && h.wmEnabled() {
		plan, err := service.PlanDefFor(ctx, h.db, userID)
		if err != nil {
			slog.Error("watermark_failed", "kind", "image", "storageKey", storageKey, "err", err)
			return nil, fmt.Errorf("读取水印档位失败: %w", err)
		}
		if plan.ID != "paid" {
			wmBytes, wmMime, err := h.wm.Image(data, mimeType)
			if err != nil {
				slog.Error("watermark_failed", "kind", "image", "storageKey", storageKey, "err", err)
				return nil, fmt.Errorf("水印烧录失败: %w", err)
			}
			origPath := storage.OrigPath(userID.String(), storageKey)
			if _, _, err := h.store.Put(ctx, origPath, bytes.NewReader(data), mimeType); err != nil {
				slog.Error("watermark_failed", "kind", "image", "storageKey", storageKey, "err", err)
				return nil, fmt.Errorf("留存干净原件失败: %w", err)
			}
			file, err := h.media.Save(ctx, service.SaveGeneratedMediaInput{
				UserID:     userID,
				StorageKey: storageKey,
				MimeType:   wmMime,
				Data:       wmBytes,
				MaxBytes:   maxBytes,
				Moderation: moderation,
			})
			if err != nil {
				if delErr := h.store.Delete(ctx, origPath); delErr != nil {
					slog.Error("补偿删除干净原件失败", "path", origPath, "err", delErr)
				}
				return nil, err
			}
			return file, nil
		}
	}
	return h.media.Save(ctx, service.SaveGeneratedMediaInput{
		UserID:     userID,
		StorageKey: storageKey,
		MimeType:   mimeType,
		Data:       data,
		MaxBytes:   maxBytes,
		Moderation: moderation,
	})
}

// ===== 语音 =====

type speechRequest struct {
	Model          string  `json:"model"`
	Input          string  `json:"input"`
	Voice          string  `json:"voice"`
	Format         string  `json:"format"`
	Speed          float64 `json:"speed"`
	Instructions   string  `json:"instructions"`
	QuoteToken     string  `json:"quoteToken"`
	IdempotencyKey string  `json:"idempotencyKey"`
	SessionID      string  `json:"sessionId"`
}

func (h *AIHandler) Speech(c *gin.Context) {
	user, ok := h.currentUser(c)
	if !ok {
		return
	}
	var req speechRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Input) == "" {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if !requireIdempotencyKey(c, req.IdempotencyKey) {
		return
	}
	catalogItem, ok := h.loadModel(c, req.Model, "audio")
	if !ok {
		return
	}
	payload, ok := h.verifyQuote(c, user, catalogItem, "audio", nil, 1, req.QuoteToken)
	if !ok {
		return
	}
	if !h.moderateInput(c, user, req.Input, nil) {
		return
	}
	if !h.precheck(c, user) {
		return
	}
	if !h.slots.allowRate(user.ID.String()) {
		h.abortConcurrency(c)
		return
	}
	if !h.slots.acquire("audio", user.ID.String(), 2) {
		h.abortConcurrency(c)
		return
	}
	defer h.slots.release("audio", user.ID.String())

	// 音频报价不依赖参数，快照在预扣前组装好，供用量分析按 voice 等维度拆分。
	format := req.Format
	if format == "" {
		format = "mp3"
	}
	audioParams := map[string]string{"format": format}
	if req.Voice != "" {
		audioParams["voice"] = req.Voice
	}
	if req.Speed != 0 {
		audioParams["speed"] = strconv.FormatFloat(req.Speed, 'f', -1, 64)
	}
	request, reserved, ok := h.beginRequest(c, user, catalogItem, "audio", payload, req.IdempotencyKey, requestDims{SessionID: req.SessionID, Params: audioParams, Spec: req.Voice})
	if !ok {
		return
	}
	started := time.Now()
	result, err := h.callSpeech(c.Request.Context(), catalogItem, provider.SpeechRequest{
		Model:        catalogItem.Name,
		Input:        req.Input,
		Voice:        req.Voice,
		Format:       format,
		Speed:        req.Speed,
		Instructions: req.Instructions,
	})
	if err != nil {
		h.failRequest(c, request, reserved, err, service.UpstreamStatus(err))
		return
	}
	maxFile, err := h.maxFileBytes(user.ID)
	if err != nil {
		h.failRequest(c, request, reserved, err, 0)
		return
	}
	media, err := h.storeAudioArtifact(c.Request.Context(), user.ID, result.Data, result.MimeType, result.URL, maxFile, 120*time.Second)
	if err != nil {
		h.failRequest(c, request, reserved, err, 0)
		return
	}
	durationMs := int(time.Since(started).Milliseconds())
	if err := h.requests.MarkSucceeded(nil, request.ID, durationMs); err != nil {
		slog.Error("收敛语音请求失败", "request", request.ID, "err", err)
	}
	remaining, _ := h.availableMicros(user.ID)
	c.JSON(http.StatusOK, gin.H{
		"model":      catalogItem.Name,
		"credits":    h.creditsPayload(request, remaining),
		"audio":      media,
		"durationMs": durationMs,
	})
}

// callSpeech 与 callImages 一样最多尝试 2 个渠道做故障转移，但不做原渠道重试。
func (h *AIHandler) callSpeech(ctx context.Context, catalogItem model.ModelCatalog, req provider.SpeechRequest) (provider.SpeechResult, error) {
	channels, err := h.channelsFor(catalogItem)
	if err != nil {
		return provider.SpeechResult{}, err
	}
	timeout := h.upstream.Timeouts()
	ctx, cancel := context.WithTimeout(ctx, timeout.SpeechTotal)
	defer cancel()
	var lastErr error
	for index, channel := range channels {
		if index >= 2 {
			break
		}
		result, err := channel.Provider.Speech(ctx, req)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if errors.Is(err, provider.ErrCapabilityUnsupported) {
			break
		}
		if !service.ShouldFailover(err) {
			break
		}
	}
	return provider.SpeechResult{}, lastErr
}

// ===== 视频 =====

type videoRequest struct {
	Model           string   `json:"model"`
	Prompt          string   `json:"prompt"`
	Duration        *int     `json:"duration"`
	Ratio           string   `json:"ratio"`
	Resolution      string   `json:"resolution"`
	GenerateAudio   *bool    `json:"generateAudio"`
	Watermark       *bool    `json:"watermark"`
	Mode            string   `json:"mode"`
	References      []string `json:"references"`
	VideoReferences []string `json:"videoReferences"`
	AudioReferences []string `json:"audioReferences"`
	QuoteToken      string   `json:"quoteToken"`
	IdempotencyKey  string   `json:"idempotencyKey"`
	SessionID       string   `json:"sessionId"`
}

// CreateVideo 创建视频生成任务：非同步，拿到上游任务句柄即返回 202 与建议轮询间隔，
// 产物由任务轮询（VideoTask）发放；每用户 pending 任务数上限 3。
func (h *AIHandler) CreateVideo(c *gin.Context) {
	user, ok := h.currentUser(c)
	if !ok {
		return
	}
	var req videoRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Prompt) == "" {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if !requireIdempotencyKey(c, req.IdempotencyKey) {
		return
	}
	catalogItem, ok := h.loadModel(c, req.Model, "video")
	if !ok {
		return
	}
	if !h.checkVideoReferences(c, catalogItem, req) {
		return
	}
	params := map[string]string{}
	if req.Ratio != "" {
		params["ratio"] = req.Ratio
	}
	if req.Resolution != "" {
		params["resolution"] = req.Resolution
	}
	duration := 5
	if req.Duration != nil && *req.Duration > 0 {
		duration = *req.Duration
	}
	params["duration"] = strconv.Itoa(duration)
	payload, ok := h.verifyQuote(c, user, catalogItem, "video", params, 1, req.QuoteToken)
	if !ok {
		return
	}
	// loadInlineMedia 会跳过空串引用，这里先过滤再切片，
	// 否则 references:[""] 会让下面的按长度切片越界（差异清单 #41）。
	refs := nonEmptyRefs(req.References)
	videoRefs := nonEmptyRefs(req.VideoReferences)
	audioRefs := nonEmptyRefs(req.AudioReferences)
	media, err := h.loadInlineMedia(user.ID, append(append(append([]string{}, refs...), videoRefs...), audioRefs...))
	if err != nil {
		// 存储层错误可能带对象路径，只回固定文案，原始错误进日志（差异清单 #120）。
		slog.Error("读取参考素材失败", "err", err)
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"references": "参考素材不可用，请检查参考素材后重试"}))
		return
	}
	images := media[:len(refs)]
	videos := media[len(refs) : len(refs)+len(videoRefs)]
	audios := media[len(refs)+len(videoRefs):]
	if !h.moderateInput(c, user, req.Prompt, images) {
		return
	}
	if !h.precheck(c, user) {
		return
	}
	// 视频任务的并发单独计算，不占 HTTP 连接。
	// 每分钟限流与全局并发与其它能力同口径（差异清单 #16）：
	// 限流直接判；全局并发用请求期槽位兜底（pending 上限 3 仍然独立生效）。
	if !h.slots.allowRate(user.ID.String()) {
		h.abortConcurrency(c)
		return
	}
	if !h.slots.acquire("video", user.ID.String(), 3) {
		h.abortConcurrency(c)
		return
	}
	defer h.slots.release("video", user.ID.String())
	pending, err := h.pendingVideoTasks(user.ID)
	if err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if pending >= 3 {
		h.abortConcurrency(c)
		return
	}
	request, reserved, ok := h.beginRequest(c, user, catalogItem, "video", payload, req.IdempotencyKey, requestDims{SessionID: req.SessionID, Params: params, Spec: req.Resolution})
	if !ok {
		return
	}

	// mode 不在这里兜底：是否走首尾帧要结合参考图数量判断，交给 provider 的
	// resolveVideoMode 决定（显式 reference 或超过 2 张才走 reference，否则 frames）。
	generateAudio := req.GenerateAudio == nil || *req.GenerateAudio
	watermark := req.Watermark != nil && *req.Watermark
	task, generationID, err := h.createUpstreamVideoTask(c, user, request, catalogItem, provider.VideoRequest{
		Model:           catalogItem.Name,
		Prompt:          req.Prompt,
		Duration:        duration,
		Ratio:           req.Ratio,
		Resolution:      req.Resolution,
		GenerateAudio:   generateAudio,
		Watermark:       watermark,
		Mode:            req.Mode,
		References:      images,
		VideoReferences: videos,
		AudioReferences: audios,
	}, req.Prompt)
	if err != nil {
		h.failRequest(c, request, reserved, err, service.UpstreamStatus(err))
		return
	}
	remaining, _ := h.availableMicros(user.ID)
	c.JSON(http.StatusAccepted, gin.H{
		"taskId":       task.ID.String(),
		"status":       task.Status,
		"credits":      h.creditsPayload(request, remaining),
		"generationId": generationID.String(),
		"pollAfterMs":  task.PollAfterMs(),
	})
}

// createUpstreamVideoTask 调上游创建视频任务并把任务与 pending 生成记录在同一事务落库；
// 渠道故障转移上限与其它能力一致（≤2），ai_requests 同时记录实际采用的渠道。
func (h *AIHandler) createUpstreamVideoTask(c *gin.Context, user model.PlatformUser, request *model.AIRequest, catalogItem model.ModelCatalog, videoReq provider.VideoRequest, prompt string) (*model.AITask, uuid.UUID, error) {
	channels, err := h.channelsFor(catalogItem)
	if err != nil {
		return nil, uuid.Nil, err
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), h.upstream.Timeouts().VideoCreate)
	defer cancel()
	var upstream provider.VideoTask
	var lastErr error
	usedChannel := uuid.Nil
	for index, channel := range channels {
		if index >= 2 {
			break
		}
		task, err := channel.Provider.CreateVideo(ctx, videoReq)
		if err == nil {
			upstream = task
			usedChannel = channel.Channel.ID
			break
		}
		lastErr = err
		if errors.Is(err, provider.ErrCapabilityUnsupported) || !service.ShouldFailover(err) {
			break
		}
	}
	if upstream.UpstreamTaskID == "" {
		if lastErr == nil {
			lastErr = errors.New("创建视频任务失败")
		}
		return nil, uuid.Nil, lastErr
	}
	if upstream.PollAfterMs <= 0 {
		upstream.PollAfterMs = 5000
	}

	task := &model.AITask{
		ID:             uuid.New(),
		UserID:         user.ID,
		RequestID:      request.ID,
		Provider:       upstream.Provider,
		UpstreamTaskID: upstream.UpstreamTaskID,
		Status:         "pending",
		GenerationID:   uuid.New(),
	}
	config, _ := json.Marshal(gin.H{
		"ratio":      videoReq.Ratio,
		"resolution": videoReq.Resolution,
		"duration":   videoReq.Duration,
	})
	// 生成记录带上任务句柄：前端刷新后据此恢复轮询，服务端后台任务本身不受页面影响。
	// 形状与前端 VideoGenerationTask 对齐（id + model），直接复用轮询逻辑。
	resultPayload, _ := json.Marshal(gin.H{"task": gin.H{"id": task.ID.String(), "model": catalogItem.Name}})
	generation := &model.Generation{
		ID:               task.GenerationID,
		UserID:           user.ID,
		Kind:             "video",
		Status:           "pending",
		Prompt:           prompt,
		Model:            catalogItem.Name,
		Config:           config,
		Result:           resultPayload,
		ModerationStatus: "skipped",
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		if err := tx.Create(generation).Error; err != nil {
			return err
		}
		return tx.Model(&model.AIRequest{}).Where("id = ?", request.ID).Update("channel_id", usedChannel).Error
	}); err != nil {
		return nil, uuid.Nil, err
	}
	return task, generation.ID, nil
}

func (h *AIHandler) pendingVideoTasks(userID uuid.UUID) (int64, error) {
	var count int64
	err := h.db.Model(&model.AITask{}).Where("user_id = ? AND status = ?", userID, "pending").Count(&count).Error
	return count, err
}

// VideoTask 查询视频任务状态。任务归属当前用户，查别人的任务返回 404。
func (h *AIHandler) VideoTask(c *gin.Context) {
	user, ok := h.currentUser(c)
	if !ok {
		return
	}
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	view, err := h.tasks.ViewTask(c.Request.Context(), user.ID, taskID)
	if err != nil {
		if errors.Is(err, service.ErrTaskNotFound) {
			errs.Abort(c, errs.ErrNotFound)
			return
		}
		slog.Error("查询视频任务失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	credits := gin.H{
		"baseCostMicros":  view.BaseCostMicros,
		"finalCostMicros": view.FinalCostMicros,
		"finalCostYuan":   microsToYuan(view.FinalCostMicros),
		"refundedMicros":  view.RefundedMicros,
	}
	if view.PromotionID != "" {
		credits["discount"] = gin.H{"promotionId": view.PromotionID, "discountBps": 0}
	} else {
		credits["discount"] = nil
	}
	payload := gin.H{
		"taskId":       view.TaskID,
		"status":       view.Status,
		"credits":      credits,
		"generationId": view.GenerationID,
		"pollAfterMs":  view.PollAfterMs,
	}
	if view.Status == "succeeded" {
		payload["video"] = gin.H{
			"storageKey": view.StorageKey,
			"bytes":      view.Bytes,
			"mimeType":   view.MimeType,
		}
	}
	if view.Status == "failed" {
		payload["error"] = gin.H{"code": "UPSTREAM_ERROR", "message": view.Error}
	}
	c.JSON(http.StatusOK, payload)
}

// ===== 对话 =====

type chatRequest struct {
	Model           string                 `json:"model"`
	Messages        []provider.ChatMessage `json:"messages"`
	ReasoningEffort string                 `json:"reasoningEffort"`
	Tools           []provider.ChatTool    `json:"tools"`
	ToolChoice      any                    `json:"toolChoice"`
	Stream          *bool                  `json:"stream"`
	QuoteToken      string                 `json:"quoteToken"`
	IdempotencyKey  string                 `json:"idempotencyKey"`
	SessionID       string                 `json:"sessionId"`
}

// Chat 文本对话：stream 未传或为 true 走 SSE 事件流，显式 false 走一次性 JSON 响应。
func (h *AIHandler) Chat(c *gin.Context) {
	user, ok := h.currentUser(c)
	if !ok {
		return
	}
	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Messages) == 0 {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if !requireIdempotencyKey(c, req.IdempotencyKey) {
		return
	}
	catalogItem, ok := h.loadModel(c, req.Model, "text")
	if !ok {
		return
	}
	payload, ok := h.verifyQuote(c, user, catalogItem, "text", nil, 1, req.QuoteToken)
	if !ok {
		return
	}
	promptText := chatPromptText(req.Messages)
	if !h.moderateInput(c, user, promptText, nil) {
		return
	}
	if !h.precheck(c, user) {
		return
	}
	if !h.slots.allowRate(user.ID.String()) {
		h.abortConcurrency(c)
		return
	}
	if !h.slots.acquire("text", user.ID.String(), 2) {
		h.abortConcurrency(c)
		return
	}
	defer h.slots.release("text", user.ID.String())

	stream := req.Stream == nil || *req.Stream
	request, reserved, ok := h.beginRequest(c, user, catalogItem, "text", payload, req.IdempotencyKey, requestDims{SessionID: req.SessionID})
	if !ok {
		return
	}
	if !stream {
		h.chatNonStream(c, user, request, reserved, catalogItem, req)
		return
	}
	h.chatStream(c, user, request, reserved, catalogItem, req)
}

// chatNonStream 非流式对话：收集完整输出后一次性响应 JSON。
func (h *AIHandler) chatNonStream(c *gin.Context, user model.PlatformUser, request *model.AIRequest, reserved *service.ReserveResult, catalogItem model.ModelCatalog, req chatRequest) {
	started := time.Now()
	// 非流式每次尝试都用全新的 collectSink（由 callChat 逐次调用），
	// 避免上一渠道的半截 tool_calls 与下一渠道的结果在累积式 sink 里叠加成脏数据。
	var collect *collectSink
	_, err := h.callChat(c.Request.Context(), catalogItem, provider.ChatRequest{
		Model:           catalogItem.Name,
		Messages:        req.Messages,
		ReasoningEffort: req.ReasoningEffort,
		Tools:           req.Tools,
		ToolChoice:      req.ToolChoice,
		Stream:          false,
	}, func() provider.StreamSink {
		collect = &collectSink{}
		return collect
	})
	if err != nil {
		h.failRequest(c, request, reserved, err, service.UpstreamStatus(err))
		return
	}
	durationMs := int(time.Since(started).Milliseconds())
	if err := h.requests.MarkSucceeded(nil, request.ID, durationMs); err != nil {
		slog.Error("收敛文本请求失败", "request", request.ID, "err", err)
	}
	remaining, _ := h.availableMicros(user.ID)
	c.JSON(http.StatusOK, gin.H{
		"credits":      h.creditsPayload(request, remaining),
		"content":      collect.text.String(),
		"toolCalls":    collect.calls,
		"finishReason": collect.finishReason,
	})
}

// collectSink 收集非流式对话的输出。
type collectSink struct {
	text         strings.Builder
	calls        []provider.ToolCall
	finishReason string
}

func (s *collectSink) Delta(text string) error {
	s.text.WriteString(text)
	return nil
}

func (s *collectSink) ToolCall(call provider.ToolCall) error {
	s.calls = append(s.calls, call)
	return nil
}

func (s *collectSink) Done(finishReason string) {
	s.finishReason = finishReason
}

// producedSink 由流式 sink 实现：只要客户端已经收到过任何 delta 或 tool_call 事件，
// callChat 就必须立即终止，不重试也不切渠道。非流式的 collectSink 不实现它，
// 客户端还没收到任何字节，失败时照常走重试表。
type producedSink interface{ produced() bool }

// callChat 带故障转移地执行对话。sinkFor 在每次尝试时提供一个 sink：
// 流式路径返回同一个 sink（客户端已收到字节就不能重来），非流式路径每次给全新的
// collectSink。成功时返回实际采用的 sink。
// 唯一的终止闸门是「客户端是否已收到任何字节」：已产出则立即返回 lastErr；
// 此前失败才按重试表原渠道重试一次，仍失败再切下一个渠道（渠道数 ≤ 2）。
func (h *AIHandler) callChat(ctx context.Context, catalogItem model.ModelCatalog, req provider.ChatRequest, sinkFor func() provider.StreamSink) (provider.StreamSink, error) {
	channels, err := h.channelsFor(catalogItem)
	if err != nil {
		return nil, err
	}
	timeout := h.upstream.Timeouts()
	ctx, cancel := context.WithTimeout(ctx, timeout.StreamTotal)
	defer cancel()
	var lastErr error
	var adopted provider.StreamSink
	retried := false  // 当前渠道是否已原渠道重试过
	usedChannels := 0 // 已尝试的渠道数，上限 2
	for _, channel := range channels {
		if usedChannels >= 2 {
			break
		}
		usedChannels++
		sink := sinkFor()
		err := channel.Provider.ChatStream(ctx, req, sink)
		if err == nil {
			return sink, nil
		}
		lastErr = err
		adopted = sink
		// 客户端已经收到过任何字节（delta 或 tool_call）：立即终止，避免重复正文。
		if produced, ok := sink.(producedSink); ok && produced.produced() {
			return adopted, lastErr
		}
		if errors.Is(err, provider.ErrCapabilityUnsupported) {
			break
		}
		if !retried && service.IsRetryableUpstream(lastErr) {
			retried = true
			sink = sinkFor()
			if retryErr := channel.Provider.ChatStream(ctx, req, sink); retryErr == nil {
				return sink, nil
			} else {
				lastErr = retryErr
				adopted = sink
				if produced, ok := sink.(producedSink); ok && produced.produced() {
					return adopted, lastErr
				}
			}
		}
		if !service.ShouldFailover(lastErr) {
			break
		}
		retried = false // 切到新渠道后重试预算重新计算
		slog.Warn("对话请求切换渠道", "channel", channel.Channel.ID, "err", lastErr)
	}
	return adopted, lastErr
}

// ===== 公共辅助 =====

// currentUser 从中间件注入的身份加载用户；邮箱未验证一律拒绝，生成接口要求已验证。
func (h *AIHandler) currentUser(c *gin.Context) (model.PlatformUser, bool) {
	uid, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return model.PlatformUser{}, false
	}
	var user model.PlatformUser
	if err := h.db.First(&user, "id = ?", uid).Error; err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return user, false
	}
	if user.EmailVerifiedAt == nil {
		errs.Abort(c, errs.ErrEmailNotVerif)
		return user, false
	}
	return user, true
}

// loadModel 校验模型存在、启用且能力匹配。目录外与带渠道前缀痕迹的标识一律拒绝。
// 同时接受目录 id 与模型名：前端目录用 id 选择，管理后台与调用脚本用 name。
func (h *AIHandler) loadModel(c *gin.Context, name, capability string) (model.ModelCatalog, bool) {
	clean := strings.TrimSpace(name)
	if clean == "" || strings.Contains(clean, "::") {
		h.abortModel(c, clean)
		return model.ModelCatalog{}, false
	}
	var catalogItem model.ModelCatalog
	query := h.db.Where("enabled = ? AND (name = ? OR id = ?)", true, clean, clean)
	if parsed, err := uuid.Parse(clean); err == nil {
		query = h.db.Where("enabled = ? AND (name = ? OR id = ?)", true, clean, parsed)
	}
	if err := query.First(&catalogItem).Error; err != nil {
		h.abortModel(c, clean)
		return model.ModelCatalog{}, false
	}
	if capability != "" && catalogItem.Capability != capability {
		h.abortModel(c, clean)
		return model.ModelCatalog{}, false
	}
	return catalogItem, true
}

func (h *AIHandler) abortModel(c *gin.Context, name string) {
	errs.Abort(c, errs.WithExtra(errs.ErrModelNotSupported, gin.H{"model": name}))
}

func (h *AIHandler) abortQuoteError(c *gin.Context, err error) {
	var paramErr *service.ParamNotSupportedError
	switch {
	case errors.As(err, &paramErr):
		errs.Abort(c, errs.WithExtra(errs.ErrParamNotSupported, gin.H{"param": paramErr.Param, "allowed": paramErr.Allowed}))
	default:
		errs.Abort(c, errs.ErrValidation)
	}
}

// abortConcurrency 返回 429 并附 Retry-After: 5，作为给前端的建议重试间隔。
func (h *AIHandler) abortConcurrency(c *gin.Context) {
	c.Header("Retry-After", "5")
	errs.Abort(c, errs.ErrConcurrencyLimited)
}

func (h *AIHandler) checkReferences(c *gin.Context, catalogItem model.ModelCatalog, references []string, mask string) bool {
	constraints, err := service.ParseConstraints(catalogItem.Constraints)
	if err != nil {
		errs.Abort(c, errs.ErrInternal)
		return false
	}
	if len(references) > 0 && !constraints.HasFeature("referenceImage") {
		errs.Abort(c, errs.WithExtra(errs.ErrParamNotSupported, gin.H{"param": "references", "allowed": []string{}}))
		return false
	}
	if mask != "" && !constraints.HasFeature("mask") {
		errs.Abort(c, errs.WithExtra(errs.ErrParamNotSupported, gin.H{"param": "mask", "allowed": []string{}}))
		return false
	}
	return true
}

func (h *AIHandler) checkVideoReferences(c *gin.Context, catalogItem model.ModelCatalog, req videoRequest) bool {
	constraints, err := service.ParseConstraints(catalogItem.Constraints)
	if err != nil {
		errs.Abort(c, errs.ErrInternal)
		return false
	}
	if len(req.References) > 0 && !constraints.HasFeature("referenceImage") {
		errs.Abort(c, errs.WithExtra(errs.ErrParamNotSupported, gin.H{"param": "references", "allowed": []string{}}))
		return false
	}
	if len(req.VideoReferences) > 0 && !constraints.HasFeature("referenceVideo") {
		errs.Abort(c, errs.WithExtra(errs.ErrParamNotSupported, gin.H{"param": "videoReferences", "allowed": []string{}}))
		return false
	}
	if len(req.AudioReferences) > 0 && !constraints.HasFeature("referenceAudio") {
		errs.Abort(c, errs.WithExtra(errs.ErrParamNotSupported, gin.H{"param": "audioReferences", "allowed": []string{}}))
		return false
	}
	return true
}

// verifyQuote 校验报价凭证，失败返回 false 并已写入错误响应。
func (h *AIHandler) verifyQuote(c *gin.Context, user model.PlatformUser, catalogItem model.ModelCatalog, capability string, params map[string]string, n int, token string) (service.QuotePayload, bool) {
	if token == "" {
		errs.Abort(c, errs.ErrQuoteStale)
		return service.QuotePayload{}, false
	}
	payload, err := h.quotes.VerifyQuote(c.Request.Context(), user.ID, token, catalogItem, capability, params, n, time.Now())
	if err != nil {
		if errors.Is(err, service.ErrQuoteStale) {
			errs.Abort(c, errs.ErrQuoteStale)
			return service.QuotePayload{}, false
		}
		errs.Abort(c, errs.ErrValidation)
		return service.QuotePayload{}, false
	}
	return payload.ToPayload(), true
}

// precheck 邮箱已在 currentUser 校验；这里补存储配额预检，避免生成后才发现存不下。
// 只读态（实际用量超过档位配额）按 403 READ_ONLY 拦截，与媒体上传同口径。
func (h *AIHandler) precheck(c *gin.Context, user model.PlatformUser) bool {
	plan, err := service.PlanDefFor(c.Request.Context(), h.db, user.ID)
	if err != nil {
		errs.Abort(c, errs.ErrInternal)
		return false
	}
	used, _, err := h.usage.Snapshot(c.Request.Context(), user.ID)
	if err != nil {
		errs.Abort(c, errs.ErrInternal)
		return false
	}
	if used > plan.StorageBytes {
		errs.Abort(c, errs.WithExtra(errs.ErrReadOnly, gin.H{"planId": plan.ID, "used": used, "limit": plan.StorageBytes}))
		return false
	}
	return true
}

// requestDims 携带请求落库时的用量分析维度：客户端会话标识、报价参数快照与主规格串。
// 幂等命中既有请求时这些值不会写回，保留首次提交的维度。
type requestDims struct {
	SessionID string
	Params    map[string]string
	Spec      string
}

// beginRequest 预扣或占用免费额度并落 ai_requests 行。扣费与请求行在同一个事务内
// （异常矩阵路径一）：余额不足、免费额度被并发占用或重复幂等键命中时整体回滚，
// 不留下任何扣费。重复的 idempotencyKey 返回既有请求，不重复扣点，分析维度保留首次的值。
func (h *AIHandler) beginRequest(c *gin.Context, user model.PlatformUser, catalogItem model.ModelCatalog, capability string, payload service.QuotePayload, idempotencyKey string, dims requestDims) (*model.AIRequest, *service.ReserveResult, bool) {
	key := idempotencyKey
	snapshot, _ := json.Marshal(gin.H{
		"model":            catalogItem.Name,
		"capability":       capability,
		"baseCostMicros":   payload.BaseCostMicros,
		"finalCostMicros":  payload.FinalCostMicros,
		"priceVersion":     payload.PriceVersion,
		"promotionId":      payload.PromotionID,
		"promotionVersion": payload.PromotionVersion,
		"billingMode":      payload.BillingMode,
	})
	request := &model.AIRequest{
		ID:               uuid.New(),
		UserID:           user.ID,
		IdempotencyKey:   &key,
		Capability:       capability,
		Model:            catalogItem.Name,
		BaseCostMicros:   payload.BaseCostMicros,
		FinalCostMicros:  payload.FinalCostMicros,
		PriceVersion:     payload.PriceVersion,
		PromotionID:      payload.PromotionID,
		PromotionVersion: payload.PromotionVersion,
		PromotionEnabled: payload.PromotionEnabled,
		PricingSnapshot:  snapshot,
		SessionID:        sessionIDOf(dims.SessionID),
		Params:           paramsSnapshot(dims.Params),
		ParamSpec:        clipRunes(dims.Spec, 64),
		StatDate:         time.Now().In(model.StatZone).Format(model.StatDateFormat),
		Status:           "running",
	}
	var reserved *service.ReserveResult
	var duplicate *model.AIRequest
	// errDuplicateConflict 让事务整体回滚（含预扣），再按既有请求收敛响应。
	errDuplicateConflict := errors.New("duplicate idempotency key")
	err := h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		result, err := h.reserve(tx, user, capability, payload, request.ID.String())
		if err != nil {
			return err
		}
		reserved = result
		request.UsedFreeTrial = reserved.UsedFreeTrial
		if len(reserved.Transactions) > 0 {
			ids := make([]uuid.UUID, 0, len(reserved.Transactions))
			for _, transaction := range reserved.Transactions {
				ids = append(ids, transaction.ID)
			}
			raw, _ := json.Marshal(ids)
			request.ConsumeTransactionIDs = raw
		}
		existing, created, err := h.requests.CreateOrGet(tx, request)
		if err != nil {
			return err
		}
		if !created {
			duplicate = existing
			return errDuplicateConflict
		}
		return h.requests.MarkConsumeTransactions(tx, request.ID, reserved.Transactions)
	})
	if errors.Is(err, errDuplicateConflict) {
		switch duplicate.Status {
		case "succeeded":
			errs.Abort(c, errs.WithExtra(errs.ErrQuoteStale, gin.H{"duplicate": true, "requestId": duplicate.ID.String()}))
		default:
			errs.Abort(c, errs.WithExtra(errs.ErrConcurrencyLimited, gin.H{"duplicate": true, "requestId": duplicate.ID.String()}))
		}
		return nil, nil, false
	}
	if err != nil {
		switch {
		case errors.Is(err, service.ErrFreeTrialTaken) || errors.Is(err, service.ErrQuoteStale):
			errs.Abort(c, errs.ErrQuoteStale)
		case errors.Is(err, billing.ErrInsufficientCredits):
			available, _ := h.availableMicros(user.ID)
			required := payload.FinalCostMicros
			shortfall := required - available
			if shortfall < 0 {
				shortfall = 0
			}
			errs.Abort(c, errs.WithExtra(errs.ErrInsufficientCredits, gin.H{
				"requiredMicros":  required,
				"availableMicros": available,
				"shortfallMicros": shortfall,
			}))
		case errors.Is(err, service.ErrMediaQuotaExceeded):
			errs.Abort(c, errs.ErrStorageQuota)
		default:
			slog.Error("写入生成请求失败", "err", err)
			errs.Abort(c, errs.ErrInternal)
		}
		return nil, nil, false
	}
	return request, reserved, true
}

// reserve 在传入事务内原子占用免费额度或按报价预扣，bizKey 统一用 ai_requests 行 id，
// 失败退还按它定位消费流水。免费额度被并发占用时返回 ErrFreeTrialTaken。
func (h *AIHandler) reserve(tx *gorm.DB, user model.PlatformUser, capability string, payload service.QuotePayload, bizKey string) (*service.ReserveResult, error) {
	if payload.BillingMode == service.BillingModeFreeTrial {
		metric := ""
		switch capability {
		case "image":
			metric = billing.MetricFreeImageTrial
		case "video":
			metric = billing.MetricFreeVideoTrial
		default:
			return nil, service.ErrQuoteStale
		}
		consumed, err := h.billing.ConsumeFreeTrial(tx, user.ID, metric)
		if err != nil {
			return nil, err
		}
		if !consumed {
			return nil, service.ErrFreeTrialTaken
		}
		return &service.ReserveResult{UsedFreeTrial: true}, nil
	}
	if payload.FinalCostMicros <= 0 {
		return &service.ReserveResult{}, nil
	}
	transactions, err := h.billing.Reserve(tx, user.ID, payload.FinalCostMicros, bizKey)
	if err != nil {
		return nil, err
	}
	return &service.ReserveResult{ConsumedMicros: payload.FinalCostMicros, Transactions: transactions}, nil
}

// failRequest 收敛失败请求并原桶退还点数。
func (h *AIHandler) failRequest(c *gin.Context, request *model.AIRequest, reserved *service.ReserveResult, err error, upstreamStatus int) {
	h.abortUpstream(c, err, upstreamStatus)
	if request == nil {
		return
	}
	if request.Status != "running" {
		return
	}
	if refundErr := h.requests.MarkFailed(c.Request.Context(), request, upstreamStatus); refundErr != nil {
		slog.Error("失败退还点数出错", "request", request.ID, "err", refundErr)
	}
	_ = reserved
}

// moderateInput 审核提示词与参考图。被拒返回 422，服务不可用按 fail mode 返回 503 或放行。
func (h *AIHandler) moderateInput(c *gin.Context, user model.PlatformUser, prompt string, references []provider.InlineMedia) bool {
	if h.moderation == nil || !h.moderation.Enabled() {
		return true
	}
	inputs := make([]service.ReferenceInput, 0, len(references))
	for _, reference := range references {
		inputs = append(inputs, service.ReferenceInput{
			ContentType: "image",
			MimeType:    reference.MimeType,
			Data:        reference.Data,
		})
	}
	_, err := h.moderation.CheckInput(c.Request.Context(), user.ID, prompt, inputs)
	switch {
	case err == nil:
		return true
	case errors.Is(err, service.ErrContentRejected):
		errs.Abort(c, errs.ErrContentRejected)
		return false
	case errors.Is(err, service.ErrModerationUnavailable):
		errs.Abort(c, errs.ErrModerationUnavailable)
		return false
	default:
		slog.Error("输入审核失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return false
	}
}

// chatPromptText 把消息里的文本拼成审核输入，图片只审参考图阶段已有的内容。
func chatPromptText(messages []provider.ChatMessage) string {
	var builder strings.Builder
	for _, message := range messages {
		switch typed := message.Content.(type) {
		case string:
			builder.WriteString(typed)
			builder.WriteString("\n")
		case []any:
			for _, item := range typed {
				if record, ok := item.(map[string]any); ok {
					if text, ok := record["text"].(string); ok {
						builder.WriteString(text)
						builder.WriteString("\n")
					}
				}
			}
		}
	}
	return builder.String()
}

// abortUpstream 把上游错误映射成本站错误响应。
func (h *AIHandler) abortUpstream(c *gin.Context, err error, upstreamStatus int) {
	switch {
	case errors.Is(err, provider.ErrCapabilityUnsupported):
		errs.Abort(c, errs.ErrModelNotSupported)
	case errors.Is(err, service.ErrMediaQuotaExceeded):
		errs.Abort(c, errs.ErrStorageQuota)
	case upstreamStatus >= 400:
		errs.Abort(c, errs.WithExtra(errs.ErrUpstreamError, gin.H{"upstreamStatus": upstreamStatus}))
	case isTimeout(err):
		errs.Abort(c, errs.WithExtra(errs.ErrUpstreamTimeout, gin.H{"phase": "response"}))
	default:
		slog.Error("上游请求失败", "err", err)
		errs.Abort(c, errs.WithExtra(errs.ErrUpstreamError, gin.H{"upstreamStatus": upstreamStatus}))
	}
}

// isTimeout 判断错误是否为超时：除 context.DeadlineExceeded 外，还按错误文本兜底识别，
// 覆盖上游把超时包成普通错误返回的情况。
func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "timeout") || strings.Contains(message, "deadline exceeded")
}

// channelsFor 加载模型目录绑定的渠道与对应 provider 实例，作为故障转移的尝试顺序。
func (h *AIHandler) channelsFor(catalogItem model.ModelCatalog) ([]service.ChannelWithProvider, error) {
	var ids []uuid.UUID
	if len(catalogItem.ChannelIDs) > 0 {
		if err := json.Unmarshal(catalogItem.ChannelIDs, &ids); err != nil {
			return nil, fmt.Errorf("模型渠道绑定不合法: %w", err)
		}
	}
	return h.upstream.LoadChannels(context.Background(), ids)
}

// loadInlineMedia 按 storageKey 从媒体存储读取参考素材。
// 传别人的 storageKey 一律按不存在处理，与媒体接口的约定一致。
func (h *AIHandler) loadInlineMedia(userID uuid.UUID, keys []string) ([]provider.InlineMedia, error) {
	media := make([]provider.InlineMedia, 0, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		var file model.MediaFile
		if err := h.db.Where("user_id = ? AND storage_key = ?", userID, key).First(&file).Error; err != nil {
			return nil, fmt.Errorf("参考素材不存在: %s", key)
		}
		reader, err := h.store.Get(context.Background(), file.ObjectPath)
		if err != nil {
			return nil, fmt.Errorf("读取参考素材失败: %w", err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			return nil, fmt.Errorf("读取参考素材失败: %w", err)
		}
		media = append(media, provider.InlineMedia{Data: data, MimeType: file.MimeType, Name: file.StorageKey})
	}
	return media, nil
}

func (h *AIHandler) maxFileBytes(userID uuid.UUID) (int64, error) {
	plan, err := service.PlanDefFor(context.Background(), h.db, userID)
	if err != nil {
		return 0, err
	}
	return plan.MaxFileBytes, nil
}

// availableMicros 返回可用余额（购买 + 赠送），账户不存在按 0 处理。
func (h *AIHandler) availableMicros(userID uuid.UUID) (int64, error) {
	account, err := h.billing.Balance(context.Background(), userID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return account.PurchasedMicros + account.GrantedMicros, nil
}

// creditsPayload 组装响应里的点数消耗块，remaining 为响应时的剩余余额。
func (h *AIHandler) creditsPayload(request *model.AIRequest, remaining int64) gin.H {
	payload := gin.H{
		"baseCostMicros":  request.BaseCostMicros,
		"finalCostMicros": request.FinalCostMicros,
		"finalCostYuan":   microsToYuan(request.FinalCostMicros),
		"remainingMicros": remaining,
	}
	if request.PromotionID != nil {
		discount := gin.H{"promotionId": request.PromotionID.String(), "discountBps": 0}
		payload["discount"] = discount
	} else {
		payload["discount"] = nil
	}
	return payload
}

func requireIdempotencyKey(c *gin.Context, key string) bool {
	if strings.TrimSpace(key) == "" {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"idempotencyKey": "生成接口必须携带 idempotencyKey"}))
		return false
	}
	return true
}

// sessionIDOf 清洗客户端会话标识：去首尾空白、按字符截断到 64，空白返回 nil。
func sessionIDOf(raw string) *string {
	id := clipRunes(strings.TrimSpace(raw), 64)
	if id == "" {
		return nil
	}
	return &id
}

// clipRunes 按字符数截断字符串，避免超长客户端输入撑爆 varchar 列。
func clipRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n])
	}
	return s
}

// paramsSnapshot 把报价参数序列化成 JSON 快照，空参数返回 nil。
func paramsSnapshot(params map[string]string) datatypes.JSON {
	if len(params) == 0 {
		return nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil
	}
	return raw
}

// stringifyParams 把报价参数压平成字符串；键 n 由调用方单独取出作为张数计价，不进参数串。
func stringifyParams(params map[string]any) map[string]string {
	out := make(map[string]string, len(params))
	for key, value := range params {
		if key == "n" {
			continue
		}
		out[key] = fmt.Sprint(value)
	}
	return out
}

func parseIntDefault(value any, fallback int) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case string:
		if parsed, err := strconv.Atoi(typed); err == nil {
			return parsed
		}
	}
	return fallback
}

func microsToYuan(micros int64) string {
	return strconv.FormatFloat(float64(micros)/1_000_000, 'f', 2, 64)
}

func randomKey() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:21]
}

// randomStorageID 给生成产物分配 storageKey 的对象段。
func RandomStorageID() string { return randomKey() }

func defaultMime(prefix string) string {
	switch prefix {
	case "image":
		return "image/png"
	case "video":
		return "video/mp4"
	case "audio":
		return "audio/mpeg"
	default:
		return "application/octet-stream"
	}
}
