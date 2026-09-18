package handler

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/storage"
)

// 「申请下载」取件链路的冻结契约（见 records/plans/media-watermark.md「接口契约」节）：
// POST /api/media/:storageKey/download 严格按归属签发短期令牌；
// GET /api/media-download/:token 校验令牌后回流干净原件。
// 两个端点永远在线，不读 WATERMARK_ENABLED——flag 只控制生成链路是否烧录。
const (
	// downloadTokenTTL 是取件令牌的有效期（钳制上限 300s，契约冻结值）。
	downloadTokenTTL = 300 * time.Second
	// downloadTokenVersion 是令牌 payload 的版本字节，仅接受 1。
	downloadTokenVersion = 1
	// downloadKeyInfo 是 HMAC 密钥派生的域分隔消息：密钥 = HMAC-SHA256(JWTSecret, 该消息) 摘要。
	// 只在内存派生，不落盘、不输出任何密钥值。
	downloadKeyInfo = "ic-media-download-v1"
	// downloadTokenMinPayload 是最小 payload 长度：ver1+exp8+uid16+keyLen2+空key+nonce8。
	downloadTokenMinPayload = 1 + 8 + 16 + 2 + 8
)

// downloadClaims 是从取件令牌里解出的归属信息。
type downloadClaims struct {
	uid        uuid.UUID
	storageKey string
}

// deriveDownloadKey 从 JWTSecret 派生令牌签名密钥（契约冻结的派生式）。
func deriveDownloadKey(secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(downloadKeyInfo))
	return mac.Sum(nil)
}

// mintDownloadToken 签发取件令牌：b64url(payload) + "." + b64url(sig)（RawURLEncoding 无 padding）。
// payload = ver(1B)=1 ‖ exp(8B unix BE) ‖ uid(16B raw uuid) ‖ keyLen(2B BE) ‖ storageKey ‖ nonce(8B)；
// sig = HMAC-SHA256(派生密钥, payload)。nonce 保证重复签发得到不同令牌。
func mintDownloadToken(secret []byte, uid uuid.UUID, storageKey string, ttl time.Duration) (string, time.Time, error) {
	exp := time.Now().Add(ttl)
	payload := make([]byte, 0, downloadTokenMinPayload+len(storageKey))
	payload = append(payload, downloadTokenVersion)
	payload = binary.BigEndian.AppendUint64(payload, uint64(exp.Unix()))
	payload = append(payload, uid[:]...)
	payload = binary.BigEndian.AppendUint16(payload, uint16(len(storageKey)))
	payload = append(payload, storageKey...)
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return "", time.Time{}, err
	}
	payload = append(payload, nonce...)

	mac := hmac.New(sha256.New, deriveDownloadKey(secret))
	mac.Write(payload)
	sig := mac.Sum(nil)

	token := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sig)
	return token, exp, nil
}

// verifyDownloadToken 校验取件令牌，任何一步失败返回 false（调用方一律 404 防探测）。
// 校验链顺序（契约冻结）：hmac.Equal 常量时间比对 → ver=1 → exp 未过期 → 结构完整；
// uid 归属与媒体行存在性由调用方继续校验。
func verifyDownloadToken(secret []byte, token string) (*downloadClaims, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	mac := hmac.New(sha256.New, deriveDownloadKey(secret))
	mac.Write(payload)
	// hmac.Equal 常量时间比对，防时序侧信道探测签名。
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, false
	}
	if len(payload) < downloadTokenMinPayload {
		return nil, false
	}
	if payload[0] != downloadTokenVersion {
		return nil, false
	}
	if time.Now().Unix() >= int64(binary.BigEndian.Uint64(payload[1:9])) {
		return nil, false
	}
	uid, err := uuid.FromBytes(payload[9:25])
	if err != nil {
		return nil, false
	}
	keyLen := int(binary.BigEndian.Uint16(payload[25:27]))
	if 27+keyLen+8 != len(payload) {
		return nil, false
	}
	return &downloadClaims{uid: uid, storageKey: string(payload[27 : 27+keyLen])}, true
}

