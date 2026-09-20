// Package errs 定义全服务统一的业务错误码：每个 AppError 绑定 HTTP 状态码与
// 用户可读文案，Abort 负责按 { error: { code, message, ... } } 形状写出响应。
// handler 不自行拼错误响应，一律复用包级单例或其 With* 副本。
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
	ErrSlugTaken        = New(409, "SLUG_TAKEN", "该 slug 已被占用")
	ErrTopicInUse       = New(409, "TOPIC_IN_USE", "栏目下仍有文章，无法删除")
	ErrRegDisabled      = New(503, "REGISTRATION_DISABLED", "注册暂时关闭")
	ErrInternal         = New(500, "INTERNAL_ERROR", "服务暂时不可用，请稍后重试")
	// 第三期启用
	ErrInsufficientCredits    = New(402, "INSUFFICIENT_CREDITS", "点数余额不足")
	ErrReadOnly               = New(403, "READ_ONLY", "当前用量超过档位上限，处于只读状态")
	ErrAccountPendingDeletion = New(403, "ACCOUNT_PENDING_DELETION", "账号处于注销冷静期，暂时无法生成或充值")
	ErrOrderAlreadyPaid       = New(409, "ORDER_ALREADY_PAID", "订单已支付，无法取消")
	ErrPromotionConflict      = New(409, "PROMOTION_CONFLICT", "折扣活动与既有规则冲突")
	ErrDeletionNotPending     = New(409, "DELETION_NOT_PENDING", "账号未处于注销冷静期")
	ErrInvalidSignature       = New(400, "INVALID_SIGNATURE", "回调验签失败")
	ErrStorageQuota           = New(507, "STORAGE_QUOTA_EXCEEDED", "存储配额不足")
	// 第四期启用
	ErrModelNotSupported  = New(400, "MODEL_NOT_SUPPORTED", "该模型暂不可用")
	ErrParamNotSupported  = New(400, "PARAM_NOT_SUPPORTED", "参数不在该模型允许范围内")
	ErrQuoteStale         = New(409, "QUOTE_STALE", "报价已失效，请重新报价")
	ErrConcurrencyLimited = New(429, "CONCURRENCY_LIMITED", "同时进行的生成任务过多，请稍后再试")
	ErrUpstreamError      = New(502, "UPSTREAM_ERROR", "上游服务返回异常，请稍后重试")
	ErrUpstreamTimeout    = New(504, "UPSTREAM_TIMEOUT", "上游服务响应超时，请稍后重试")
	// 第五期启用
	ErrContentRejected       = New(422, "CONTENT_REJECTED", "内容未通过审核")
	ErrModerationUnavailable = New(503, "MODERATION_UNAVAILABLE", "内容审核服务暂不可用，请稍后重试")
	ErrModerationReviewed    = New(409, "MODERATION_ALREADY_REVIEWED", "该审核记录已被复核")
	// 第六期启用（RBAC 与管理员建号）
	ErrPasswordChangeRequired = New(403, "PASSWORD_CHANGE_REQUIRED", "请先修改初始密码后再继续操作")
)

// AddConflict 返回带冲突说明的 VALIDATION_FAILED 副本，用于需要附带业务原因的 409 场景。
func AddConflict(message string) *AppError {
	return New(409, "VALIDATION_FAILED", message)
}

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
