package handler

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/moderation"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/storage"
)

// storageKeyRe 与前端 storageKeyPattern 对齐并收紧字符集，不匹配一律 400。
var storageKeyRe = regexp.MustCompile(`^(image|video|audio|file|video-reference|audio-reference):[A-Za-z0-9_-]{1,64}$`)

// MediaHandler 媒体文件：HEAD/GET 读，PUT 经后端代理流式写入，DELETE 硬删除。
type MediaHandler struct {
	db         *gorm.DB
	storage    storage.Storage
	quota      *service.QuotaService
	moderation *service.ModerationService
}

func NewMediaHandler(db *gorm.DB, stor storage.Storage, moderation *service.ModerationService) *MediaHandler {
	return &MediaHandler{db: db, storage: stor, quota: service.NewQuotaService(db), moderation: moderation}
}

func (h *MediaHandler) Head(c *gin.Context) {
	file, ok := h.findOwned(c)
	if !ok {
		return
	}
	c.Header("Content-Length", strconv.FormatInt(file.Bytes, 10))
	c.Header("Content-Type", file.MimeType)
	c.Header("X-Checksum", file.Checksum)
	c.Status(http.StatusOK)
}

func (h *MediaHandler) Get(c *gin.Context) {
	file, ok := h.findOwned(c)
	if !ok {
		return
	}
	if h.storage.Kind() == "local" {
		h.serveLocal(c, file)
		return
	}
	h.redirectToPresigned(c, file)
}

// serveLocal 直接回流二进制：local 驱动没有签名问题，下发一年 immutable 与 ETag。
func (h *MediaHandler) serveLocal(c *gin.Context, file *model.MediaFile) {
	reader, err := h.storage.Get(c.Request.Context(), file.ObjectPath)
	if errors.Is(err, storage.ErrObjectNotFound) {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if err != nil {
		slog.Error("读取媒体对象失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	defer reader.Close()

	c.Header("Content-Type", file.MimeType)
	c.Header("ETag", `"`+file.Checksum+`"`)
	c.Header("Cache-Control", "private, max-age=31536000, immutable")
	c.Header("Content-Length", strconv.FormatInt(file.Bytes, 10))
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, reader); err != nil {
		slog.Warn("媒体响应写出中断", "err", err)
	}
}

// redirectToPresigned 302 到对齐整点的预签名 URL。这条重定向的缓存只能是
// private + 剩余秒数减 60，绝不能加 immutable：签名过期后浏览器不会回源重签，
// 全站图片会集体裂开。长缓存属于预签名 URL 指向的对象本身。
func (h *MediaHandler) redirectToPresigned(c *gin.Context, file *model.MediaFile) {
	presigned, err := h.storage.Presign(c.Request.Context(), file.ObjectPath)
	if err != nil {
		slog.Error("生成预签名 URL 失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	remaining := int(time.Until(presigned.ExpiresAt).Seconds()) - 60
	if remaining < 0 {
		remaining = 0
	}
	c.Header("Cache-Control", fmt.Sprintf("private, max-age=%d", remaining))
	c.Header("Location", presigned.URL)
	c.Status(http.StatusFound)
}

func (h *MediaHandler) Put(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		return
	}
	key, ok := validStorageKey(c)
	if !ok {
		return
	}
	contentType := strings.TrimSpace(c.GetHeader("Content-Type"))
	if contentType == "" {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"Content-Type": "上传必须携带 Content-Type"}))
		return
	}

	// 未验证邮箱禁止上传：只限制媒体写路径，画布/素材/生成记录不受影响。
	var user model.User
	if err := h.db.First(&user, "id = ?", uid).Error; err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return
	}
	if user.EmailVerifiedAt == nil {
		errs.Abort(c, errs.ErrEmailNotVerif)
		return
	}

	now := time.Now()
	_, plan, err := h.quota.DerivePlan(c.Request.Context(), uid, now)
	if err != nil {
		slog.Error("读取档位失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	maxBytes := plan.MaxFileBytes
	if c.Request.ContentLength > maxBytes {
		errs.Abort(c, errs.ErrFileTooLarge)
		return
	}

	// 配额校验先于写盘：重复 PUT 是覆盖语义，计数的增量是「新文件大小 - 旧文件大小」。
	used, err := h.quota.StorageBytes(c.Request.Context(), uid)
	if err != nil {
		slog.Error("读取存储用量失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	baseline := used
	var existing model.MediaFile
	existingErr := h.db.Where("user_id = ? AND storage_key = ?", uid, key).First(&existing).Error
	if existingErr == nil {
		baseline = used - existing.Bytes
	} else if !errors.Is(existingErr, gorm.ErrRecordNotFound) {
		slog.Error("查询媒体索引失败", "err", existingErr)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if err := service.CheckUpload(baseline, c.Request.ContentLength, plan.StorageBytes); err != nil {
		h.abortStorageError(c, err, plan, baseline, c.Request.ContentLength)
		return
	}

	objectPath := mediaObjectPath(uid, key)
	body := http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)

	// 审核开启时先流式写入隔离区，审核通过后再提交正式对象；拒绝不污染正式配额。
	moderating := h.moderation != nil && h.moderation.Enabled()
	if moderating {
		raw, readErr := io.ReadAll(body)
		if readErr != nil {
			errs.Abort(c, errs.ErrFileTooLarge)
			return
		}
		if _, _, putErr := h.quarantineUpload(c, uid, raw, contentType, key, maxBytes); putErr != nil {
			h.abortUploadError(c, putErr)
			return
		}
		return
	}

	written, checksum, err := h.storage.Put(c.Request.Context(), objectPath, body, contentType)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) || written >= maxBytes {
			errs.Abort(c, errs.ErrFileTooLarge)
			return
		}
		slog.Error("写入媒体对象失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if written > maxBytes {
		_ = h.storage.Delete(c.Request.Context(), objectPath)
		errs.Abort(c, errs.ErrFileTooLarge)
		return
	}
	// 服务端始终自行计算 sha256，X-Checksum 只用于比对；不一致时既不写库也不留文件。
	if expected := strings.TrimSpace(c.GetHeader("X-Checksum")); expected != "" && !strings.EqualFold(expected, checksum) {
		_ = h.storage.Delete(c.Request.Context(), objectPath)
		errs.Abort(c, errs.ErrChecksumMismatch)
		return
	}

	file := model.MediaFile{
		ID:               uuid.New(),
		UserID:           uid,
		StorageKey:       key,
		ObjectPath:       objectPath,
		MimeType:         contentType,
		Bytes:            written,
		Checksum:         checksum,
		ModerationStatus: "skipped",
		CreatedAt:        time.Now(),
	}
	// 重复 PUT 是覆盖语义：唯一约束 (user_id, storage_key) 更新原行，不新增记录。
	// 媒体行与存储计数在同一事务内更新，否则并发上传会导致计数漂移。
	if txErr := h.db.Transaction(func(tx *gorm.DB) error {
		var previous model.MediaFile
		delta := written
		if err := tx.Where("user_id = ? AND storage_key = ?", uid, key).First(&previous).Error; err == nil {
			delta = written - previous.Bytes
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "storage_key"}},
			DoUpdates: clause.AssignmentColumns([]string{"object_path", "mime_type", "bytes", "checksum", "moderation_status"}),
		}).Create(&file).Error; err != nil {
			return err
		}
		if delta != 0 {
			if _, err := h.quota.AddUsage(tx, uid, service.MetricStorageBytes, delta); err != nil {
				return err
			}
		}
		return nil
	}); txErr != nil {
		_ = h.storage.Delete(c.Request.Context(), objectPath)
		slog.Error("写入媒体索引失败", "err", txErr)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"storageKey": key,
		"bytes":      written,
		"checksum":   checksum,
		"mimeType":   contentType,
	})
}

