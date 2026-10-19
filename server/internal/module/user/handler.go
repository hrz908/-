// Package user 用户资料：查询、修改、头像上传。
// user_id 只从鉴权中间件注入的 context 取，不信任请求体里的任何身份字段
package user

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"stock-agent-server/internal/deps"
	"stock-agent-server/internal/resp"
	"stock-agent-server/internal/upload"
)

// 昵称长度上限，与小程序侧 NICKNAME_MAX 保持一致。
// 按字符数算（中文一个字算一个），用 utf8.RuneCountInString 而不是 len
const nicknameMax = 20

// avatarMaxBytes 头像大小上限 2MB
const avatarMaxBytes = 2 << 20

type ProfileResp struct {
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}

type UpdateProfileReq struct {
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}

// GetProfile GET /api/v1/user/profile
func GetProfile(c *gin.Context) {
	u, ok := current(c)
	if !ok {
		return
	}
	resp.OK(c, ProfileResp{Nickname: u.Nickname, Avatar: u.Avatar})
}

// UpdateProfile PUT /api/v1/user/profile
// 昵称截断到 nicknameMax 个字符；avatar 传空串表示不改动
func UpdateProfile(c *gin.Context) {
	u, ok := current(c)
	if !ok {
		return
	}

	var req UpdateProfileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误")
		return
	}

	nickname := strings.TrimSpace(req.Nickname)
	if nickname == "" {
		resp.Fail(c, resp.CodeBadRequest, "昵称不能为空")
		return
	}
	if utf8.RuneCountInString(nickname) > nicknameMax {
		nickname = trimRunes(nickname, nicknameMax)
	}

	updates := map[string]any{"nickname": nickname}
	if strings.TrimSpace(req.Avatar) != "" {
		updates["avatar"] = strings.TrimSpace(req.Avatar)
	}

	if err := deps.DB.Model(&User{}).Where("id = ?", u.ID).Updates(updates).Error; err != nil {
		log.Printf("update profile failed: user=%d err=%v", u.ID, err)
		resp.Fail(c, resp.CodeInternal, "保存失败")
		return
	}

	resp.OK(c, ProfileResp{Nickname: nickname, Avatar: avatarOf(updates, u)})
}

// UploadAvatar POST /api/v1/user/avatar
// 小程序用 wx.uploadFile 发 multipart，字段名为 file。
// 返回站内相对路径，前端拼上后端基地址即可显示
func UploadAvatar(c *gin.Context) {
	userID := c.GetInt64("user_id")

	file, err := c.FormFile("file")
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "缺少头像文件")
		return
	}
	if file.Size > avatarMaxBytes {
		resp.Fail(c, resp.CodeBadRequest, "头像不能超过 2MB")
		return
	}

	// 只保留认识的扩展名，其余一律按 png 存，避免把可疑后缀写进磁盘
	ext := strings.ToLower(filepath.Ext(file.Filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
	default:
		ext = ".png"
	}

	name := fmt.Sprintf("avatar_%d_%d%s", userID, time.Now().UnixNano(), ext)
	if err := c.SaveUploadedFile(file, upload.AvatarPath(name)); err != nil {
		log.Printf("save avatar failed: user=%d err=%v", userID, err)
		resp.Fail(c, resp.CodeInternal, "头像保存失败")
		return
	}

	rel := "/uploads/avatars/" + name
	if err := deps.DB.Model(&User{}).Where("id = ?", userID).Update("avatar", rel).Error; err != nil {
		log.Printf("update avatar failed: user=%d err=%v", userID, err)
		resp.Fail(c, resp.CodeInternal, "头像保存失败")
		return
	}

	resp.OK(c, gin.H{"avatar_url": rel})
}

// current 取当前登录用户。取不到（未登录 / 用户不存在）时自行写回响应
func current(c *gin.Context) (*User, bool) {
	userID := c.GetInt64("user_id")
	if userID <= 0 {
		resp.Fail(c, resp.CodeUnauthorized, "未登录或登录已失效")
		return nil, false
	}

	var u User
	if err := deps.DB.Where("id = ?", userID).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			resp.Fail(c, resp.CodeNotFound, "用户不存在，请重新登录")
			return nil, false
		}
		log.Printf("load user failed: user=%d err=%v", userID, err)
		resp.Fail(c, resp.CodeInternal, "查询失败")
		return nil, false
	}
	return &u, true
}

func avatarOf(updates map[string]any, u *User) string {
	if v, ok := updates["avatar"].(string); ok && v != "" {
		return v
	}
	return u.Avatar
}

// trimRunes 按字符（而非字节）截断
func trimRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	out := make([]rune, 0, n)
	// range 的 i 是字节下标，不能直接当字符序号用，得自己数
	count := 0
	for _, r := range s {
		if count >= n {
			break
		}
		out = append(out, r)
		count++
	}
	return string(out)
}
