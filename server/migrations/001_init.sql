-- 自选股：一个用户对同一只股票只能有一条记录
CREATE TABLE IF NOT EXISTS user_watchlist (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL,
    stock_code  VARCHAR(20) NOT NULL,          -- 格式如 SH600519
    stock_name  VARCHAR(100),
    cost_price  NUMERIC(18,4) DEFAULT 0,       -- 成本价（元）
    quantity    INT DEFAULT 0,                 -- 持股数量（股）
    enabled     BOOLEAN DEFAULT TRUE,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(user_id, stock_code)
);

-- 提醒规则：同一用户同一股票可设两个方向（>= / <=）
CREATE TABLE IF NOT EXISTS alert_rule (
    id                BIGSERIAL PRIMARY KEY,
    user_id           BIGINT NOT NULL,
    stock_code        VARCHAR(20) NOT NULL,
    condition_type    VARCHAR(20) NOT NULL,    -- price_gte / price_lte
    condition_value   NUMERIC(18,4) NOT NULL,  -- 目标价
    enabled           BOOLEAN DEFAULT TRUE,
    created_at        TIMESTAMPTZ DEFAULT NOW(),
    last_triggered_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_alert_user ON alert_rule(user_id);
CREATE INDEX IF NOT EXISTS idx_watchlist_user ON user_watchlist(user_id);