func (h *MediaHandler) Delete(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		return
	}
	key, ok := validStorageKey(c)
	if !ok {
		return
	}
	var file model.MediaFile
	err := h.db.Where("user_id = ? AND storage_key = ?", uid, key).First(&file).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		noContent(c)
		return
	}
	if err != nil {
		slog.Error("查询媒体索引失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if err := h.storage.Delete(c.Request.Context(), file.ObjectPath); err != nil {
		slog.Error("删除媒体对象失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	// media_files 走硬删除：对象已经删除，留一行记录没有意义。计数在同一事务内回退。
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", file.ID).Delete(&model.MediaFile{}).Error; err != nil {
			return err
		}
		if _, err := h.quota.AddUsage(tx, uid, service.MetricStorageBytes, -file.Bytes); err != nil {
			return err
		}
		return nil
	}); err != nil {
		slog.Error("删除媒体索引失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	noContent(c)
}

// quarantineUpload 处理开启审核时的上传：写隔离区、送审、通过后提交正式对象并计配额。
// 拒绝时不写 media_files、不增加用量；同一 storageKey 重试幂等。
func (h *MediaHandler) quarantineUpload(c *gin.Context, uid uuid.UUID, raw []byte, contentType, key string, maxBytes int64) (int64, string, error) {
	quarantine, err := h.moderation.Quarantine().Put(c.Request.Context(), uid, raw, contentType)
	if err != nil {
		slog.Error("写入隔离区失败", "err", err)
		return 0, "", err
	}
	_, err = h.moderation.CheckArtifact(c.Request.Context(), uid, moderation.ContentType("image"), quarantine.Key, raw, contentType)
	if err != nil {
		if !errors.Is(err, service.ErrContentRejected) {
			// 拒绝时保留隔离原件供人工复核；其他失败清掉临时对象。
			_ = h.moderation.Quarantine().Delete(c.Request.Context(), uid, quarantine.Key)
		}
		return 0, "", err
	}

	objectPath := mediaObjectPath(uid, key)
	written, checksum, err := h.storage.Put(c.Request.Context(), objectPath, bytes.NewReader(raw), contentType)
	if err != nil {
		slog.Error("提交正式对象失败", "err", err)
		return 0, "", err
	}
	if written > maxBytes {
		_ = h.storage.Delete(c.Request.Context(), objectPath)
		return 0, "", errs.ErrFileTooLarge
	}
	if expected := strings.TrimSpace(c.GetHeader("X-Checksum")); expected != "" && !strings.EqualFold(expected, checksum) {
		_ = h.storage.Delete(c.Request.Context(), objectPath)
		return 0, "", errs.ErrChecksumMismatch
	}

	file := model.MediaFile{
		ID:               uuid.New(),
		UserID:           uid,
		StorageKey:       key,
		ObjectPath:       objectPath,
		MimeType:         contentType,
		Bytes:            written,
		Checksum:         checksum,
		ModerationStatus: "passed",
		CreatedAt:        time.Now(),
	}
	if txErr := h.db.Transaction(func(tx *gorm.DB) error {
		var previous model.MediaFile
		delta := written
		if err := tx.Where("user_id = ? AND storage_key = ?", uid, key).First(&previous).Error; err == nil {
			delta = written - previous.Bytes
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "storage_key"}},
			DoUpdates: clause.AssignmentColumns([]string{"object_path", "mime_type", "bytes", "checksum", "moderation_status"}),
		}).Create(&file).Error; err != nil {
			return err
		}
		if delta != 0 {
			if _, err := h.quota.AddUsage(tx, uid, service.MetricStorageBytes, delta); err != nil {
				return err
			}
		}
		return nil
	}); txErr != nil {
		_ = h.storage.Delete(c.Request.Context(), objectPath)
		slog.Error("写入媒体索引失败", "err", txErr)
		return 0, "", txErr
	}
	// 正式对象已经可用，清掉隔离原件。
	_ = h.moderation.Quarantine().Delete(c.Request.Context(), uid, quarantine.Key)
	c.JSON(http.StatusCreated, gin.H{
		"storageKey": key,
		"bytes":      written,
		"checksum":   checksum,
		"mimeType":   contentType,
	})
	return written, checksum, nil
}

