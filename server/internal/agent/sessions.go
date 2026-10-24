package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"stock-agent-server/internal/middleware"
	"stock-agent-server/internal/resp"
)

// 会话历史网关接口（2026-10-24，小程序「历史对话」抽屉）。
// 会话注册表（chat_sessions）与 checkpoint 都由 Python Agent 持有，
// 网关只做三件事：鉴权后取 user_id、转发、按统一响应体返回。
// 不直接碰数据库——checkpoint 的创建/删除必须走 LangGraph 的
// saver API（adelete_thread），绕过它手工删表会留下孤儿 writes/blobs。

// sessionItem 会话列表条目，与 Agent 侧 /sessions 返回结构一一对应。
type sessionItem struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
}

// messageItem 会话消息条目（恢复上下文用）。
type messageItem struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// validSessionID 与 Agent 侧 sessions.valid_session_id 同口径：
// session_id 会拼进 thread_id 与 Redis key，只放行安全字符。
func validSessionID(sid string) bool {
	if sid == "" || len(sid) > 64 {
		return false
	}
	for i := 0; i < len(sid); i++ {
		c := sid[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') &&
			(c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// ServeListSessions 处理 GET /api/v1/agent/sessions：历史对话列表。
func ServeListSessions(c *gin.Context) {
	userID := c.GetInt64("user_id")

	var out struct {
		Sessions []sessionItem `json:"sessions"`
	}
	if err := proxy.callJSON(c.Request.Context(), http.MethodGet,
		"/sessions?user_id="+strconv.FormatInt(userID, 10), nil, &out); err != nil {
		log.Printf("rid=%s agent sessions list failed: %v", middleware.RequestIDFrom(c), err)
		resp.Fail(c, resp.CodeBadGateway, "会话列表获取失败")
		return
	}
	resp.OK(c, gin.H{"sessions": out.Sessions})
}

// ServeCreateSession 处理 POST /api/v1/agent/sessions：注册新对话。
// 「新对话」按钮在跳转前调用，让空会话立即出现在历史列表里
// （首条消息之前就有条目，标题先显示「新对话」）。
func ServeCreateSession(c *gin.Context) {
	userID := c.GetInt64("user_id")

	var req struct {
		SessionID string `json:"session_id"`
		Title     string `json:"title"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "请求体格式错误")
		return
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if !validSessionID(req.SessionID) {
		resp.Fail(c, resp.CodeBadRequest, "session_id 不合法")
		return
	}

	body := map[string]string{
		"user_id":    strconv.FormatInt(userID, 10),
		"session_id": req.SessionID,
		"title":      strings.TrimSpace(req.Title),
	}
	if err := proxy.callJSON(c.Request.Context(), http.MethodPost, "/sessions", body, nil); err != nil {
		log.Printf("rid=%s agent session create failed: %v", middleware.RequestIDFrom(c), err)
		resp.Fail(c, resp.CodeBadGateway, "会话创建失败")
		return
	}
	resp.OK(c, gin.H{"session_id": req.SessionID})
}

// ServeSessionMessages 处理 GET /api/v1/agent/sessions/:sid/messages：
// 恢复某会话的纯文本消息（历史列表点选后加载）。
func ServeSessionMessages(c *gin.Context) {
	userID := c.GetInt64("user_id")
	sid := strings.TrimSpace(c.Param("sid"))
	if !validSessionID(sid) {
		resp.Fail(c, resp.CodeBadRequest, "session_id 不合法")
		return
	}

	var out struct {
		Messages []messageItem `json:"messages"`
	}
	path := fmt.Sprintf("/sessions/%s/messages?user_id=%s", sid, strconv.FormatInt(userID, 10))
	if err := proxy.callJSON(c.Request.Context(), http.MethodGet, path, nil, &out); err != nil {
		log.Printf("rid=%s agent session messages failed: %v", middleware.RequestIDFrom(c), err)
		resp.Fail(c, resp.CodeBadGateway, "会话消息获取失败")
		return
	}
	resp.OK(c, gin.H{"messages": out.Messages})
}

// ServeDeleteSession 处理 DELETE /api/v1/agent/sessions/:sid：
// 删除会话（注册行 + checkpoints + Redis 摘要，由 Agent 侧完成）。
func ServeDeleteSession(c *gin.Context) {
	userID := c.GetInt64("user_id")
	sid := strings.TrimSpace(c.Param("sid"))
	if !validSessionID(sid) {
		resp.Fail(c, resp.CodeBadRequest, "session_id 不合法")
		return
	}

	var out struct {
		Deleted bool `json:"deleted"`
	}
	path := fmt.Sprintf("/sessions/%s?user_id=%s", sid, strconv.FormatInt(userID, 10))
	if err := proxy.callJSON(c.Request.Context(), http.MethodDelete, path, nil, &out); err != nil {
		log.Printf("rid=%s agent session delete failed: %v", middleware.RequestIDFrom(c), err)
		resp.Fail(c, resp.CodeBadGateway, "会话删除失败")
		return
	}
	resp.OK(c, gin.H{"deleted": out.Deleted})
}

// callJSON 非流式代理：转发到 Python Agent 并解析 JSON 响应体。
// 与 StreamRun 的分工：那个管 ndjson 长流（每问一请求），这个管普通
// 请求-响应式接口（会话 CRUD）。out 为 nil 时不解析响应体。
func (p *AgentProxy) callJSON(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 带出上游错误正文（限长），与 StreamRun 的错误口径一致
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("agent service returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

// registerSession 把本轮会话 upsert 进注册表（标题取首个问题）。
// 旁路逻辑：失败只记日志，绝不阻断对话流——历史列表晚出现一条，
// 远比用户问不出话好。2s 超时兜底，防上游卡死拖住整轮 run。
// WS / SSE 两条入口都经 StreamRun，在这里统一做，不用各自调用。
func (p *AgentProxy) registerSession(ctx context.Context, sessionID, userID, query string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	body, err := json.Marshal(map[string]string{
		"user_id":    userID,
		"session_id": sessionID,
		"title":      truncateRunes(query, 40),
	})
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, p.baseURL+"/sessions", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		log.Printf("agent proxy: register session %s failed: %v", sessionID, err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
}

// truncateRunes 按字符（而非字节）截断，避免把中文 URL/标题拦腰切断成乱码。
func truncateRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n])
}
