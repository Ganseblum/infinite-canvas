package admin

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/httpx"
	"github.com/infinite-canvas/server/internal/model"
)

// ===== 社区管理 =====

// ListCommunityWorks 管理端作品列表，可按状态与关键词筛选。
func (h *AdminHandler) ListCommunityWorks(c *gin.Context) {
	params, ok := httpx.ParsePageParams(c)
	if !ok {
		return
	}
	query := h.db.Model(&model.CommunityWork{})
	if status := c.Query("status"); status != "" {
		query = query.Where("status = ?", status)
	}
	if q := c.Query("q"); q != "" {
		pattern := httpx.SearchPattern(q)
		query = query.Where("LOWER(title) LIKE ? OR LOWER(description) LIKE ?", pattern, pattern)
	}
	if userId := c.Query("userId"); userId != "" {
		parsed, err := uuid.Parse(userId)
		if err != nil {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"userId": "userId 不合法"}))
			return
		}
		query = query.Where("user_id = ?", parsed)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var works []model.CommunityWork
	if err := query.Order("created_at DESC").Offset((params.Page - 1) * params.Size).Limit(params.Size).Find(&works).Error; err != nil {
		slog.Error("查询社区作品失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]gin.H, 0, len(works))
	for i := range works {
		items = append(items, communityWorkPayload(&works[i]))
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": params.Page, "size": params.Size})
}

type communityWorkPatchReq struct {
	Status *string `json:"status"`
}

// PatchCommunityWork 下架或恢复作品。下架后前台列表与详情都不可见。
func (h *AdminHandler) PatchCommunityWork(c *gin.Context) {
	workID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	var req communityWorkPatchReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Status == nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if *req.Status != "published" && *req.Status != "hidden" && *req.Status != "removed" {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"status": "status 取值非法"}))
		return
	}
	var work model.CommunityWork
	if err := h.db.First(&work, "id = ?", workID).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.CommunityWork{}).Where("id = ?", workID).Update("status", *req.Status).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "community.work_status", "community_work", workID.String(), c.GetString("request_id"), "",
			gin.H{"status": work.Status}, gin.H{"status": *req.Status})
	})
	if err != nil {
		slog.Error("更新作品状态失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": workID.String(), "status": *req.Status})
}

// ListCommunityReports 举报列表，默认只看待处理。
func (h *AdminHandler) ListCommunityReports(c *gin.Context) {
	params, ok := httpx.ParsePageParams(c)
	if !ok {
		return
	}
	query := h.db.Model(&model.CommunityReport{})
	if status := c.Query("status"); status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var reports []model.CommunityReport
	if err := query.Order("created_at DESC").Offset((params.Page - 1) * params.Size).Limit(params.Size).Find(&reports).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	workIDs := make([]uuid.UUID, 0, len(reports))
	for _, report := range reports {
		workIDs = append(workIDs, report.WorkID)
	}
	works := map[uuid.UUID]model.CommunityWork{}
	if len(workIDs) > 0 {
		var rows []model.CommunityWork
		h.db.Where("id IN ?", workIDs).Find(&rows)
		for _, work := range rows {
			works[work.ID] = work
		}
	}
	items := make([]gin.H, 0, len(reports))
	for i := range reports {
		work := works[reports[i].WorkID]
		items = append(items, gin.H{
			"id":        reports[i].ID.String(),
			"workId":    reports[i].WorkID.String(),
			"workTitle": work.Title,
			"reason":    reports[i].Reason,
			"status":    reports[i].Status,
			"createdAt": httpx.FormatTime(reports[i].CreatedAt),
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": params.Page, "size": params.Size})
}

type communityReportPatchReq struct {
	Status     string `json:"status"`
	RemoveWork bool   `json:"removeWork"`
}

// PatchCommunityReport 处理举报：忽略或采纳；采纳时可同时下架作品。
func (h *AdminHandler) PatchCommunityReport(c *gin.Context) {
	reportID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	var req communityReportPatchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if req.Status != "handled" && req.Status != "dismissed" {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"status": "status 只能是 handled 或 dismissed"}))
		return
	}
	var report model.CommunityReport
	if err := h.db.First(&report, "id = ?", reportID).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.CommunityReport{}).Where("id = ?", reportID).Update("status", req.Status).Error; err != nil {
			return err
		}
		if req.RemoveWork && req.Status == "handled" {
			if err := tx.Model(&model.CommunityWork{}).Where("id = ?", report.WorkID).Update("status", "removed").Error; err != nil {
				return err
			}
		}
		return h.audit.Record(tx, actorID, "community.report", "community_report", reportID.String(), c.GetString("request_id"), "",
			gin.H{"status": report.Status}, gin.H{"status": req.Status, "removeWork": req.RemoveWork})
	})
	if err != nil {
		slog.Error("处理举报失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": reportID.String(), "status": req.Status})
}

func communityWorkPayload(work *model.CommunityWork) gin.H {
	payload := gin.H{
		"id":          work.ID.String(),
		"userId":      work.UserID.String(),
		"assetId":     work.AssetID.String(),
		"title":       work.Title,
		"description": work.Description,
		"tags":        work.Tags,
		"coverKey":    work.CoverKey,
		"status":      work.Status,
		"likeCount":   work.LikeCount,
		"remixCount":  work.RemixCount,
		"reportCount": work.ReportCount,
		"createdAt":   httpx.FormatTime(work.CreatedAt),
	}
	if work.SourceWorkID != nil {
		payload["sourceWorkId"] = work.SourceWorkID.String()
	}
	return payload
}
