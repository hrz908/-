// Package wecom 封装企业微信（WeCom）自建应用的消息推送。
//
// 用途：把命中的股票提醒作为「应用消息」推送给企业成员，
// 与小程序内的 WebSocket 实时提醒并存（两条通道互不替代）。
//
// 自建应用只能把消息发给企业内部的成员 userid，小程序用户表里只有 openid、
// 无法映射到企业微信成员，因此这里统一推送给 @all（全体企业成员）。
package wecom

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	tokenURL = "https://qyapi.weixin.qq.com/cgi-bin/gettoken"
	sendURL  = "https://qyapi.weixin.qq.com/cgi-bin/message/send"

	// tokenRefreshAhead access_token 官方有效期 7200 秒，
	// 提前 5 分钟判为过期，避免用到临界失效的 token
	tokenRefreshAhead = 5 * time.Minute

	// fallbackTokenTTL 接口未返回 expires_in 时的兜底有效期
	fallbackTokenTTL = 2 * time.Hour
)

// 企业微信自建应用凭据，由 main 启动时从配置注入（与 wechat.AppID 同一套路）。
// 三者任一为空 / 为 0 表示「未接入企业微信」，提醒只走 WebSocket，不推送
var (
	CorpID  string // 企业 ID（corpid）
	Secret  string // 自建应用的 secret
	AgentID int    // 自建应用的 agentid
)

var httpClient = &http.Client{Timeout: 5 * time.Second}

// Enabled 是否已配置企业微信凭据。没配置时不要去调企业微信接口，
// 否则每条提醒都会白白等一次网络失败
func Enabled() bool { return CorpID != "" && Secret != "" && AgentID != 0 }

// access_token 内存缓存：有效期内复用，过期重新获取。
// 单实例部署足够；企业微信对同一应用的 token 重复获取会返回相同值，
// 多实例各自缓存也不会互相失效
var (
	mu          sync.Mutex
	cachedToken string
	tokenExpire time.Time
)

// accessToken 取一个未过期的 access_token，优先用缓存。
// 企业微信的错误也写在 200 的响应体里（errcode），所以不能只看 HTTP 状态码
func accessToken() (string, error) {
	mu.Lock()
	defer mu.Unlock()

	if cachedToken != "" && time.Now().Before(tokenExpire) {
		return cachedToken, nil
	}

	q := url.Values{}
	q.Set("corpid", CorpID)
	q.Set("corpsecret", Secret)

	resp, err := httpClient.Get(tokenURL + "?" + q.Encode())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
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

// invalidateToken 清掉缓存的 token，强制下次重新获取。
// 用于消息发送返回 token 失效类错误（40014/41001/42001）后自愈
func invalidateToken() {
	mu.Lock()
	cachedToken = ""
	tokenExpire = time.Time{}
	mu.Unlock()
}

// SendTextToAll 向全体成员（@all）推送一条文本消息。
// 未配置企业微信凭据时静默跳过并返回 nil，调用方无需判断是否接入
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

	resp, err := httpClient.Post(sendURL+"?access_token="+url.QueryEscape(token),
		"application/json", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var out struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if out.ErrCode != 0 {
		// token 失效：清缓存，下次调用重新获取，实现自愈
		if out.ErrCode == 40014 || out.ErrCode == 41001 || out.ErrCode == 42001 {
			invalidateToken()
		}
		return fmt.Errorf("send message failed: errcode=%d errmsg=%s", out.ErrCode, out.ErrMsg)
	}
	return nil
}
