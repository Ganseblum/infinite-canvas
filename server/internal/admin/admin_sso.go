package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/model"
)

// ===== SSO OAuth 客户端管理（PLAN T10）=====

// oauthClientPayload 把客户端行渲染成管理端契约形状；密钥摘要永不回传，
// 明文 secret 只在创建与重置的响应里出现一次。
func oauthClientPayload(client model.OAuthClient) gin.H {
	uris := []string{}
	_ = json.Unmarshal(client.RedirectURIs, &uris)
	return gin.H{
		"id":           client.ID.String(),
		"productId":    client.Product,
		"name":         client.Name,
		"clientId":     client.ClientID,
		"redirectUris": uris,
		"enabled":      client.Enabled,
		"createdAt":    formatTime(client.CreatedAt),
		"updatedAt":    formatTime(client.UpdatedAt),
	}
}

// newOAuthClientSecret 生成 48 位十六进制明文密钥（仅响应瞬间存在，库里只存摘要）。
func newOAuthClientSecret() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// validateRedirectURIs 校验回调地址清单：每行合法 URL、无 fragment、必须 https
// （仅 localhost / 127.0.0.1 / [::1] 允许 http，与 admin 前端校验同口径）；去空白去重。
func validateRedirectURIs(uris []string) ([]string, bool) {
	cleaned := make([]string, 0, len(uris))
	seen := make(map[string]struct{}, len(uris))
	for _, raw := range uris {
		uri := strings.TrimSpace(raw)
		if uri == "" {
			continue
		}
		parsed, err := url.Parse(uri)
		if err != nil || parsed.Fragment != "" || parsed.Host == "" {
			return nil, false
		}
		https := parsed.Scheme == "https"
		loopback := parsed.Scheme == "http" && (parsed.Hostname() == "localhost" ||
			parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1")
		if !https && !loopback {
			return nil, false
		}
		if _, dup := seen[uri]; dup {
			continue
		}
		seen[uri] = struct{}{}
		cleaned = append(cleaned, uri)
	}
	if len(cleaned) == 0 {
		return nil, false
	}
	return cleaned, true
}

// ListOAuthClients 列出全部接入客户端（前端全量渲染 + 前端分页）。
func (h *AdminHandler) ListOAuthClients(c *gin.Context) {
	var clients []model.OAuthClient
	if err := h.db.WithContext(c.Request.Context()).
		Order("created_at DESC, id DESC").Find(&clients).Error; err != nil {
		slog.Error("查询 OAuth 客户端列表失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]gin.H, 0, len(clients))
	for _, client := range clients {
		items = append(items, oauthClientPayload(client))
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items), "page": 1, "size": len(items)})
}

type createOAuthClientReq struct {
	Name         string   `json:"name"`
	RedirectURIs []string `json:"redirectUris"`
	ProductID    string   `json:"productId"`
}

// CreateOAuthClient 新增接入客户端：明文 secret 仅本次 201 响应返回一次，库里只存摘要。
func (h *AdminHandler) CreateOAuthClient(c *gin.Context) {
	var req createOAuthClientReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	fields := map[string]string{}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 120 {
		fields["name"] = "名称必填且不超过 120 字符"
	}
	uris, ok := validateRedirectURIs(req.RedirectURIs)
	if !ok {
		fields["redirectUris"] = "至少一个合法回调地址，且必须为 https（本机回环允许 http）"
	}
	product := strings.TrimSpace(req.ProductID)
	if product == "" {
		product = model.ProductCanvas
	}
	if product != model.ProductCanvas {
		// 未上线阶段只注册了画布产品；新产品接入时随 /admin/meta 注册表一并放开。
		fields["productId"] = "未知产品"
	}
	if len(fields) > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, fields))
		return
	}
	clientID, err := newOAuthClientID()
	if err != nil {
		slog.Error("生成 OAuth client_id 失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	secret, err := newOAuthClientSecret()
	if err != nil {
		slog.Error("生成 OAuth client_secret 失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	client := model.OAuthClient{
		ID:               uuid.New(),
		Product:          product,
		Name:             name,
		ClientID:         clientID,
		ClientSecretHash: hashOAuthClientSecret(secret),
		Enabled:          true,
	}
	if raw, err := json.Marshal(uris); err == nil {
		client.RedirectURIs = raw
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&client).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "sso.client.create", "oauth_client", client.ID.String(),
			c.GetString("request_id"), "", nil, oauthClientPayload(client))
	})
	if err != nil {
		if isUniqueViolation(err, "client_id") {
			errs.Abort(c, errs.ErrInternal)
			return
		}
		slog.Error("创建 OAuth 客户端失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"client": oauthClientPayload(client), "clientSecret": secret})
}

type updateOAuthClientReq struct {
	Name         *string  `json:"name"`
	RedirectURIs []string `json:"redirectUris"`
	Enabled      *bool    `json:"enabled"`
}

// UpdateOAuthClient 修改客户端：仅更新请求中出现的字段，变更前后写入审计。
func (h *AdminHandler) UpdateOAuthClient(c *gin.Context) {
	clientID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	var req updateOAuthClientReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	fields := map[string]string{}
	updates := map[string]any{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len([]rune(name)) > 120 {
			fields["name"] = "名称必填且不超过 120 字符"
		} else {
			updates["name"] = name
		}
	}
	if req.RedirectURIs != nil {
		uris, ok := validateRedirectURIs(req.RedirectURIs)
		if !ok {
			fields["redirectUris"] = "至少一个合法回调地址，且必须为 https（本机回环允许 http）"
		} else if raw, err := json.Marshal(uris); err == nil {
			updates["redirect_uris"] = raw
		}
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if len(fields) > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, fields))
		return
	}
	if len(updates) == 0 {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	var client model.OAuthClient
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&client, "id = ?", clientID).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.OAuthClient{}).Where("id = ?", clientID).Updates(updates).Error; err != nil {
			return err
		}
		// 审计 after 需要更新后的行：事务内回读（评审门④ P2-5 口径统一）。
		var updated model.OAuthClient
		if err := tx.First(&updated, "id = ?", clientID).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "sso.client.update", "oauth_client", clientID.String(),
			c.GetString("request_id"), "", oauthClientPayload(client), oauthClientPayload(updated))
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if err != nil {
		slog.Error("更新 OAuth 客户端失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	if err := h.db.First(&client, "id = ?", clientID).Error; err != nil {
		slog.Error("回读 OAuth 客户端失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"client": oauthClientPayload(client)})
}

// ResetOAuthClientSecret 重置密钥：旧 secret 立即失效，新明文仅本次响应返回一次。
func (h *AdminHandler) ResetOAuthClientSecret(c *gin.Context) {
	clientID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	secret, err := newOAuthClientSecret()
	if err != nil {
		slog.Error("生成 OAuth client_secret 失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	var client model.OAuthClient
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&client, "id = ?", clientID).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.OAuthClient{}).Where("id = ?", clientID).
			Update("client_secret_hash", hashOAuthClientSecret(secret)).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "sso.client.reset_secret", "oauth_client", clientID.String(),
			c.GetString("request_id"), "", nil, nil)
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if err != nil {
		slog.Error("重置 OAuth 客户端密钥失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"clientSecret": secret})
}

// DeleteOAuthClient 删除接入客户端（授权即刻失效）。
func (h *AdminHandler) DeleteOAuthClient(c *gin.Context) {
	clientID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	var client model.OAuthClient
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&client, "id = ?", clientID).Error; err != nil {
			return err
		}
		if err := tx.Delete(&client).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "sso.client.delete", "oauth_client", clientID.String(),
			c.GetString("request_id"), "", oauthClientPayload(client), nil)
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if err != nil {
		slog.Error("删除 OAuth 客户端失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.Status(http.StatusNoContent)
}
