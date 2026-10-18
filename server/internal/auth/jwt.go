package auth

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// token 有效期
const tokenTTL = 7 * 24 * time.Hour

var jwtSecret = []byte("please-change-me")

// Init 注入密钥，main 启动时调用一次
func Init(secret string) {
	if secret != "" {
		jwtSecret = []byte(secret)
	}
}

// ParseToken 解析并校验 JWT，返回 user_id。
// 契约与提示词一致：parseToken(tokenStr) (int64, error)
func ParseToken(tokenStr string) (int64, error) {
	if tokenStr == "" {
		return 0, errors.New("empty token")
	}
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return 0, errors.New("invalid token")
	}
	switch v := claims["user_id"].(type) {
	case float64: // JSON number 解码后是 float64
		return int64(v), nil
	case string:
		return strconv.ParseInt(v, 10, 64)
	default:
		return 0, errors.New("missing user_id")
	}
}

// SignToken 签发 JWT（登录接口用）
func SignToken(userID int64) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"user_id": userID,
		"iat":     now.Unix(),
		"exp":     now.Add(tokenTTL).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)
}
