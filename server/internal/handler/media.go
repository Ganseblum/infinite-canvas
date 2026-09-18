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
	"github.com/infinite-canvas/server/internal/watermark"
)

// storageKeyRe 与前端 storageKeyPattern 对齐并收紧字符集，不匹配一律 400。
var storageKeyRe = regexp.MustCompile(`^(image|video|audio|file|video-reference|audio-reference):[A-Za-z0-9_-]{1,64}$`)

// origPresignTTL 是干净原件 S3 直链的预签名有效期（计划冻结的边界值 360s）。
const origPresignTTL = 360 * time.Second

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
// 下发按「归属者当前档位」选字节：免费档出水印版主对象，付费档存在干净原件时
// 出原件；secret 用于「申请下载」取件令牌的 HMAC 签名，wm 供生成链路（T5）复用。
type MediaHandler struct {
	db         *gorm.DB
	storage    storage.Storage
	quota      *service.QuotaService
	moderation *service.ModerationService
	secret     []byte
	wm         *watermark.Service
}

func NewMediaHandler(db *gorm.DB, stor storage.Storage, moderation *service.ModerationService, secret []byte, wm *watermark.Service) *MediaHandler {
	return &MediaHandler{db: db, storage: stor, quota: service.NewQuotaService(db), moderation: moderation, secret: secret, wm: wm}
}

func (h *MediaHandler) Head(c *gin.Context) {
	file, ok := h.findOwned(c)
	if !ok {
		return
	}
	sel, ok := h.selectBytes(c, file)
	if !ok {
		return
	}
	// Head 与 Get 完全同口径：同一套选字节与缓存头，出 orig 时 Content-Length 用 Stat 值。
	writeCacheHeaders(c, file, sel)
	// 出 orig 时字节类型可能与媒体行 mime 不一致（webp 源生成件的媒体行记水印版 mime），
	// Content-Type 必须按实际字节嗅探，与 Get/取件下发同口径。Head 无响应体，嗅探后即关闭。
	contentType := file.MimeType
	if sel.isOrig {
		reader, err := h.storage.Get(c.Request.Context(), sel.path)
		if errors.Is(err, storage.ErrObjectNotFound) {
			errs.Abort(c, errs.ErrNotFound)
			return
		}
		if err != nil {
			slog.Error("读取干净原件失败", "err", err)
			errs.Abort(c, errs.ErrInternal)
			return
		}
		contentType, _ = sniffHead(reader)
		_ = reader.Close()
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Length", strconv.FormatInt(sel.size, 10))
	c.Header("X-Content-Type-Options", "nosniff")
	// 行 checksum 属主对象字节、与 orig 字节不一致：出 orig 时省略优于错值。
	if !sel.isOrig {
		c.Header("X-Checksum", file.Checksum)
	}
	c.Status(http.StatusOK)
}

func (h *MediaHandler) Get(c *gin.Context) {
	file, ok := h.findOwned(c)
	if !ok {
		return
	}
	sel, ok := h.selectBytes(c, file)
	if !ok {
		return
	}
	if h.storage.Kind() == "local" {
		h.serveLocal(c, file, sel)
		return
	}
	h.redirectToPresigned(c, file, sel)
}

// writeCacheHeaders 按选字节结果写 ETag 与分级缓存头，Head/Get 共用保证同口径。
func writeCacheHeaders(c *gin.Context, file *model.MediaFile, sel *mediaBytes) {
	if sel.mutable {
		// 可变字节（免费档影像或付费档干净原件）：禁止长缓存，ETag 前缀区分字节代际，
		// 档位升降后浏览器必须回源拿新字节，杜绝缓存串档。
		c.Header("Cache-Control", "private, no-cache")
		c.Header("ETag", sel.etag(file.Checksum))
		return
	}
	c.Header("Cache-Control", "private, max-age=31536000, immutable")
	c.Header("ETag", sel.etag(file.Checksum))
}

// serveLocal 直接回流二进制。缓存头按选字节结果分级：可变字节 private,no-cache +
// wm-/orig- ETag 并支持 If-None-Match 304；稳定字节维持一年 immutable。
// 出 orig 时字节类型可能与媒体行 mime 不一致（webp 源生成件的媒体行记水印版 mime），
// Content-Type 按文件头嗅探下发，口径与 serveOrigLocal 一致（评审 E-1）。
func (h *MediaHandler) serveLocal(c *gin.Context, file *model.MediaFile, sel *mediaBytes) {
	writeCacheHeaders(c, file, sel)
	if !sel.isOrig {
		// 主对象媒体行 mime 即权威，304 响应同样携带。
		c.Header("Content-Type", file.MimeType)
	}
	// nosniff 配合上传侧的类型嗅探校验：即使内容被伪装成图片，浏览器也不会猜测类型执行。
	c.Header("X-Content-Type-Options", "nosniff")
	// If-None-Match 命中时不读文件体：304 不带 Content-Length，也不触发存储读。
	// orig 的嗅探放在判定之后：304 无响应体，不为嗅探多读一次存储（此时不带 Content-Type）。
	if matchETag(c.Request.Header.Get("If-None-Match"), sel.etag(file.Checksum)) {
		c.Status(http.StatusNotModified)
		return
	}
	c.Header("Content-Length", strconv.FormatInt(sel.size, 10))
	reader, err := h.storage.Get(c.Request.Context(), sel.path)
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
	// orig 按文件头嗅探实际类型下发，头部字节与剩余流拼接续传，不整体读入内存。
	var body io.Reader = reader
	if sel.isOrig {
		sniffed, head := sniffHead(reader)
		c.Header("Content-Type", sniffed)
		body = io.MultiReader(bytes.NewReader(head), reader)
	}
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, body); err != nil {
		slog.Warn("媒体响应写出中断", "err", err)
	}
}

