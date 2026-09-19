package ai

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/service"
)

// ModelHandler 提供平台模型目录查询。目录是前端唯一的模型来源。
type ModelHandler struct {
	catalog *service.CatalogService
}

func NewModelHandler(db *gorm.DB, promotionEnabled func() bool) *ModelHandler {
	return &ModelHandler{catalog: service.NewCatalogService(db, promotionEnabled)}
}

func (h *ModelHandler) List(c *gin.Context) {
	models, err := h.catalog.ListModels(c.Request.Context(), c.Query("capability"))
	if err != nil {
		if errors.Is(err, service.ErrInvalidCapability) {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"capability": "capability 不在枚举内"}))
			return
		}
		slog.Error("读取模型目录失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": models})
}
