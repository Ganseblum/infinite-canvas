package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/httpx"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/moderation"
	"github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/storage"
)

// SetModeration 注入审核服务，启用管理端的复核、预览与统计接口。
func (h *AdminHandler) SetModeration(moderation *service.ModerationService) {
	h.moderation = moderation
}

// SetSettings 注入站点设置服务，启用系统设置、社区与活动开关。
func (h *AdminHandler) SetSettings(settings *service.SiteSettingService) {
	h.settings = settings
}

var moderationSorts = map[string]string{
	"createdAt": "created_at",
	"decision":  "decision",
}

// ListModerationRecords 按阶段、结论、标签、复核状态、用户与时间筛选审核记录。
func (h *AdminHandler) ListModerationRecords(c *gin.Context) {
	params, ok := httpx.ParsePageParams(c)
	if !ok {
		return
	}
	sortColumn, ok := httpx.ParseSort(c.Query("sort"), moderationSorts, "-createdAt")
	if !ok {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"sort": "sort 不在白名单内"}))
		return
	}
	query := h.db.Model(&model.ModerationRecord{})
	if stage := c.Query("stage"); stage != "" {
		query = query.Where("stage = ?", stage)
	}
	if decision := c.Query("decision"); decision != "" {
		query = query.Where("decision = ?", decision)
	}
	if reviewStatus := c.Query("reviewStatus"); reviewStatus != "" {
		query = query.Where("review_status = ?", reviewStatus)
	}
	if raw := c.Query("userId"); raw != "" {
		userID, err := uuid.Parse(raw)
		if err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"userId": "userId 不合法"}))
			return
		}
		query = query.Where("user_id = ?", userID)
	}
	if label := c.Query("label"); label != "" {
		// risk_labels 是 JSON 数组，用 LIKE 做包含匹配即可满足筛选需求。
		query = query.Where("risk_labels LIKE ?", "%\""+strings.TrimSpace(label)+"\"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var records []model.ModerationRecord
	if err := query.Order(sortColumn).Order("id ASC").
		Offset((params.Page - 1) * params.Size).Limit(params.Size).
		Find(&records).Error; err != nil {
		slog.Error("查询审核记录失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]gin.H, 0, len(records))
	for i := range records {
		items = append(items, moderationRecordPayload(&records[i]))
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": params.Page, "size": params.Size})
}

// GetModerationRecord 返回审核详情；隔离原件仍有效时给出短期预览信息。
func (h *AdminHandler) GetModerationRecord(c *gin.Context) {
	record, ok := h.loadModerationRecord(c)
	if !ok {
		return
	}
	payload := moderationRecordPayload(record)
	payload["providerResult"] = json.RawMessage(record.ProviderResult)
	payload["riskLabels"] = json.RawMessage(record.RiskLabels)
	if record.QuarantineKey != "" {
		if record.QuarantineExpiresAt != nil && record.QuarantineExpiresAt.Before(time.Now()) {
			payload["quarantine"] = gin.H{"available": false, "reason": "隔离期已过，原件已删除"}
		} else {
			preview, expiresAt, err := h.moderation.Quarantine().PreviewURL(c.Request.Context(), record.UserID, record.QuarantineKey)
			if err != nil {
				payload["quarantine"] = gin.H{"available": false, "reason": "隔离原件不可读"}
			} else {
				// 不在响应里缓存预览地址；管理员页面拿到后短期使用。
				c.Header("Cache-Control", "no-store")
				payload["quarantine"] = gin.H{
					"available":   true,
					"previewUrl":  preview,
					"previewPath": "/api/admin/moderation/records/" + record.ID.String() + "/preview",
					"expiresAt":   httpx.FormatTime(expiresAt),
					"expiresAtAt": httpx.FormatTimePtr(record.QuarantineExpiresAt),
				}
			}
		}
	} else {
		payload["quarantine"] = gin.H{"available": false, "reason": "无隔离原件"}
	}
	c.JSON(http.StatusOK, gin.H{"record": payload})
}

// PreviewModerationArtifact 回流失效前直接返回隔离原件，仅管理员可访问且禁止缓存。
func (h *AdminHandler) PreviewModerationArtifact(c *gin.Context) {
	record, ok := h.loadModerationRecord(c)
	if !ok {
		return
	}
	if record.QuarantineKey == "" || (record.QuarantineExpiresAt != nil && record.QuarantineExpiresAt.Before(time.Now())) {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	data, err := h.moderation.Quarantine().Get(c.Request.Context(), record.UserID, record.QuarantineKey)
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Type", "application/octet-stream")
	c.Data(http.StatusOK, "application/octet-stream", data)
}

type moderationReviewReq struct {
	Decision string `json:"decision"`
	Note     string `json:"note"`
	Revision int    `json:"revision"`
}

// ReviewModerationRecord 提交人工结论。revision 防止两个管理员互相覆盖。
func (h *AdminHandler) ReviewModerationRecord(c *gin.Context) {
	record, ok := h.loadModerationRecord(c)
	if !ok {
		return
	}
	var req moderationReviewReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if (req.Decision != "approved" && req.Decision != "rejected") || strings.TrimSpace(req.Note) == "" {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"decision": "decision 只能是 approved 或 rejected，且必须填写说明"}))
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	now := time.Now()
	result := h.db.Model(&model.ModerationRecord{}).
		Where("id = ? AND review_revision = ?", record.ID, req.Revision).
		Updates(map[string]any{
			"review_status":   req.Decision,
			"reviewed_by":     actorID,
			"review_note":     req.Note,
			"reviewed_at":     now,
			"review_revision": gorm.Expr("review_revision + 1"),
		})
	if result.Error != nil {
		slog.Error("复核审核记录失败", "err", result.Error)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if result.RowsAffected == 0 {
		errs.Abort(c, errs.ErrModerationReviewed)
		return
	}

	// 人工通过时把隔离原件释放到正式存储；已过期则明确提示无法恢复。
	released := false
	releaseError := ""
	if req.Decision == "approved" && record.QuarantineKey != "" {
		if record.QuarantineExpiresAt != nil && record.QuarantineExpiresAt.Before(now) {
			releaseError = "隔离期已过，原件已删除"
		} else if err := h.releaseQuarantined(record); err != nil {
			releaseError = "释放隔离原件失败"
			slog.Error("释放隔离原件失败", "record", record.ID, "err", err)
		} else {
			released = true
		}
	}
	_ = h.audit.Record(h.db, actorID, "moderation.review", "moderation_record", record.ID.String(), c.GetString("request_id"), req.Note,
		gin.H{"reviewStatus": record.ReviewStatus, "revision": record.ReviewRevision},
		gin.H{"reviewStatus": req.Decision, "revision": record.ReviewRevision + 1})
	c.JSON(http.StatusOK, gin.H{"released": released, "releaseError": releaseError})
}

// releaseQuarantined 把隔离原件提交到正式存储，并回填 media_files 与生成记录。
// 水印归属（评审 E-4）：生成产物（stage=artifact）按物主档位烧录水印，上传件
// （stage=upload）不烧录。生成件与生成链路同 fail-closed 口径：水印或干净原件写入
// 失败即释放失败，隔离原件保留待人工重试，绝不落无水印字节。
func (h *AdminHandler) releaseQuarantined(record *model.ModerationRecord) error {
	data, err := h.moderation.Quarantine().Get(context.Background(), record.UserID, record.QuarantineKey)
	if err != nil {
		return err
	}
	prefix := "image"
	if record.ContentType == "video" {
		prefix = "video"
	} else if record.ContentType == "audio" {
		prefix = "audio"
	}
	mimeType := "application/octet-stream"
	if record.MediaFileID != nil {
		var file model.MediaFile
		if err := h.db.First(&file, "id = ?", record.MediaFileID).Error; err == nil {
			mimeType = file.MimeType
		}
	}
	if mimeType == "application/octet-stream" {
		// 上传被拒时 media_files 行尚未写入，按字节探测真实类型，探测不出再按前缀兜底。
		if detected := http.DetectContentType(data); detected != "application/octet-stream" {
			mimeType = detected
		}
	}
	if mimeType == "application/octet-stream" {
		if prefix == "video" {
			mimeType = "video/mp4"
		} else if prefix == "audio" {
			mimeType = "audio/mpeg"
		} else {
			mimeType = "image/png"
		}
	}
	storageKey := prefix + ":" + randomStorageID()
	saveData := data
	origPath := ""
	if record.Stage == string(moderation.StageArtifact) && h.wm != nil && h.wmEnabled() {
		planID, _, err := h.membership.ActivePlan(context.Background(), record.UserID, time.Now())
		if err != nil {
			slog.Error("watermark_failed", "kind", "release", "record", record.ID, "err", err)
			return fmt.Errorf("读取水印档位失败: %w", err)
		}
		if planID != "paid" {
			var wmBytes []byte
			var wmMime string
			switch prefix {
			case "image":
				wmBytes, wmMime, err = h.wm.Image(data, mimeType)
			case "video":
				wmBytes, err = h.wm.Video(context.Background(), data, mimeType)
				wmMime = mimeType
			default:
				// 音频等没有水印能力的类型直接落原件。
			}
			if err != nil {
				slog.Error("watermark_failed", "kind", "release", "record", record.ID, "err", err)
				return fmt.Errorf("释放水印烧录失败: %w", err)
			}
			if wmBytes != nil {
				// 与生成落盘同序：先写干净原件再落正式对象，失败补偿删除，避免可下发窗口。
				origPath = storage.OrigPath(record.UserID.String(), storageKey)
				if _, _, err := h.store.Put(context.Background(), origPath, bytes.NewReader(data), mimeType); err != nil {
					slog.Error("watermark_failed", "kind", "release", "record", record.ID, "err", err)
					return fmt.Errorf("留存干净原件失败: %w", err)
				}
				saveData = wmBytes
				mimeType = wmMime
			}
		}
	}
	file, err := h.media.SaveWithGeneration(context.Background(), service.SaveGeneratedMediaInput{
		UserID:     record.UserID,
		StorageKey: storageKey,
		MimeType:   mimeType,
		Data:       saveData,
		Moderation: "passed",
	}, nil)
	if err != nil {
		if origPath != "" {
			if delErr := h.store.Delete(context.Background(), origPath); delErr != nil {
				slog.Error("补偿删除干净原件失败", "path", origPath, "err", delErr)
			}
		}
		return err
	}
	if record.GenerationID != nil {
		_ = h.db.Model(&model.Generation{}).Where("id = ?", record.GenerationID).
			Updates(map[string]any{"status": "success", "moderation_status": "passed"}).Error
	}
	_ = h.moderation.Quarantine().Delete(context.Background(), record.UserID, record.QuarantineKey)
	h.moderation.ClearQuarantineKey(context.Background(), record.ID)
	_ = file
	return nil
}

type moderationCompensateReq struct {
	AmountMicros int64  `json:"amountMicros"`
	Note         string `json:"note"`
}

// CompensateModeration 向 granted 桶补偿误判损失。同一记录重复补偿不重复到账。
func (h *AdminHandler) CompensateModeration(c *gin.Context) {
	record, ok := h.loadModerationRecord(c)
	if !ok {
		return
	}
	var req moderationCompensateReq
	if err := c.ShouldBindJSON(&req); err != nil || req.AmountMicros <= 0 || strings.TrimSpace(req.Note) == "" {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"amountMicros": "补偿金额必须大于 0，并填写说明"}))
		return
	}
	if record.ReviewStatus != "approved" {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"reviewStatus": "只有人工复核通过的记录可以补偿"}))
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	// 唯一业务 id 保证同一记录只补偿一次。
	refID := "moderation:" + record.ID.String()
	var existing int64
	h.db.Model(&model.CreditTransaction{}).Where("ref_type = ? AND ref_id = ?", "moderation", refID).Count(&existing)
	if existing > 0 {
		errs.Abort(c, errs.AddConflict("该审核记录已补偿"))
		return
	}
	var credit model.CreditAccount
	err := h.db.Transaction(func(tx *gorm.DB) error {
		updated, err := h.credits.Adjust(tx, record.UserID, billing.BucketGranted, req.AmountMicros, req.Note, actorID.String())
		if err != nil {
			return err
		}
		credit = updated
		// 补偿流水用审核记录 id 作为 ref_id，Rewr调整接口写的是 admin ref，这里补一条标记。
		if err := tx.Model(&model.CreditTransaction{}).
			Where("ref_type = ? AND ref_id = ? AND created_at = (SELECT MAX(created_at) FROM credit_transactions WHERE user_id = ?)",
				"admin", actorID.String(), record.UserID).
			Updates(map[string]any{"ref_type": "moderation", "ref_id": refID}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.ModerationRecord{}).Where("id = ?", record.ID).
			Update("compensated_micros", req.AmountMicros).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "moderation.compensate", "moderation_record", record.ID.String(), c.GetString("request_id"), req.Note,
			nil, gin.H{"amountMicros": req.AmountMicros})
	})
	if err != nil {
		if errors.Is(err, billing.ErrInsufficientCredits) {
			errs.Abort(c, errs.ErrInsufficientCredits)
			return
		}
		slog.Error("审核补偿失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"purchasedMicros": credit.PurchasedMicros,
		"grantedMicros":   credit.GrantedMicros,
	})
}

