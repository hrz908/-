// Package wechat 封装小程序侧的开放接口调用。
// 目前只有 code2session：用 wx.login 拿到的 code 换 openid
package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"stock-agent-server/internal/httpclient"
)

const code2SessionURL = "https://api.weixin.qq.com/sns/jscode2session"

// AppID / Secret 由 main 启动时通过 Init 注入（配置来源唯一，运行期不再读取）。
// 两者都为空表示「未接入微信」，登录会退化成演示账号，见 module/auth
var (
	AppID  string
	Secret string
	// httpClient 复用连接：登录是高频短请求，复用省掉每次 TLS 握手
	httpClient = httpclient.New(5 * time.Second)
)

// Init 注入小程序凭据与上游超时，main 启动时调用一次
func Init(appID, secret string, timeout time.Duration) {
	AppID = appID
	Secret = secret
	if timeout > 0 {
		httpClient = httpclient.New(timeout)
	}
}

// Session 微信返回的登录态。
// 只保留用到的 openid：unionid / session_key 全仓没有读取方，
// 存着只会让人误以为后续逻辑依赖它们
type Session struct {
	OpenID string
}

type code2SessionResp struct {
	OpenID  string `json:"openid"`
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// maxRespBytes 微信响应体读取上限：jscode2session 只回一小段 JSON，
// 64KB 绰绰有余；不限制的话上游异常时的超大响应会打爆内存
const maxRespBytes = 64 << 10

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

	// 请求级 ctx 超时兜底：即使 Client.Timeout 因故未生效（如被整体替换），
	// 单次调用也不会无限期挂起登录请求
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, code2SessionURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out code2SessionResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRespBytes)).Decode(&out); err != nil {
		return nil, err
	}
	if out.ErrCode != 0 || strings.TrimSpace(out.OpenID) == "" {
		return nil, fmt.Errorf("jscode2session failed: errcode=%d errmsg=%s", out.ErrCode, strings.TrimSpace(out.ErrMsg))
	}

	return &Session{OpenID: out.OpenID}, nil
}
