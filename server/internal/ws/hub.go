package ws

import (
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// 小程序端握手时无法自定义请求头，因此不做浏览器式同源校验；
// 上线时建议改成 Origin 白名单
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

const (
	// writeTimeout 单次写超时：只连不读的慢客户端不能把推送方无限期挂住
	// （推送是在 refresh 的 goroutine 里同步发起的）
	writeTimeout = 5 * time.Second

	// readTimeout 读空闲超时，配合 pong 续期使用。
	// 移动端网络切换后 TCP 可能进入半开状态：没有超时的话连接条目与
	// goroutine 会一起泄漏，Push 还会一直往死连接写
	readTimeout = 60 * time.Second

	// maxMessageSize 读上限。本连接只读不发，4KB 足够
	maxMessageSize = 4096
)

// 连接上限（由 Configure 按配置注入，默认档即迁移前硬编码值 + 新增全局上限）：
// 只有每用户上限时，海量不同账号仍能把进程级连接数打满，需要全局兜底
var (
	maxConnsPerUser = 5
	maxConnsTotal   = int64(2048)
	totalConns      atomic.Int64
)

// Configure 注入连接上限，main 启动时调用一次（非法值保留默认档）。
func Configure(perUser, maxTotal int) {
	if perUser > 0 {
		maxConnsPerUser = perUser
	}
	if maxTotal > 0 {
		maxConnsTotal = int64(maxTotal)
	}
}

// conn 包一层：gorilla/websocket 明确不支持并发写同一连接，
// 推送、关闭都走这把锁，否则并发推送会写坏帧甚至 panic
type conn struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func newConn(ws *websocket.Conn) *conn { return &conn{ws: ws} }

func (c *conn) writeJSON(msg any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.ws.WriteJSON(msg)
}

func (c *conn) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.ws.Close()
}

// hub 连接中心：同一账号可能开多个页面/设备，用集合存连接
type hub struct {
	mu    sync.Mutex
	conns map[int64]map[*conn]struct{}
}

var h = &hub{conns: make(map[int64]map[*conn]struct{})}

// add 返回 false 表示已达单用户或全局连接上限，调用方应直接拒绝
func (u *hub) add(userID int64, c *conn) bool {
	if totalConns.Load() >= maxConnsTotal {
		return false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.conns[userID]) >= maxConnsPerUser {
		return false
	}
	if u.conns[userID] == nil {
		u.conns[userID] = make(map[*conn]struct{})
	}
	u.conns[userID][c] = struct{}{}
	totalConns.Add(1)
	return true
}

func (u *hub) remove(userID int64, c *conn) {
	u.mu.Lock()
	defer u.mu.Unlock()
	set, ok := u.conns[userID]
	if !ok {
		return
	}
	if _, exists := set[c]; exists {
		delete(set, c)
		totalConns.Add(-1)
	}
	if len(set) == 0 {
		delete(u.conns, userID)
	}
}

func (u *hub) list(userID int64) []*conn {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]*conn, 0, len(u.conns[userID]))
	for c := range u.conns[userID] {
		out = append(out, c)
	}
	return out
}

// CloseAll 关闭全部连接。优雅关闭时用：
// srv.Shutdown 不会等待被 hijack 的长连接，不主动关会一直等到超时才退出
func (u *hub) CloseAll() {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, set := range u.conns {
		for c := range set {
			c.close()
		}
	}
	u.conns = make(map[int64]map[*conn]struct{})
	totalConns.Store(0)
}

// CloseAll 关闭全部连接（优雅关闭用）
func CloseAll() { h.CloseAll() }

// Push 给指定用户推消息（提示词约定里的 pushToUser）。
// 写失败的连接直接摘掉，避免坏连接一直占着
func Push(userID int64, msg map[string]any) {
	for _, c := range h.list(userID) {
		if err := c.writeJSON(msg); err != nil {
			log.Printf("push to user %d failed: %v", userID, err)
			h.remove(userID, c)
			c.close()
		}
	}
}

// Serve GET /api/v1/ws
// user_id 由鉴权中间件注入；握手不能带自定义请求头，token 走 ?token= 兜底
// （该兜底只对 /ws 一个路由开放，见 router）
func Serve(c *gin.Context) {
	userID := c.GetInt64("user_id")

	raw, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("ws upgrade failed: user=%d err=%v", userID, err)
		return
	}
	conn := newConn(raw)
	if !h.add(userID, conn) {
		log.Printf("ws conn limit reached, reject: user=%d", userID)
		conn.close()
		return
	}
	defer func() {
		h.remove(userID, conn)
		conn.close()
	}()

	// 只读：客户端消息无业务用途，读到错误即视为断开。
	// 读超时靠 pong 续期：客户端（或中间网络设备）的 pong 回应会刷新 deadline，
	// 长时间没有回应就会被判为半开连接，最终走完 deferred 清理
	raw.SetReadLimit(maxMessageSize)
	_ = raw.SetReadDeadline(time.Now().Add(readTimeout))
	raw.SetPongHandler(func(string) error {
		return raw.SetReadDeadline(time.Now().Add(readTimeout))
	})

	for {
		if _, _, err := raw.ReadMessage(); err != nil {
			return
		}
	}
}