// ModerationStats 返回命中率、失败率、标签分布、复核积压与隔离区用量。
func (h *AdminHandler) ModerationStats(c *gin.Context) {
	if h.moderation == nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	stats, err := h.moderation.Stats(c.Request.Context())
	if err != nil {
		slog.Error("读取审核统计失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, stats)
}

func (h *AdminHandler) loadModerationRecord(c *gin.Context) (*model.ModerationRecord, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return nil, false
	}
	var record model.ModerationRecord
	if err := h.db.First(&record, "id = ?", id).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return nil, false
	}
	return &record, true
}

func moderationRecordPayload(record *model.ModerationRecord) gin.H {
	payload := gin.H{
		"id":                record.ID.String(),
		"userId":            record.UserID.String(),
		"stage":             record.Stage,
		"contentType":       record.ContentType,
		"contentHash":       record.ContentHash,
		"provider":          record.Provider,
		"providerRequestId": record.ProviderRequestID,
		"policyVersion":     record.PolicyVersion,
		"decision":          record.Decision,
		"riskLabels":        json.RawMessage(record.RiskLabels),
		"reviewStatus":      record.ReviewStatus,
		"reviewRevision":    record.ReviewRevision,
		"reviewNote":        record.ReviewNote,
		"compensatedMicros": record.CompensatedMicros,
		"createdAt":         httpx.FormatTime(record.CreatedAt),
	}
	if record.GenerationID != nil {
		payload["generationId"] = record.GenerationID.String()
	}
	if record.ReviewedBy != nil {
		payload["reviewedBy"] = record.ReviewedBy.String()
	}
	if record.ReviewedAt != nil {
		payload["reviewedAt"] = httpx.FormatTime(*record.ReviewedAt)
	}
	if record.QuarantineExpiresAt != nil {
		payload["quarantineExpiresAt"] = httpx.FormatTime(*record.QuarantineExpiresAt)
	}
	return payload
}

// randomStorageID 生成新的媒体存储键对象段（与 ai 域生成口径一致）。
func randomStorageID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:21]
}