// RequestDownload 处理 POST /api/media/:storageKey/download（契约两态响应）。
// 归属判定用严格查询 user_id = 当前用户 AND storage_key，禁走 findOwned 的
// 社区放行分支——那是本 feature 最严重的泄漏面：社区浏览者绝不能借下载端点
// 取得作者名下对象的干净原件。归属不符与不存在一律 404，防探测。
func (h *MediaHandler) RequestDownload(c *gin.Context) {
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
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if err != nil {
		slog.Error("查询媒体索引失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	// 档位按媒体行归属者派生（此处归属者即当前用户），口径与下发选字节一致。
	_, plan, err := h.quota.DerivePlan(c.Request.Context(), file.UserID, time.Now())
	if err != nil {
		slog.Error("读取档位失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	mediaURL := "/api/media/" + key
	if plan.ID != "paid" {
		// 非付费档：下发层本就出主对象（水印版），无需签发取件链接。
		c.JSON(http.StatusOK, gin.H{"url": mediaURL, "expiresAt": nil})
		return
	}
	has, err := storage.HasOriginal(c.Request.Context(), h.storage, file.UserID.String(), file.StorageKey)
	if err != nil {
		slog.Error("查询干净原件失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if !has {
		// 付费但无干净原件：无从签发，回表现有下发 URL（水印版）。
		c.JSON(http.StatusOK, gin.H{"url": mediaURL, "expiresAt": nil})
		return
	}
	token, exp, err := mintDownloadToken(h.secret, file.UserID, key, downloadTokenTTL)
	if err != nil {
		slog.Error("签发下载令牌失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": "/api/media-download/" + token, "expiresAt": exp.Format(time.RFC3339)})
}

// ServeDownload 处理 GET /api/media-download/:token：校验链任一步失败一律 404，
// 不区分过期、篡改、跨用户或归属行已删（防探测）。成功时回流干净原件。
func (h *MediaHandler) ServeDownload(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		return
	}
	claims, ok := verifyDownloadToken(h.secret, c.Param("token"))
	if !ok {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	// 令牌内 uid 必须等于当前用户：签名链接不可转借。
	if claims.uid != uid {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	// storageKey 由本服务签发、签发前已过格式校验，取件侧再过一次正则，
	// 保证进入对象路径拼装与附件文件名的字符串绝对干净；不符按伪造处理。
	if !storageKeyRe.MatchString(claims.storageKey) {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	// media_files 归属行仍存在（同严格口径）：行已删则对象连带清理，链接立即失效。
	var file model.MediaFile
	err := h.db.Where("user_id = ? AND storage_key = ?", claims.uid, claims.storageKey).First(&file).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if err != nil {
		slog.Error("查询媒体索引失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	origPath := storage.OrigPath(file.UserID.String(), file.StorageKey)
	if h.storage.Kind() == "local" {
		h.serveOrigLocal(c, origPath, file.StorageKey)
		return
	}
	presigned, err := h.storage.PresignWithTTL(c.Request.Context(), origPath, origPresignTTL)
	if err != nil {
		// 预签名失败不允许回退到无签名长 URL（异常矩阵：返回 500，可重试）。
		slog.Error("生成干净原件预签名失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, no-store")
	c.Header("Location", presigned.URL)
	c.Status(http.StatusFound)
}

// serveOrigLocal 流式回流干净原件。orig 无扩展名且 mime 与媒体行可能不一致
// （webp→png 场景以实际字节为准），Content-Type 与附件文件名按嗅探结果给出。
func (h *MediaHandler) serveOrigLocal(c *gin.Context, origPath, storageKey string) {
	size, err := h.storage.Stat(c.Request.Context(), origPath)
	if errors.Is(err, storage.ErrObjectNotFound) {
		// 行在而原件已被并发清理（T6/覆盖上传）：链接立即失效，一律 404。
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if err != nil {
		slog.Error("读取干净原件失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	reader, err := h.storage.Get(c.Request.Context(), origPath)
	if errors.Is(err, storage.ErrObjectNotFound) {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if err != nil {
		slog.Error("打开干净原件失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	defer reader.Close()
	// 嗅探文件头决定类型与扩展名，头部字节与剩余流拼接回给客户端。
	// 嗅探逻辑与媒体读取路径共用 sniffHead，避免两份实现漂移。
	sniffed, head := sniffHead(reader)
	ext := ".bin"
	if exts, err := mime.ExtensionsByType(sniffed); err == nil && len(exts) > 0 {
		ext = exts[0]
	}
	// storageKey 已过 storageKeyRe，对象 id 段仅含 [A-Za-z0-9_-]，文件名无需再转义。
	c.Header("Content-Type", sniffed)
	c.Header("Content-Disposition", `attachment; filename="ic-`+storageObjectID(storageKey)+ext+`"`)
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Length", strconv.FormatInt(size, 10))
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, io.MultiReader(bytes.NewReader(head), reader)); err != nil {
		slog.Warn("干净原件响应写出中断", "err", err)
	}
}

// storageObjectID 取 storageKey 冒号后的对象 id 段，用于附件文件名。
func storageObjectID(storageKey string) string {
	_, id, _ := strings.Cut(storageKey, ":")
	return id
}
