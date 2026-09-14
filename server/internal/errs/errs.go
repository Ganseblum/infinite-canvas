package errs

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

// AppError 是统一业务错误，code 对应第一期错误码总表。
type AppError struct {
	HTTPStatus int
	Code       string
	Message    string
	Fields     map[string]string
	// Extra 是 error 对象上的附加数据，例如 REVISION_CONFLICT 的当前 revision。
	Extra      map[string]any
	RetryAfter int
}

func (e *AppError) Error() string { return e.Code + ": " + e.Message }

func New(httpStatus int, code, message string) *AppError {
	return &AppError{HTTPStatus: httpStatus, Code: code, Message: message}
}

// WithFields 返回带字段说明的副本，不修改传入的错误实例（含包级单例）。
func WithFields(e *AppError, fields map[string]string) *AppError {
	clone := *e
	clone.Fields = fields
	return &clone
}

// WithRetryAfter 返回带 Retry-After 的副本，不修改传入的错误实例（含包级单例）。
func WithRetryAfter(e *AppError, seconds int) *AppError {
	clone := *e
	clone.RetryAfter = seconds
	return &clone
}

// WithExtra 返回带附加数据的副本，不修改传入的错误实例（含包级单例）。
func WithExtra(e *AppError, extra map[string]any) *AppError {
	clone := *e
	clone.Extra = extra
	return &clone
}

// 第一期启用（按错误码总表）
var (
	ErrValidation      = New(400, "VALIDATION_FAILED", "参数校验失败")
	ErrUnauthorized    = New(401, "UNAUTHORIZED", "缺少或无效的凭证")
	ErrInvalidCreds    = New(401, "INVALID_CREDENTIALS", "邮箱或密码错误")
	ErrTokenExpired    = New(401, "TOKEN_EXPIRED", "登录已过期，请重新登录")
	ErrAccountDisabled = New(403, "ACCOUNT_DISABLED", "账号已被封禁")
	ErrEmailNotVerif   = New(403, "EMAIL_NOT_VERIFIED", "请先验证邮箱")
	ErrForbidden       = New(403, "FORBIDDEN", "无权访问该资源")
	ErrFreeGrantUnav   = New(403, "FREE_GRANT_UNAVAILABLE", "免费赠送暂不可用")
	ErrNotFound        = New(404, "NOT_FOUND", "资源不存在")
	ErrEmailTaken      = New(409, "EMAIL_TAKEN", "邮箱已注册")
	ErrUsernameTaken   = New(409, "USERNAME_TAKEN", "用户名已占用")
	// 第二期启用
	ErrRevisionConflict = New(409, "REVISION_CONFLICT", "画布已在其他设备被修改")
	ErrChecksumMismatch = New(409, "CHECKSUM_MISMATCH", "文件校验和不一致")
	ErrFileTooLarge     = New(413, "FILE_TOO_LARGE", "上传文件超过所属套餐的单文件大小限制")
	ErrTokenInvalid     = New(410, "TOKEN_INVALID", "令牌无效或已过期")
	ErrRateLimited      = New(429, "RATE_LIMITED", "请求过于频繁，请稍后重试")
	ErrRegDisabled      = New(503, "REGISTRATION_DISABLED", "注册暂时关闭")
	ErrInternal         = New(500, "INTERNAL_ERROR", "服务暂时不可用，请稍后重试")
)

// Abort 中止请求并写统一错误响应。
func Abort(c *gin.Context, e *AppError) {
	if e.RetryAfter > 0 {
		c.Header("Retry-After", fmt.Sprint(e.RetryAfter))
	}
	errObj := gin.H{"code": e.Code, "message": e.Message}
	if len(e.Fields) > 0 {
		errObj["fields"] = e.Fields
	}
	for k, v := range e.Extra {
		errObj[k] = v
	}
	c.AbortWithStatusJSON(e.HTTPStatus, gin.H{"error": errObj})
}