// redirectToPresigned 302 到预签名 URL。选字节为干净原件时改签 orig 路径并缩短 TTL
// （干净件直链必须短时效），302 自身的缓存语义维持现状：private + 剩余秒数减 60，
// 绝不能加 immutable——签名过期后浏览器不会回源重签，全站图片会集体裂开。
// 长缓存属于预签名 URL 指向的对象本身。
func (h *MediaHandler) redirectToPresigned(c *gin.Context, file *model.MediaFile, sel *mediaBytes) {
	var (
		presigned storage.Presigned
		err       error
	)
	if sel.isOrig {
		presigned, err = h.storage.PresignWithTTL(c.Request.Context(), sel.path, origPresignTTL)
	} else {
		presigned, err = h.storage.Presign(c.Request.Context(), file.ObjectPath)
	}
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

// mediaBytes 是一次下发选出的字节来源与缓存口径。
type mediaBytes struct {
	path       string // 对象路径：主对象或干净原件
	size       int64  // 出 orig 时为 Stat 值，出主对象时为媒体行字节数
	mutable    bool   // true=可变字节：no-cache + 前缀 ETag；false=稳定字节：immutable
	etagPrefix string // mutable 时的 ETag 前缀，区分 wm-/orig- 字节代际
	isOrig     bool   // true=本次下发的是干净原件（S3 驱动据此走短时效预签名）
}

func (s *mediaBytes) etag(checksum string) string {
	if s.mutable {
		return `"` + s.etagPrefix + checksum + `"`
	}
	return `"` + checksum + `"`
}

// watermarkableMime 判定 mime 是否属于可能涉及水印/干净原件的影像类型（类型闸门）。
// 只有这些类型才需要查档位与 orig；audio/file 等其余类型永远走稳定字节。
// 媒体行的 mime 可能带参数后缀，统一去参数、小写后再比对。
func watermarkableMime(mimeType string) bool {
	mime := strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))
	switch mime {
	case "image/jpeg", "image/png", "image/webp", "image/gif", "video/mp4", "video/webm":
		return true
	}
	return false
}

