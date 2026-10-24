// Package wecom 封装企业微信自建应用的消息推送。
//
// 用途：把命中的股票提醒作为「应用消息」推给企业成员，与小程序内 WS 实时提醒并存。
// 自建应用只能发给企业内部 userid，小程序用户表只有 openid、无法映射，
// 因此这里统一推送给 @all。
package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"stock-agent-server/internal/httpclient"
)

const (
	tokenURL = "https://qyapi.weixin.qq.com/cgi-bin/gettoken"
	sendURL  = "https://qyapi.weixin.qq.com/cgi-bin/message/send"

	// tokenRefreshAhead access_token 官方有效期 7200 秒，提前 5 分钟判过期，避免用到临界失效的 token
	tokenRefreshAhead = 5 * time.Minute

	// fallbackTokenTTL 接口未返回 expires_in 时的兜底有效期
	fallbackTokenTTL = 2 * time.Hour
)

// 企业微信自建应用凭据，由 main 通过 Init 注入（同 wechat.Init）。
// 任一为空 / 为 0 表示「未接入企业微信」，提醒只走 WebSocket。
var (
	CorpID  string // 企业 ID（corpid）
	Secret  string // 自建应用的 secret
	AgentID int    // 自建应用的 agentid
	// httpClient 复用连接，提醒推送是低频短请求
	httpClient = httpclient.New(5 * time.Second)
)

// Init 注入企业微信凭据与上游超时，main 启动时调用一次。
func Init(corpID, secret string, agentID int, timeout time.Duration) {
	CorpID = corpID
	Secret = secret
	AgentID = agentID
	if timeout > 0 {
		httpClient = httpclient.New(timeout)
	}
}

// maxRespBytes 企业微信响应体上限：gettoken / send 都只回一小段 JSON，
// 防上游异常时的超大响应打爆内存。
const maxRespBytes = 64 << 10

// doJSON 请求企业微信接口并把限长响应解码到 out。
// 每次调用带独立 5s 超时：推送跑在提醒检查的异步 goroutine 里，挂着不返回会占住 goroutine。
func doJSON(method, url string, body io.Reader, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, maxRespBytes)).Decode(out)
}

// Enabled 是否已配置企业微信凭据；未配置时不要调接口，否则每条提醒都白等一次网络失败。
func Enabled() bool { return CorpID != "" && Secret != "" && AgentID != 0 }

// access_token 内存缓存：有效期内复用。单实例足够；
// 企业微信对同一应用重复获取会返回相同 token，多实例各自缓存也互不影响。
var (
	mu          sync.Mutex
	cachedToken string
	tokenExpire time.Time
)

// accessToken 取未过期的 token，优先用缓存。
// 企业微信的错误也写在 200 响应体的 errcode 里，不能只看 HTTP 状态码。
func accessToken() (string, error) {
	mu.Lock()
	defer mu.Unlock()

	if cachedToken != "" && time.Now().Before(tokenExpire) {
		return cachedToken, nil
	}

	q := url.Values{}
	q.Set("corpid", CorpID)
	q.Set("corpsecret", Secret)

	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
	}
	if err := doJSON(http.MethodGet, tokenURL+"?"+q.Encode(), nil, &out); err != nil {
		return "", err
	}
	if out.ErrCode != 0 || out.AccessToken == "" {
		return "", fmt.Errorf("gettoken failed: errcode=%d errmsg=%s", out.ErrCode, out.ErrMsg)
	}

	ttl := fallbackTokenTTL
	if out.ExpiresIn > 0 {
		ttl = time.Duration(out.ExpiresIn) * time.Second
	}
	if ttl > tokenRefreshAhead {
		ttl -= tokenRefreshAhead
	}

	cachedToken = out.AccessToken
	tokenExpire = time.Now().Add(ttl)
	return cachedToken, nil
}

// invalidateToken 清掉缓存的 token，用于发送返回 token 失效类错误（40014/41001/42001）后自愈。
func invalidateToken() {
	mu.Lock()
	cachedToken = ""
	tokenExpire = time.Time{}
	mu.Unlock()
}

// SendTextToAll 向全体成员（@all）推送一条文本消息。
// 未配置凭据时静默跳过并返回 nil，调用方无需判断是否接入。
func SendTextToAll(content string) error {
	if !Enabled() {
		return nil
	}
	if content == "" {
		return errors.New("empty content")
	}

	token, err := accessToken()
	if err != nil {
		return err
	}

	payload := map[string]any{
		"touser":  "@all",
		"msgtype": "text",
		"agentid": AgentID,
		"text":    map[string]any{"content": content},
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	var out struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := doJSON(http.MethodPost, sendURL+"?access_token="+url.QueryEscape(token),
		bytes.NewReader(buf), &out); err != nil {
		return err
	}
	if out.ErrCode != 0 {
		// token 失效：清缓存，下次调用重新获取
		if out.ErrCode == 40014 || out.ErrCode == 41001 || out.ErrCode == 42001 {
			invalidateToken()
		}
		return fmt.Errorf("send message failed: errcode=%d errmsg=%s", out.ErrCode, out.ErrMsg)
	}
	return nil
}
