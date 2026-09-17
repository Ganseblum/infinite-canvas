package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 预检响应的固定取值。凭证模式下 Access-Control-Allow-Headers 用 * 不覆盖
// Authorization，必须显式列出；Retry-After 不在 CORS 安全列表响应头里，
// 必须靠 Expose-Headers 暴露，前端才能读到 429 的等待秒数。
const (
	corsAllowMethods  = "GET,POST,PUT,PATCH,DELETE,HEAD,OPTIONS"
	corsAllowHeaders  = "Authorization,Content-Type"
	corsExposeHeaders = "Retry-After"
	corsMaxAge        = "600"
)

// CORS 只回显白名单内的 Origin；白名单为空表示完全不启用，行为与没有该中间件一致。
//
// 非白名单来源（同源主站自带的 Origin、curl、监控、支付回调）不加任何
// Access-Control-* 头并照常放行：是否跨源由浏览器判断，服务端只决定是否授权，
// 返回 403 会把同源业务请求和回调一并拦掉。
func CORS(allowedOrigins []string) gin.HandlerFunc {
	if len(allowedOrigins) == 0 {
		return func(c *gin.Context) { c.Next() }
	}
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		// Origin: null（沙箱 iframe、部分重定向）永远不视为白名单来源。
		if origin == "" || origin == "null" {
			continue
		}
		allowed[origin] = struct{}{}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}
		// 响应随 Origin 变化，必须写 Vary，避免共享缓存把带 CORS 头的响应复用到其他来源。
		c.Writer.Header().Add("Vary", "Origin")
		if _, ok := allowed[origin]; !ok {
			c.Next()
			return
		}
		header := c.Writer.Header()
		// 精确回显 Origin，凭证模式下不能用 *。
		header.Set("Access-Control-Allow-Origin", origin)
		header.Set("Access-Control-Allow-Credentials", "true")
		header.Set("Access-Control-Expose-Headers", corsExposeHeaders)
		if c.Request.Method == http.MethodOptions && c.GetHeader("Access-Control-Request-Method") != "" {
			header.Set("Access-Control-Allow-Methods", corsAllowMethods)
			header.Set("Access-Control-Allow-Headers", corsAllowHeaders)
			header.Set("Access-Control-Max-Age", corsMaxAge)
			// 预检在鉴权与限流之前短路，浏览器拿到结果后才会发真实请求。
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
