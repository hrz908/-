package ws

import (
	"log"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// 小程序端握手时无法自定义请求头，因此不做浏览器式同源校验；
// 上线时建议改成 Origin 白名单
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// hub 连接中心：同一账号可能开多个页面/设备，用集合存连接
type hub struct {
	mu    sync.Mutex
	conns map[int64]map[*websocket.Conn]struct{}
}

var h = &hub{conns: make(map[int64]map[*websocket.Conn]struct{})}

func (u *hub) add(userID int64, conn *websocket.Conn) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.conns[userID] == nil {
		u.conns[userID] = make(map[*websocket.Conn]struct{})
	}
	u.conns[userID][conn] = struct{}{}
}

func (u *hub) remove(userID int64, conn *websocket.Conn) {
	u.mu.Lock()
	defer u.mu.Unlock()
	set, ok := u.conns[userID]
	if !ok {
		return
	}
	delete(set, conn)
	if len(set) == 0 {
		delete(u.conns, userID)
	}
}

func (u *hub) list(userID int64) []*websocket.Conn {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]*websocket.Conn, 0, len(u.conns[userID]))
	for c := range u.conns[userID] {
		out = append(out, c)
	}
	return out
}

// Push 给指定用户推消息（提示词约定里的 pushToUser）。
// 写失败的连接直接摘掉，避免坏连接一直占着
func Push(userID int64, msg map[string]any) {
	for _, conn := range h.list(userID) {
		if err := conn.WriteJSON(msg); err != nil {
			log.Printf("push to user %d failed: %v", userID, err)
			h.remove(userID, conn)
			conn.Close()
		}
	}
}

// Serve GET /api/v1/ws
// user_id 由 auth 中间件注入（握手不能带自定义头，token 走 ?token= 兜底）
func Serve(c *gin.Context) {
	userID := c.GetInt64("user_id")

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("ws upgrade failed: user=%d err=%v", userID, err)
		return
	}
	h.add(userID, conn)
	defer func() {
		h.remove(userID, conn)
		conn.Close()
	}()

	// 只读：客户端消息无业务用途，读到错误即视为断开
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
