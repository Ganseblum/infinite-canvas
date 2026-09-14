package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/infinite-canvas/server/internal/errs"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
	// maxCanvasDataBytes 是画布 data 的字节上限（第一期约定「建议 2 MB」）。
	// 反代已限 2m，这里再兜一层，直连 8080 的请求同样会被拦住。
	maxCanvasDataBytes = 2 * 1024 * 1024
)

type pageParams struct {
	Page int
	Size int
}

// parsePageParams 解析 page/size：page 从 1 开始缺省 1；size 缺省 20、上限 100。
// 参数越界时写 400 VALIDATION_FAILED 并返回 false。
func parsePageParams(c *gin.Context) (pageParams, bool) {
	params := pageParams{Page: 1, Size: defaultPageSize}
	if raw := c.Query("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"page": "page 必须是不小于 1 的整数"}))
			return params, false
		}
		params.Page = n
	}
	if raw := c.Query("size"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxPageSize {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"size": "size 必须是 1-100 的整数"}))
			return params, false
		}
		params.Size = n
	}
	return params, true
}

// parseSort 把排序参数映射到列名白名单，不把用户输入拼进 SQL。
// 前缀 "-" 表示倒序，缺省由 fallback 给出。
func parseSort(raw string, allowed map[string]string, fallback string) (string, bool) {
	if raw == "" {
		raw = fallback
	}
	desc := false
	name := raw
	if strings.HasPrefix(raw, "-") {
		desc = true
		name = raw[1:]
	}
	column, ok := allowed[name]
	if !ok {
		return "", false
	}
	if desc {
		column += " DESC"
	} else {
		column += " ASC"
	}
	return column, true
}

// currentUserID 从鉴权中间件写入的上下文取当前用户 id。
func currentUserID(c *gin.Context) (uuid.UUID, bool) {
	uid, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return uuid.Nil, false
	}
	return uid, true
}

// formatTime 统一时间序列化：RFC3339、UTC。
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// searchPattern 把关键字转成前后通配的大小写不敏感 LIKE 模式。
func searchPattern(q string) string {
	return "%" + strings.ToLower(strings.TrimSpace(q)) + "%"
}

// assetContentExpr 返回提取素材正文的方言表达式。MySQL 用 JSON_UNQUOTE +
// JSON_EXTRACT，测试库 SQLite 用 json_extract；只查询明确的 content 键，
// 不把整个 JSON 转文本，避免 storageKey、mimeType 等元数据被搜出来。
func assetContentExpr(dialector string) string {
	if dialector == "mysql" {
		return "JSON_UNQUOTE(JSON_EXTRACT(data, '$.content'))"
	}
	return "json_extract(data, '$.content')"
}

// noContent 用于 DELETE 等幂等 204 响应。
func noContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}
