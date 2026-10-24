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
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"stock-agent-server/internal/resp"
)

// globalSem 全局连接闸门：进程内 WS 连接上限，取满即拒（503）不排队，避免耗尽 fd。
// 容量由 agent.Init 按 config.agent.ws_max_conns 重建（默认 512）。
var globalSem = make(chan struct{}, 512)

// upgrader 小程序端握手无法带自定义头与 Origin，故不做同源校验（鉴权走 AuthAllowQueryToken）；
// 写缓冲 32KB 以容纳 card/table 等较大事件帧。
var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 32 * 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

const (
	wsWriteTimeout   = 5 * time.Second  // 单次写超时，防慢客户端挂死推送 goroutine
	wsPongTimeout    = 60 * time.Second // 读空闲上限：无 pong 即判半开连接
	wsPingInterval   = 25 * time.Second // 服务端心跳间隔
	wsMaxMessageSize = 64 * 1024        // 客户端消息上限，query 可能较长
)

// agentConn 包装 websocket.Conn；gorilla/websocket 禁止并发写，
// 心跳、推送、关闭统一经 mu 串行化，否则会写坏帧。
type agentConn struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func newAgentConn(ws *websocket.Conn) *agentConn { return &agentConn{ws: ws} }

func (a *agentConn) writeJSON(v any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = a.ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return a.ws.WriteJSON(v)
}

func (a *agentConn) writePing() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = a.ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return a.ws.WriteMessage(websocket.PingMessage, nil)
}

func (a *agentConn) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = a.ws.Close()
}

// ServeWS 处理 GET /api/v1/ws/agent；token 由鉴权中间件消费，session_id 取自 query。
// 客户端发送 {"query":"..."}，服务端以 Event JSON 帧推送运行过程。
func ServeWS(c *gin.Context) {
	userID := c.GetInt64("user_id")
	sessionID := strings.TrimSpace(c.Query("session_id"))

	// 闸门满则 503，不升级连接
	select {
	case globalSem <- struct{}{}:
		defer func() { <-globalSem }()
	default:
		c.JSON(http.StatusServiceUnavailable, resp.Body{Code: http.StatusServiceUnavailable, Message: "服务繁忙，请稍后再试"})
		return
	}

	wsConn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		// gorilla 已写好错误响应，仅记录
		log.Printf("agent ws upgrade failed: user=%d err=%v", userID, err)
		return
	}
	ac := newAgentConn(wsConn)
	defer ac.close()

	// 连接级上下文：读循环退出或心跳写失败时取消，联动停止 ping 与进行中的运行。
	// 不用 c.Request.Context()：连接 hijack 后 HTTP server 不再感知断开。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ac.pingLoop(ctx, cancel)
	ac.readLoop(ctx, userID, sessionID)
}

// pingLoop 每 25s 发一次 ping；写失败说明连接已死，cancel 让读循环与运行尽快退出。
// 还必须主动 Close：cancel 唤不醒阻塞在 ReadMessage 的读循环，
// 最坏要等 60s 读超时才释放全局闸门槽位。
func (a *agentConn) pingLoop(ctx context.Context, cancel context.CancelFunc) {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	defer a.close()
	for {
		select {
		case <-ticker.C:
			if err := a.writePing(); err != nil {
				cancel()
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// readLoop 串行读取并处理客户端消息；读错误即视为断开，返回后由 ServeWS 的 defer 清理。
func (a *agentConn) readLoop(ctx context.Context, userID int64, sessionID string) {
	a.ws.SetReadLimit(wsMaxMessageSize)
	a.ws.SetPongHandler(func(string) error {
		// 收到 pong 续期读超时，活跃连接不被误判半开
		return a.ws.SetReadDeadline(time.Now().Add(wsPongTimeout))
	})

	for {
		// 每轮读前刷新 deadline：handleRun 可能远超 60s，
		// 否则回到此处时 deadline 已过期，会立即误判断开。
		_ = a.ws.SetReadDeadline(time.Now().Add(wsPongTimeout))

		_, msg, err := a.ws.ReadMessage()
		if err != nil {
			return // 客户端断开或读超时
		}

		var req RunRequest
		if err := json.Unmarshal(msg, &req); err != nil {
			// 协议错误只回 error 事件不断连，连接仍可复用
			_ = a.writeJSON(Event{Event: EventError, Data: ErrorData{Code: "bad_request", Msg: "无效的消息格式"}})
			continue
		}
		if strings.TrimSpace(req.Query) == "" {
			_ = a.writeJSON(Event{Event: EventError, Data: ErrorData{Code: "bad_request", Msg: "query 不能为空"}})
			continue
		}

		// 消息内 session_id 优先，回退握手参数
		sid := sessionID
		if s := strings.TrimSpace(req.SessionID); s != "" {
			sid = s
		}

		a.handleRun(ctx, userID, sid, req.Query)
	}
}

// handleRun 处理一次运行：限流 → 代理流式请求 → 逐事件写回。
// 任何失败都以 error 事件回传，不主动断连。
func (a *agentConn) handleRun(ctx context.Context, userID int64, sessionID, query string) {
	// 三道限流闸门，拒绝时回结构化 error 事件，不占用上游资源
	if err := limiter.Allow(ctx, userID); err != nil {
		_ = a.writeJSON(Event{Event: EventError, Data: ErrorData{Code: limitCode(err), Msg: err.Error()}})
		return
	}
	// Allow 通过即占用并发槽，无论成败都要归还；
	// ReleaseConcurrent 内部脱离 ctx，客户端断开时仍能归还
	defer limiter.ReleaseConcurrent(ctx, userID)

	// thread_id 由服务端派生，客户端无法伪造他人会话
	threadID := fmt.Sprintf("default:%d:%s", userID, sessionID)
	events, errs := proxy.StreamRun(ctx, threadID, sessionID, query, strconv.FormatInt(userID, 10))

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				// 事件流结束：非取消类错误补发 error 事件告知前端
				if err := <-errs; err != nil && !errors.Is(err, context.Canceled) {
					_ = a.writeJSON(Event{Event: EventError, Data: ErrorData{Code: "agent_error", Msg: err.Error()}})
				}
				return
			}
			if err := a.writeJSON(ev); err != nil {
				return // 写失败说明客户端已断开
			}
		case <-ctx.Done():
			// 客户端断开或心跳失败：上游请求随 ctx 取消
			return
		}
	}
}
