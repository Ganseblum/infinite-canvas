package handler

import (
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
	"github.com/infinite-canvas/server/internal/storage"
)

// storageKeyRe 与前端 storageKeyPattern 对齐并收紧字符集，不匹配一律 400。
var storageKeyRe = regexp.MustCompile(`^(image|video|audio|file|video-reference|audio-reference):[A-Za-z0-9_-]{1,64}$`)

// MediaHandler 媒体文件：HEAD/GET 读，PUT 经后端代理流式写入，DELETE 硬删除。
type MediaHandler struct {
	db      *gorm.DB
	storage storage.Storage
}

func NewMediaHandler(db *gorm.DB, stor storage.Storage) *MediaHandler {
	return &MediaHandler{db: db, storage: stor}
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

	var plan model.Plan
	if err := h.db.First(&plan, "id = ?", "free").Error; err != nil {
		slog.Error("读取档位失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	maxBytes := plan.MaxFileBytes
	if c.Request.ContentLength > maxBytes {
		errs.Abort(c, errs.ErrFileTooLarge)
		return
	}

	objectPath := mediaObjectPath(uid, key)
	body := http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
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
	err = h.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "storage_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"object_path", "mime_type", "bytes", "checksum", "moderation_status"}),
	}).Create(&file).Error
	if err != nil {
		slog.Error("写入媒体索引失败", "err", err)
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
	// media_files 走硬删除：对象已经删除，留一行记录没有意义。
	if err := h.db.Where("id = ?", file.ID).Delete(&model.MediaFile{}).Error; err != nil {
		slog.Error("删除媒体索引失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	noContent(c)
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
