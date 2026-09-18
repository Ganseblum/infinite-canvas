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

// allowedUploadTypes 是上传允许的声明类型清单，与前端上传入口实际产出的类型对齐，
// 超出清单一律 400，文本、网页等非媒体类型进不了媒体存储。
// application/octet-stream 是前端导入链路对无类型文件的回退，其内容仍受嗅探家族校验约束。
var allowedUploadTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true,
	"video/mp4": true, "video/webm": true,
	"audio/mpeg": true, "audio/wav": true, "audio/x-wav": true, "audio/ogg": true,
	"application/octet-stream": true,
}

func allowedUploadType(t string) bool {
	t = strings.ToLower(strings.TrimSpace(strings.SplitN(t, ";", 2)[0]))
	return allowedUploadTypes[t]
}

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
	c.Header("X-Content-Type-Options", "nosniff")
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
	// nosniff 配合上传侧的类型嗅探校验：即使内容被伪装成图片，浏览器也不会猜测类型执行。
	c.Header("X-Content-Type-Options", "nosniff")
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
	// 与本地直读路径同口径：nosniff 统一媒体读取响应的安全策略。
	c.Header("X-Content-Type-Options", "nosniff")
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
	if !allowedUploadType(contentType) {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"Content-Type": "不支持的媒体类型"}))
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

	objectPath := storage.ObjectPath(uid.String(), key)
	rawBody := http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	head := make([]byte, sniffHeadLen)
	n, _ := io.ReadFull(rawBody, head)
	// 上传类型不再信任客户端声明：按文件头嗅探实际类型，主动内容与家族不符一律拒绝。
	// 公开读路径（社区作品）引入前必须先堵住存储型 XSS 面。
	if err := validateUploadType(contentType, http.DetectContentType(head[:n])); err != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"Content-Type": "Content-Type 与文件内容不符"}))
		return
	}
	body := io.Reader(io.MultiReader(bytes.NewReader(head[:n]), rawBody))

	// 审核开启时先流式写入隔离区，审核通过后再提交正式对象；拒绝不污染正式配额。
	moderating := h.moderation != nil && h.moderation.Enabled()
		if moderating {
			raw, readErr := io.ReadAll(body)
			if readErr != nil {
				errs.Abort(c, errs.ErrFileTooLarge)
				return
			}
			// 分块传输没有 ContentLength，预检失效：按实际字节数复核总用量，超限 507。
			if c.Request.ContentLength < 0 {
				if err := service.CheckUpload(baseline, int64(len(raw)), plan.StorageBytes); err != nil {
					h.abortStorageError(c, err, plan, baseline, int64(len(raw)))
					return
				}
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
	// 分块传输没有 ContentLength，预检失效：写完后按实际字节数复核总用量，
	// 超限则删除已写对象并返回 507（响应结构与预检一致），不让配额被绕过。
	if c.Request.ContentLength < 0 {
		if err := service.CheckUpload(baseline, written, plan.StorageBytes); err != nil {
			_ = h.storage.Delete(c.Request.Context(), objectPath)
			h.abortStorageError(c, err, plan, baseline, written)
			return
		}
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
	verdict, err := h.moderation.CheckArtifact(c.Request.Context(), uid, moderation.ContentType("image"), quarantine.Key, raw, contentType)
	h.moderation.SetQuarantineBytes(c.Request.Context(), verdict.RecordID, quarantine.Bytes)
	if err != nil {
		if !errors.Is(err, service.ErrContentRejected) {
			// 拒绝时保留隔离原件供人工复核；其他失败清掉临时对象。
			_ = h.moderation.Quarantine().Delete(c.Request.Context(), uid, quarantine.Key)
		}
		return 0, "", err
	}

	objectPath := storage.ObjectPath(uid.String(), key)
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
	// 这两类必须在响应中回给客户端：此前直接 return 会留下「200 + 空响应体」，
	// 前端把它当成功解析 JSON，用户只看到无意义的解析错误。
	if errors.Is(err, errs.ErrFileTooLarge) {
		errs.Abort(c, errs.ErrFileTooLarge)
		return
	}
	if errors.Is(err, errs.ErrChecksumMismatch) {
		errs.Abort(c, errs.ErrChecksumMismatch)
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
		// 社区作品对全部浏览者只读可见（差异清单 #31）：key 属于已发布作品的
		// 内容本体或封面时，返回作者名下的那一行。只放行这一条只读分支，
		// 不放开整体归属校验，否则引入 IDOR。
		if pub, perr := h.publishedCommunityMedia(key); perr == nil && pub != nil {
			return pub, true
		}
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

// publishedCommunityMedia 返回已发布且审核放行的社区作品（内容本体或封面）对应的
// 作者媒体行；不属于任何已发布作品时返回 nil。同 storageKey 可能存在于多个用户名下，
// 因此必须在同一条查询里把媒体行限定到作品作者。源素材已软删的作品不进公开流
// （community 侧已过滤），媒体只读放行保持同一口径，避免公开流外仍可读残留。
func (h *MediaHandler) publishedCommunityMedia(key string) (*model.MediaFile, error) {
	var file model.MediaFile
	err := h.db.Raw(`
		SELECT mf.* FROM media_files mf
		JOIN assets a ON a.user_id = mf.user_id AND a.storage_key = mf.storage_key AND a.deleted_at IS NULL
		JOIN community_works cw ON cw.asset_id = a.id
		WHERE mf.storage_key = ? AND cw.status = 'published' AND cw.moderation_ok = 1
		UNION
		SELECT mf.* FROM media_files mf
		JOIN community_works cw ON cw.user_id = mf.user_id AND cw.cover_key = mf.storage_key
		JOIN assets a ON a.id = cw.asset_id AND a.deleted_at IS NULL
		WHERE mf.storage_key = ? AND cw.status = 'published' AND cw.moderation_ok = 1
		LIMIT 1`, key, key).Scan(&file).Error
	if err != nil {
		return nil, err
	}
	if file.ID == uuid.Nil {
		return nil, nil
	}
	return &file, nil
}

// sniffHeadLen 是嗅探上传实际类型所需的文件头字节数。
const sniffHeadLen = 512

// validateUploadType 校验声明的 Content-Type 与实际内容一致且不含主动执行内容。
// 服务端不下发 text/html 一类可执行类型，且媒体响应统一带 nosniff，
// 消除「上传完全信任客户端 Content-Type」的存储型 XSS 面。
func validateUploadType(declared, sniffed string) error {
	sniffed = strings.TrimSpace(strings.SplitN(sniffed, ";", 2)[0])
	switch sniffed {
	case "application/octet-stream":
		// 二进制特征不在嗅探表内（avif/heic/mov 等），无法核验，放行交给nosniff兜底。
		return nil
	case "application/x-empty", "":
		return errors.New("上传内容为空")
	}
	if sniffed == "text/html" || sniffed == "application/xhtml+xml" {
		return errors.New("不允许上传网页类内容")
	}
	if strings.HasPrefix(declared, "image/svg") {
		// SVG 内嵌脚本在直接导航时可执行，一律拒绝。
		return errors.New("不允许上传 SVG")
	}
	df, sf := mediaFamily(declared), mediaFamily(sniffed)
	if df != "other" && sf != "other" && df != sf {
		return errors.New("Content-Type 与文件内容不符")
	}
	return nil
}

// mediaFamily 把类型归入 image/video/audio/text 大类，无法归类返回 other。
func mediaFamily(t string) string {
	switch {
	case strings.HasPrefix(t, "image/"):
		return "image"
	case strings.HasPrefix(t, "video/"):
		return "video"
	case strings.HasPrefix(t, "audio/"):
		return "audio"
	case strings.HasPrefix(t, "text/"):
		return "text"
	default:
		return "other"
	}
}

func validStorageKey(c *gin.Context) (string, bool) {
	key := c.Param("storageKey")
	if !storageKeyRe.MatchString(key) {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"storageKey": "storageKey 格式不合法"}))
		return "", false
	}
	return key, true
}