// abortUploadError 把审查/写入错误映射成响应。
func (h *MediaHandler) abortUploadError(c *gin.Context, err error) {
	if errors.Is(err, errs.ErrFileTooLarge) || errors.Is(err, errs.ErrChecksumMismatch) {
		return
	}
	if errors.Is(err, service.ErrContentRejected) {
		errs.Abort(c, errs.ErrContentRejected)
		return
	}
	if errors.Is(err, service.ErrModerationUnavailable) {
		errs.Abort(c, errs.ErrModerationUnavailable)
		return
	}
	errs.Abort(c, errs.ErrInternal)
}

// abortStorageError 写上传配额错误响应，响应体带当前档位、已用量与上限，
// 让前端区分「该充值」和「该清理」。
func (h *MediaHandler) abortStorageError(c *gin.Context, err error, plan model.Plan, used, incoming int64) {
	if errors.Is(err, service.ErrReadOnly) {
		errs.Abort(c, errs.WithExtra(errs.ErrReadOnly, gin.H{
			"planId": plan.ID,
			"used":   used,
			"limit":  plan.StorageBytes,
		}))
		return
	}
	errs.Abort(c, errs.WithExtra(errs.ErrStorageQuota, gin.H{
		"used":  used + incoming,
		"limit": plan.StorageBytes,
	}))
}

func (h *MediaHandler) findOwned(c *gin.Context) (*model.MediaFile, bool) {
	uid, ok := currentUserID(c)
	if !ok {
		return nil, false
	}
	key, ok := validStorageKey(c)
	if !ok {
		return nil, false
	}
	var file model.MediaFile
	err := h.db.Where("user_id = ? AND storage_key = ?", uid, key).First(&file).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 跨用户访问与不存在都返回 404，避免探测他人 storageKey 是否存在。
		errs.Abort(c, errs.ErrNotFound)
		return nil, false
	}
	if err != nil {
		slog.Error("查询媒体索引失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return nil, false
	}
	return &file, true
}

func validStorageKey(c *gin.Context) (string, bool) {
	key := c.Param("storageKey")
	if !storageKeyRe.MatchString(key) {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"storageKey": "storageKey 格式不合法"}))
		return "", false
	}
	return key, true
}

// mediaObjectPath 由服务端按「用户 id/类型段/对象 id」重新组装安全路径，
// storageKey 里的任何字符都不会直接进入文件路径。
func mediaObjectPath(uid uuid.UUID, key string) string {
	prefix, id, _ := strings.Cut(key, ":")
	return uid.String() + "/" + prefix + "/" + id
}
