// 包名取 authapi：与 internal/auth（JWT 签发/解析）区分开，
// 这个包是「登录接口」这个业务模块
package authapi

import (
	"hash/fnv"
	"log"
	"strings"

	"github.com/gin-gonic/gin"

	"stock-agent-server/internal/auth"
	"stock-agent-server/internal/resp"
)

type LoginReq struct {
	Code string `json:"code"` // 小程序 wx.login 拿到的 code
}

type LoginResp struct {
	Token  string `json:"token"`
	UserID int64  `json:"user_id"`
}

// Login POST /api/v1/auth/login
// 真实流程：code → 微信 code2Session 换 openid → 落库/查用户 → 签发 JWT。
// 这里保留契约，openid 换取处留 TODO，方便接入你自己的鉴权服务
func Login(c *gin.Context) {
	var req LoginReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少登录 code")
		return
	}

	// TODO: 调微信 jscode2session 换 openid，再按 openid 查/建用户
	userID := demoUserID(req.Code)

	token, err := auth.SignToken(userID)
	if err != nil {
		log.Printf("sign token failed: user=%d err=%v", userID, err)
		resp.Fail(c, resp.CodeInternal, "登录失败")
		return
	}

	resp.OK(c, LoginResp{Token: token, UserID: userID})
}

// demoUserID 演示用：把 code 哈希成一个稳定 uid，真实项目请换成 openid → uid
func demoUserID(code string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.TrimSpace(code)))
	return int64(h.Sum64()%1000000) + 1
}
