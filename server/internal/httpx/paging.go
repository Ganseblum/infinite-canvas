// Package httpx 提供 Gin handler 的通用请求辅助：分页与排序参数解析、
// 当前用户读取、时间与关键字匹配模式的统一序列化口径。
package httpx

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/infinite-canvas/server/internal/errs"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

type PageParams struct {
	Page int
	Size int
}

// ParsePageParams 解析 page/size：page 从 1 开始缺省 1；size 缺省 20、上限 100。
// 参数越界时写 400 VALIDATION_FAILED 并返回 false。
func ParsePageParams(c *gin.Context) (PageParams, bool) {
	params := PageParams{Page: 1, Size: DefaultPageSize}
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
		if err != nil || n < 1 || n > MaxPageSize {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"size": "size 必须是 1-100 的整数"}))
			return params, false
		}
		params.Size = n
	}
	return params, true
}

// ParseCursorSize 解析游标分页的 size：缺省 20、上限 100。
func ParseCursorSize(raw string) (int, error) {
	if raw == "" {
		return DefaultPageSize, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > MaxPageSize {
		return 0, errors.New("size 越界")
	}
	return n, nil
}

// ParseSort 把排序参数映射到列名白名单，不把用户输入拼进 SQL。
// 前缀 "-" 表示倒序，缺省由 fallback 给出。
func ParseSort(raw string, allowed map[string]string, fallback string) (string, bool) {
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

// CurrentUserID 从鉴权中间件写入的上下文取当前用户 id。
func CurrentUserID(c *gin.Context) (uuid.UUID, bool) {
	uid, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		errs.Abort(c, errs.ErrUnauthorized)
		return uuid.Nil, false
	}
	return uid, true
}

// FormatTime 统一时间序列化：RFC3339、UTC。
func FormatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// FormatTimePtr 序列化可空时间：nil 返回 JSON null，否则 RFC3339、UTC。
func FormatTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// SearchPattern 把关键字转成前后通配的大小写不敏感 LIKE 模式。
func SearchPattern(q string) string {
	return "%" + strings.ToLower(strings.TrimSpace(q)) + "%"
}

// NoContent 用于 DELETE 等幂等 204 响应。
func NoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}
