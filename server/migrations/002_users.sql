-- 用户资料：一个微信用户一行，openid 唯一。
-- 微信不提供微信号（wx.login 只返回 openid），故不存 wechat_id
CREATE TABLE IF NOT EXISTS users (
    id         BIGSERIAL PRIMARY KEY,
    openid     VARCHAR(64) NOT NULL,
    nickname   VARCHAR(64),
    avatar     VARCHAR(512),              -- 站内相对路径，如 /uploads/avatars/xxx.png
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_users_openid ON users(openid);
