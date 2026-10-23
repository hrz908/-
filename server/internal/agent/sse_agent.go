package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"stock-agent-server/internal/resp"
)

// sseWriteTimeoutExtra SSE 连接的写截止时间余量。
// http.Server 的 WriteTimeout（默认 15s）会掐断长流；这里用 ResponseController
// 把本连接的写截止放宽到「单次运行上限 + 余量」，覆盖整个流式过程。
const sseWriteTimeoutExtra = 15 * time.Second

// ServeSSE 处理 POST /api/v1/agent/stream（H5 / 不支持 WS 的场景兜底）。
// 请求体 {"query":"...","session_id":"..."}，响应 text/event-stream，
// 每帧格式 event: {type}\ndata: {json}\n\n，逐帧 Flush 保证实时性。
func ServeSSE(c *gin.Context) {
	userID := c.GetInt64("user_id")

	var req RunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "请求体格式错误")
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		resp.Fail(c, resp.CodeBadRequest, "query 不能为空")
		return
	}

	ctx := c.Request.Context()

	// 限流：此时流尚未开始，可直接返回标准 HTTP 429，
	// 便于前端与监控按状态码区分「被限流」与「流内错误」。
	if err := limiter.Allow(ctx, userID); err != nil {
		resp.Fail(c, resp.CodeTooManyRequests, err.Error())
		return
	}
	defer limiter.ReleaseConcurrent(ctx, userID)

	// SSE 响应头。X-Accel-Buffering: no 关闭 Nginx 缓冲，否则事件会被攒批下发失去实时性。
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		log.Printf("agent sse: response writer does not support flush")
		return
	}

	// 放宽写截止时间，避免被 server 级 WriteTimeout（15s）中断长流。
	// gin 的 ResponseWriter 实现了 Unwrap，ResponseController 可透传到底层连接。
	rc := http.NewResponseController(c.Writer)
	if err := rc.SetWriteDeadline(time.Now().Add(proxy.runTimeout + sseWriteTimeoutExtra)); err != nil {
		log.Printf("agent sse: set write deadline failed: %v", err)
	}

	threadID := fmt.Sprintf("default:%d:%s", userID, strings.TrimSpace(req.SessionID))
	events, errs := proxy.StreamRun(ctx, threadID, strings.TrimSpace(req.SessionID), req.Query, strconv.FormatInt(userID, 10))

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				// 事件流结束：带非取消类错误时补发一帧 error，让前端知晓失败原因
				if err := <-errs; err != nil && !errors.Is(err, context.Canceled) {
					writeSSE(c, flusher, Event{Event: EventError, Data: ErrorData{Code: "agent_error", Msg: err.Error()}})
				}
				return
			}
			writeSSE(c, flusher, ev)
		case <-ctx.Done():
			// 客户端断开：上游请求随 ctx 取消，直接退出
			return
		}
	}
}

// writeSSE 按 SSE 线格式写出一帧并立即 Flush。
// data 用紧凑 JSON（无换行），因此单行 data 即可，无需拆多行。
func writeSSE(c *gin.Context, flusher http.Flusher, ev Event) {
	data, err := json.Marshal(ev.Data)
	if err != nil {
		data = []byte("null") // 数据无法序列化时降级为 null，不中断整个流
	}
	_, _ = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", ev.Event, data)
	flusher.Flush()
}
