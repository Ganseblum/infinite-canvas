package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/httpx"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/service"
)

// 平台渠道管理。Key 字段只写不读：响应里只返回 hasKey 与后四位，永远不返回明文。
var validAPIFormats = map[string]bool{"openai": true, "gemini": true, "ark": true}

func (h *AdminHandler) ListChannels(c *gin.Context) {
	var channels []model.PlatformChannel
	if err := h.db.Order("priority DESC, created_at ASC").Find(&channels).Error; err != nil {
		slog.Error("读取平台渠道失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]gin.H, 0, len(channels))
	for i := range channels {
		items = append(items, channelPayload(&channels[i]))
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func channelPayload(channel *model.PlatformChannel) gin.H {
	return gin.H{
		"id":        channel.ID.String(),
		"name":      channel.Name,
		"baseUrl":   channel.BaseURL,
		"apiFormat": channel.APIFormat,
		"priority":  channel.Priority,
		"enabled":   channel.Enabled,
		"hasKey":    len(channel.Payload) > 0,
		"createdAt": httpx.FormatTime(channel.CreatedAt),
		"updatedAt": httpx.FormatTime(channel.UpdatedAt),
	}
}

type channelReq struct {
	Name      string `json:"name"`
	BaseURL   string `json:"baseUrl"`
	APIFormat string `json:"apiFormat"`
	APIKey    string `json:"apiKey"`
	Priority  *int   `json:"priority"`
	Enabled   *bool  `json:"enabled"`
}

// CreateChannel 新增平台渠道：Key 经上游密码器加密落库（nonce+payload），审计不含 Key。
func (h *AdminHandler) CreateChannel(c *gin.Context) {
	if h.upstream == nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var req channelReq
	if err := c.ShouldBindJSON(&req); err != nil || !validChannelReq(&req) {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	nonce, payload, err := h.upstream.Cipher().Encrypt([]byte(strings.TrimSpace(req.APIKey)))
	if err != nil {
		slog.Error("加密渠道密钥失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	channel := model.PlatformChannel{
		ID:         uuid.New(),
		Name:       req.Name,
		BaseURL:    service.TrimBaseURL(req.BaseURL),
		APIFormat:  req.APIFormat,
		Nonce:      nonce,
		Payload:    payload,
		KeyVersion: 1,
		Enabled:    req.Enabled == nil || *req.Enabled,
	}
	if req.Priority != nil {
		channel.Priority = *req.Priority
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&channel).Error; err != nil {
			return err
		}
		// 审计只记渠道基本信息，不记 Key。
		return h.audit.Record(tx, actorID, "channel.create", "platform_channel", channel.ID.String(), c.GetString("request_id"), "", nil, channelAudit(channel))
	})
	if err != nil {
		slog.Error("新增渠道失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusCreated, channelPayload(&channel))
}

func (h *AdminHandler) UpdateChannel(c *gin.Context) {
	if h.upstream == nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	channelID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	var req channelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	var channel model.PlatformChannel
	if err := h.db.First(&channel, "id = ?", channelID).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	before := channelAudit(channel)
	if req.Name != "" {
		channel.Name = req.Name
	}
	if req.BaseURL != "" {
		channel.BaseURL = service.TrimBaseURL(req.BaseURL)
	}
	if req.APIFormat != "" {
		if !validAPIFormats[req.APIFormat] {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"apiFormat": "apiFormat 取值非法"}))
			return
		}
		channel.APIFormat = req.APIFormat
	}
	// Key 只写不读：不传就保持原值，传了就整把替换。
	if strings.TrimSpace(req.APIKey) != "" {
		nonce, payload, err := h.upstream.Cipher().Encrypt([]byte(strings.TrimSpace(req.APIKey)))
		if err != nil {
			errs.Abort(c, errs.ErrInternal)
			return
		}
		channel.Nonce = nonce
		channel.Payload = payload
		channel.KeyVersion++
	}
	if req.Priority != nil {
		channel.Priority = *req.Priority
	}
	if req.Enabled != nil {
		channel.Enabled = *req.Enabled
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&channel).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "channel.update", "platform_channel", channel.ID.String(), c.GetString("request_id"), "", before, channelAudit(channel))
	})
	if err != nil {
		slog.Error("更新渠道失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, channelPayload(&channel))
}

func (h *AdminHandler) DeleteChannel(c *gin.Context) {
	channelID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	var channel model.PlatformChannel
	if err := h.db.First(&channel, "id = ?", channelID).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	// 仍被模型目录引用的渠道不允许删除，避免生成时才失败。
	var models []model.ModelCatalog
	if err := h.db.Find(&models).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	for _, item := range models {
		var ids []uuid.UUID
		if len(item.ChannelIDs) == 0 {
			continue
		}
		if err := json.Unmarshal(item.ChannelIDs, &ids); err != nil {
			continue
		}
		for _, id := range ids {
			if id == channelID {
				errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"id": "该渠道仍被模型引用，请先解除绑定"}))
				return
			}
		}
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", channelID).Delete(&model.PlatformChannel{}).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "channel.delete", "platform_channel", channelID.String(), c.GetString("request_id"), "", channelAudit(channel), nil)
	})
	if err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	httpx.NoContent(c)
}

// RetryRefunds 重试所有退还失败的生成请求。
func (h *AdminHandler) RetryRefunds(c *gin.Context) {
	if h.requests == nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	retried, err := h.requests.RetryPendingRefunds(c.Request.Context())
	if err != nil {
		slog.Error("重试待退还失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"retried": retried})
}

func validChannelReq(req *channelReq) bool {
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.BaseURL) == "" || strings.TrimSpace(req.APIKey) == "" {
		return false
	}
	return validAPIFormats[req.APIFormat]
}

func channelAudit(channel model.PlatformChannel) gin.H {
	return gin.H{
		"name":      channel.Name,
		"baseUrl":   channel.BaseURL,
		"apiFormat": channel.APIFormat,
		"priority":  channel.Priority,
		"enabled":   channel.Enabled,
		"hasKey":    len(channel.Payload) > 0,
	}
}
