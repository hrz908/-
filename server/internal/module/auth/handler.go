// 包名取 authapi：与 internal/auth（JWT 签发/解析）区分开，
// 这个包是「登录接口」这个业务模块
package authapi

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"stock-agent-server/internal/auth"
	"stock-agent-server/internal/deps"
	"stock-agent-server/internal/model"
	"stock-agent-server/internal/resp"
	"stock-agent-server/internal/wechat"
)

type LoginReq struct {
	Code string `json:"code"` // 小程序 wx.login 拿到的 code
}

type LoginResp struct {
	Token    string `json:"token"`
	UserID   int64  `json:"user_id"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
	// 是不是本次刚建的号。前端靠它分流：
	// false 且已有资料 → 直接登录、不打扰用户；
	// true → 才引导用户去补头像昵称
	IsNew bool `json:"is_new"`
}

// Login POST /api/v1/auth/login
// 只做身份认证：code → openid → 查/建用户 → 签发 JWT，并把库里已有的资料一并返回。
// 不接收 nickname/avatar：头像昵称是微信的用户权限数据，得先拿到身份、
// 确认库里确实没有，才值得让用户去授权；每次登录都重选一遍纯属打扰。
// 补齐资料走 PUT /user/profile（那时已经有 token 了）
func Login(c *gin.Context) {
	ctx := c.Request.Context()

	var req LoginReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少登录 code")
		return
	}

	openid, err := openIDOf(req.Code)
	if err != nil {
		log.Printf("login resolve openid failed: %v", err)
		resp.Fail(c, resp.CodeBadGateway, "微信登录失败，请重试")
		return
	}

	u, isNew, err := findOrCreateUser(ctx, openid)
	if err != nil {
		log.Printf("login upsert user failed: openid=%s err=%v", openid, err)
		resp.Fail(c, resp.CodeInternal, "登录失败")
		return
	}

	token, err := auth.SignToken(u.ID)
	if err != nil {
		log.Printf("sign token failed: user=%d err=%v", u.ID, err)
		resp.Fail(c, resp.CodeInternal, "登录失败")
		return
	}

	resp.OK(c, LoginResp{
		Token:    token,
		UserID:   u.ID,
		Nickname: u.Nickname,
		Avatar:   u.Avatar,
		IsNew:    isNew,
	})
}

// openIDOf code → openid。
// 配了 AppID/Secret 就走微信；没配就退化为固定演示账号，
// 保证本地没接微信时也能把链路（登录 → 建号 → 存资料）跑通
func openIDOf(code string) (string, error) {
	if wechat.Enabled() {
		session, err := wechat.Code2Session(strings.TrimSpace(code))
		if err != nil {
			return "", err
		}
		return session.OpenID, nil
	}
	// 演示账号：所有人共用同一个 openid，方便本地联调。
	// 注意这与旧实现（把 code 哈希成 uid）不同——旧做法每次登录都是一个新账号，
	// 资料会丢；改成固定 openid 后同一个演示账号的资料能留住。
	// prod 环境由 main 强制要求配置微信凭据，不会走到这里
	return "demo", nil
}

// findOrCreateUser 按 openid 查用户，没有就建（返回 isNew=true）。
// 已有的用户原样返回，一个字段都不动——重复登录不该改动用户自己设好的资料。
// 新建时昵称先用默认值占位：真正的值要等用户在前端授权后再由 PUT /user/profile 覆盖，
// 默认值同时是前端判断「资料还没完善」的依据（与前端的 DEFAULT_NICKNAME 保持一致）
func findOrCreateUser(ctx context.Context, openid string) (*model.User, bool, error) {
	var u model.User
	err := deps.DB.WithContext(ctx).Where("openid = ?", openid).First(&u).Error

	switch {
	case err == nil:
		return &u, false, nil

	case errors.Is(err, gorm.ErrRecordNotFound):
		// 并发首次登录时两个请求都会走到这里：用 OnConflict DoNothing 让
		// 后到者静默跳过（撞 uk_users_openid），再查一次拿回已有行，
		// 否则它会拿到一个 500
		nu := model.User{OpenID: openid, Nickname: "微信用户"}
		if err := deps.DB.WithContext(ctx).
			Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "openid"}}, DoNothing: true}).
			Create(&nu).Error; err != nil {
			return nil, false, err
		}
		if nu.ID == 0 {
			// 冲突了：行已存在，重新查出来
			if err := deps.DB.WithContext(ctx).Where("openid = ?", openid).First(&nu).Error; err != nil {
				return nil, false, err
			}
			return &nu, false, nil
		}
		return &nu, true, nil

	default:
		return nil, false, err
	}
}