// selectBytes 按媒体行归属者（file.UserID，绝不取浏览者）当前档位选择下发字节：
// 非付费档直接服务主对象（无需查 orig）；付费档存在干净原件时服务原件，
// HasOriginal 出错按 500 处理，绝不降级直出，宁可不可用也不泄漏干净字节。
func (h *MediaHandler) selectBytes(c *gin.Context, file *model.MediaFile) (*mediaBytes, bool) {
	sel := &mediaBytes{path: file.ObjectPath, size: file.Bytes}
	if !watermarkableMime(file.MimeType) {
		return sel, true
	}
	_, plan, err := h.quota.DerivePlan(c.Request.Context(), file.UserID, time.Now())
	if err != nil {
		slog.Error("读取媒体归属者档位失败", "err", err, "storageKey", file.StorageKey)
		errs.Abort(c, errs.ErrInternal)
		return nil, false
	}
	if plan.ID != "paid" {
		// 免费档的 image/video 一律视为可变字节：无论历史产物是否带水印，
		// 保守按「可能被水印替换」处理，缓存绝不长存。
		sel.mutable = true
		sel.etagPrefix = "wm-"
		return sel, true
	}
	// 付费归属者：存在干净原件才出原件。「确认不存在」的负缓存由 storage 层记录，
	// 前提是 orig 只在生成落盘时写入且 storageKey 每次生成为新 UUID（见 storage/orig.go）。
	has, err := storage.HasOriginal(c.Request.Context(), h.storage, file.UserID.String(), file.StorageKey)
	if err != nil {
		slog.Error("查询干净原件失败", "err", err, "storageKey", file.StorageKey)
		errs.Abort(c, errs.ErrInternal)
		return nil, false
	}
	if !has {
		return sel, true
	}
	origPath := storage.OrigPath(file.UserID.String(), file.StorageKey)
	size, err := h.storage.Stat(c.Request.Context(), origPath)
	if err != nil {
		// HasOriginal 刚确认存在而 Stat 失败属并发异常，fail-closed 返回 500。
		slog.Error("读取干净原件大小失败", "err", err, "storageKey", file.StorageKey)
		errs.Abort(c, errs.ErrInternal)
		return nil, false
	}
	return &mediaBytes{path: origPath, size: size, mutable: true, etagPrefix: "orig-", isOrig: true}, true
}

// matchETag 判定 If-None-Match 是否命中当前 ETag：支持 * 与逗号分隔列表，
// 值须与本服务下发的强 ETag（含引号）完全一致。
func matchETag(header, etag string) bool {
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	for _, part := range strings.Split(header, ",") {
		if strings.TrimSpace(part) == etag {
			return true
		}
	}
	return false
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
	var user model.PlatformUser
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
	// 覆盖写成功后主对象即权威，旧干净原件已成陈旧内容，best-effort 清除；
	// 清除失败仅记日志，不阻塞上传响应（残余孤儿由清理链路兜底）。
	if err := h.storage.Delete(c.Request.Context(), storage.OrigPath(uid.String(), key)); err != nil {
		slog.Error("覆盖上传后清理干净原件失败", "err", err, "storageKey", key)
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
	// 主对象已删，best-effort 补删干净原件：媒体行即将硬删，orig 留着只会成为孤儿。
	// 失败仅记日志不阻塞删除主流程（异常矩阵 D：Delete 幂等，残余孤儿见未决项）。
	if err := h.storage.Delete(c.Request.Context(), storage.OrigPath(file.UserID.String(), key)); err != nil {
		slog.Error("删除干净原件失败", "err", err, "storageKey", key)
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
	// 覆盖写成功后主对象即权威，旧干净原件 best-effort 清除，语义与直传路径一致。
	if err := h.storage.Delete(c.Request.Context(), storage.OrigPath(uid.String(), key)); err != nil {
		slog.Error("覆盖上传后清理干净原件失败", "err", err, "storageKey", key)
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

// sniffHead 从读取器读文件头（≤sniffHeadLen 字节）并按 http.DetectContentType 嗅探
// 实际 Content-Type。orig 对象无扩展名且字节类型可能与媒体行 mime 不一致（webp 源
// 生成件的媒体行记水印版 mime，见 ai.go saveGeneratedImage），下发必须以实际字节为准。
// 返回嗅探类型与已消费的头部字节：调用方必须把头部字节与剩余流拼接续传，不能整体
// 读入内存。Get/Head 与取件下发共用本入口，避免同一处不一致只修一条路径的回归。
func sniffHead(reader io.Reader) (string, []byte) {
	head := make([]byte, sniffHeadLen)
	n, _ := io.ReadFull(reader, head)
	return http.DetectContentType(head[:n]), head[:n]
}

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
