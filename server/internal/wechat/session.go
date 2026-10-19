// Package wechat 封装小程序侧的开放接口调用。
// 目前只有 code2session：用 wx.login 拿到的 code 换 openid
package wechat

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const code2SessionURL = "https://api.weixin.qq.com/sns/jscode2session"

// AppID / Secret 由 main 启动时从配置注入（与 quote.QuoteAPIBase 同一套路）。
// 两者都为空表示「未接入微信」，登录会退化成演示账号，见 module/auth
var (
	AppID  string
	Secret string
)

// Session 微信返回的登录态
type Session struct {
	OpenID     string
	UnionID    string
	SessionKey string
}

type code2SessionResp struct {
	OpenID     string `json:"openid"`
	UnionID    string `json:"unionid"`
	SessionKey string `json:"session_key"`
	ErrCode    int    `json:"errcode"`
	ErrMsg     string `json:"errmsg"`
}

var httpClient = &http.Client{Timeout: 5 * time.Second}

// Enabled 是否已配置小程序凭据。没配置时不要去调微信接口，
// 否则每个登录请求都会白白等一次网络失败
func Enabled() bool { return AppID != "" && Secret != "" }

// Code2Session 用 code 换 openid。
// 微信的错误也写在 200 的响应体里（errcode），所以不能只看 HTTP 状态码
func Code2Session(code string) (*Session, error) {
	if !Enabled() {
		return nil, errors.New("wechat appid/secret not configured")
	}

	q := url.Values{}
	q.Set("appid", AppID)
	q.Set("secret", Secret)
	q.Set("js_code", code)
	q.Set("grant_type", "authorization_code")

	resp, err := httpClient.Get(code2SessionURL + "?" + q.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out code2SessionResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.ErrCode != 0 || strings.TrimSpace(out.OpenID) == "" {
		return nil, fmt.Errorf("jscode2session failed: errcode=%d errmsg=%s", out.ErrCode, strings.TrimSpace(out.ErrMsg))
	}

	return &Session{OpenID: out.OpenID, UnionID: out.UnionID, SessionKey: out.SessionKey}, nil
}
