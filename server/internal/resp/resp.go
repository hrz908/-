package resp

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 业务码：0 表示成功，其余与 HTTP 状态码对齐，前端一眼能看出性质
const (
	CodeOK              = 0
	CodeBadRequest      = 400
	CodeUnauthorized    = 401
	CodeNotFound        = 404
	CodeConflict        = 409
	CodeTooManyRequests = 429
	CodeInternal        = 500
	CodeBadGateway      = 502
)

// Body 统一响应体：{ code, message, data }
type Body struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// Resp 即提示词约定的 resp(c, code, msg, data)。
// 中间件和 Handler 都复用它，保证返回结构始终一致；
// 是否中断请求由调用方决定（中间件里自行 c.Abort()）
func Resp(c *gin.Context, code int, msg string, data any) {
	c.JSON(Status(code), Body{Code: code, Message: msg, Data: data})
}

func OK(c *gin.Context, data any) { Resp(c, CodeOK, "ok", data) }

func Fail(c *gin.Context, code int, msg string) { Resp(c, code, msg, nil) }

// Status 业务码 → HTTP 状态码；code=0 一律 200
func Status(code int) int {
	switch code {
	case CodeOK:
		return http.StatusOK
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeBadRequest, CodeNotFound, CodeConflict:
		return code
	case CodeTooManyRequests:
		return http.StatusTooManyRequests
	case CodeBadGateway:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}
