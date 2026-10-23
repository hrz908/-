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

// globalSem 全局连接闸门：进程内同时存活的 Agent WS 连接上限。
// 超出直接 503，避免连接数无限膨胀耗尽 fd / 内存。取满即拒（default 分支），不排队。
// 容量在 agent.Init 里按配置创建（config.agent.ws_max_conns，默认 512）。
var globalSem = make(chan struct{}, 512)

// upgrader 小程序端握手无法自定义请求头，也不带浏览器 Origin，故不做同源校验；
// 鉴权已在路由中间件（AuthAllowQueryToken）完成。写缓冲放大到 32KB，
// 适配 card/table 这类较大的结构化事件帧。
var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 32 * 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

const (
	wsWriteTimeout   = 5 * time.Second  // 单次写超时，防止慢客户端把推送 goroutine 挂死
	wsPongTimeout    = 60 * time.Second // 读空闲超时：60s 收不到 pong 判定半开连接并关闭
	wsPingInterval   = 25 * time.Second // 服务端心跳间隔
	wsMaxMessageSize = 64 * 1024        // 客户端消息上限，query 可能较长，给到 64KB
)

// agentConn 包装 websocket.Conn。
// gorilla/websocket 明确不支持并发写：心跳 ping、事件推送、关闭可能来自不同 goroutine，
// 全部经这把互斥锁串行化，否则会写坏帧甚至 panic。
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

// ServeWS 处理 GET /api/v1/ws/agent。
// 握手参数 ?token=（鉴权中间件消费）& session_id=；客户端发送 {"query":"..."}，
// 服务端以 Event JSON 帧推送运行过程。
func ServeWS(c *gin.Context) {
	userID := c.GetInt64("user_id")
	sessionID := strings.TrimSpace(c.Query("session_id"))

	// 全局连接闸门：满则 503，不升级连接
	select {
	case globalSem <- struct{}{}:
		defer func() { <-globalSem }()
	default:
		c.JSON(http.StatusServiceUnavailable, resp.Body{Code: http.StatusServiceUnavailable, Message: "服务繁忙，请稍后再试"})
		return
	}

	wsConn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		// 升级失败时 gorilla 已写好错误响应，这里只记录
		log.Printf("agent ws upgrade failed: user=%d err=%v", userID, err)
		return
	}
	ac := newAgentConn(wsConn)
	defer ac.close()

	// 连接级上下文：读循环退出（客户端断开）或心跳写失败时取消，
	// 联动停止 ping goroutine 与正在进行的运行。不依赖 c.Request.Context()，
	// 因为连接被 hijack 后 HTTP server 不再感知断开。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ac.pingLoop(ctx, cancel)
	ac.readLoop(ctx, userID, sessionID)
}

// pingLoop 服务端心跳：每 25s 发一个 ping。
// 写失败说明连接已死，主动 cancel 让读循环与运行中的请求尽快退出；
// 同时必须主动 Close 连接——cancel 只能取消能感知 ctx 的调用，
// 阻塞在 ReadMessage 里的读循环感知不到，最坏要等 60s 读超时才醒，
// 期间这条半开连接一直占着全局闸门槽位。
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

// readLoop 读取客户端消息并串行处理每次运行。
// 读到错误即视为断开，返回后触发 ServeWS 的 defer 链完成清理。
func (a *agentConn) readLoop(ctx context.Context, userID int64, sessionID string) {
	a.ws.SetReadLimit(wsMaxMessageSize)
	a.ws.SetPongHandler(func(string) error {
		// 收到 pong 续期读超时：活跃连接不会被误判为半开
		return a.ws.SetReadDeadline(time.Now().Add(wsPongTimeout))
	})

	for {
		// 每轮读之前刷新 deadline：handleRun 可能耗时远超 60s，期间不读消息，
		// 不刷新的话回到读循环时 deadline 早已过期，会立即误判断开。
		_ = a.ws.SetReadDeadline(time.Now().Add(wsPongTimeout))

		_, msg, err := a.ws.ReadMessage()
		if err != nil {
			return // 客户端断开或读超时
		}

		var req RunRequest
		if err := json.Unmarshal(msg, &req); err != nil {
			// 协议错误只回 error 事件，不断连：客户端可能是格式笔误，连接仍可复用
			_ = a.writeJSON(Event{Event: EventError, Data: ErrorData{Code: "bad_request", Msg: "无效的消息格式"}})
			continue
		}
		if strings.TrimSpace(req.Query) == "" {
			_ = a.writeJSON(Event{Event: EventError, Data: ErrorData{Code: "bad_request", Msg: "query 不能为空"}})
			continue
		}

		// 消息内 session_id 优先，回退到握手参数
		sid := sessionID
		if s := strings.TrimSpace(req.SessionID); s != "" {
			sid = s
		}

		a.handleRun(ctx, userID, sid, req.Query)
	}
}

// handleRun 处理一次运行：限流 → 代理流式请求 → 逐事件写回 WS。
// 任何失败都以 error 事件回传，绝不主动断连（连接可继续服务后续 query）。
func (a *agentConn) handleRun(ctx context.Context, userID int64, sessionID, query string) {
	// 三道限流闸门：失败发结构化 error 事件后直接返回，本次不占上游资源
	if err := limiter.Allow(ctx, userID); err != nil {
		_ = a.writeJSON(Event{Event: EventError, Data: ErrorData{Code: limitCode(err), Msg: err.Error()}})
		return
	}
	// Allow 通过即占用一个并发槽，无论后续成功失败都要归还。
	// ReleaseConcurrent 内部脱离 ctx，客户端断开导致 ctx 取消时仍能正常归还。
	defer limiter.ReleaseConcurrent(ctx, userID)

	// thread_id 由服务端派生，客户端无法伪造他人会话
	threadID := fmt.Sprintf("default:%d:%s", userID, sessionID)
	events, errs := proxy.StreamRun(ctx, threadID, sessionID, query, strconv.FormatInt(userID, 10))

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				// 事件流正常结束：若带有非取消类错误，补发一个 error 事件告知前端
				if err := <-errs; err != nil && !errors.Is(err, context.Canceled) {
					_ = a.writeJSON(Event{Event: EventError, Data: ErrorData{Code: "agent_error", Msg: err.Error()}})
				}
				return
			}
			if err := a.writeJSON(ev); err != nil {
				return // 写失败说明客户端已断开，停止转发
			}
		case <-ctx.Done():
			// 客户端断开或心跳失败：上游请求随 ctx 取消，这里直接退出
			return
		}
	}
}
