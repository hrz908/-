package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"slices"
	"syscall"

	"stock-agent-server/internal/agent"
	"stock-agent-server/internal/asr"
	"stock-agent-server/internal/auth"
	"stock-agent-server/internal/config"
	"stock-agent-server/internal/db"
	"stock-agent-server/internal/deps"
	"stock-agent-server/internal/model"
	"stock-agent-server/internal/module/alert"
	"stock-agent-server/internal/module/watchlist"
	"stock-agent-server/internal/quote"
	"stock-agent-server/internal/router"
	"stock-agent-server/internal/upload"
	"stock-agent-server/internal/wechat"
	"stock-agent-server/internal/wecom"
	"stock-agent-server/internal/ws"
)

func main() {
	cfg := config.Load()

	// 退出信号：收到后走优雅关闭
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 生产环境的安全底线：密钥还是默认值等于人人可伪造 token；
	// 微信凭据缺失会让所有用户共用一个演示账号，数据全部串号。
	// dev 下两者都允许（本地联调方便），prod 一律拒绝启动
	if cfg.Env == "prod" {
		if cfg.JWTSecret == "" || cfg.JWTSecret == "please-change-me" {
			log.Fatal("JWT_SECRET 未设置或仍是默认值，禁止以 prod 启动")
		}
		if cfg.WechatAppID == "" || cfg.WechatSecret == "" {
			log.Fatal("WECHAT_APPID / WECHAT_SECRET 未配置，禁止以 prod 启动（否则所有用户共用一个演示账号）")
		}
	}

	pg, err := db.NewPostgres(cfg.DSN, db.PoolParams{
		MaxOpenConns:    cfg.DB.MaxOpenConns,
		MaxIdleConns:    cfg.DB.MaxIdleConns,
		ConnMaxLifetime: cfg.DB.ConnMaxLifetime,
		ConnMaxIdleTime: cfg.DB.ConnMaxIdleTime,
	})
	if err != nil {
		log.Fatalf("init postgres: %v", err)
	}
	// 各模块的模型在这里汇总：新增模块时把它导出的 Models 加进这个切片
	models := slices.Concat(watchlist.Models, alert.Models, model.Models)
	if err := db.AutoMigrate(pg, models...); err != nil {
		log.Fatalf("auto migrate: %v", err)
	}

	rdb := db.NewRedis(ctx, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB, cfg.Redis.PoolSize)
	deps.Init(pg, rdb)

	auth.Init(cfg.JWTSecret, cfg.JWTTTL)
	// 行情源与批量参数：超时 / 并发 / 缓存 TTL 统一由配置注入
	quote.Init(cfg.QuoteAPI,
		cfg.HTTP.QuoteTimeout, cfg.HTTP.SearchTimeout, cfg.HTTP.QuoteCacheTTL,
		cfg.HTTP.QuoteFetchLimit, cfg.HTTP.QuoteBatchSize, cfg.HTTP.QuoteMgetChunk)
	// 小程序凭据：两个都有值时登录走微信 code2session，否则退化为演示账号
	wechat.Init(cfg.WechatAppID, cfg.WechatSecret, cfg.HTTP.WechatTimeout)
	// 企业微信自建应用凭据：三项齐全时，命中的提醒会额外推送给企业成员（@all），
	// 与小程序内的 WebSocket 提醒并存；未配置则只走 WebSocket
	wecom.Init(cfg.WeComCorpID, cfg.WeComSecret, cfg.WeComAgentID, cfg.HTTP.WecomTimeout)
	// 行情提醒 WS hub 连接上限（每用户 / 全局）
	ws.Configure(cfg.Hub.MaxPerUser, cfg.Hub.MaxTotal)
	if err := upload.Init(cfg.UploadDir); err != nil {
		log.Fatalf("init upload dir: %v", err)
	}

	// Agent 网关：注入 Redis（三道限流闸门）与 Python Agent 服务地址及运行参数。
	// 必须在 router.New 之前完成，否则路由 handler 取到的 limiter/proxy 为 nil
	agent.Init(rdb, agent.Options{
		BaseURL:          cfg.AgentBaseURL,
		QPS:              cfg.Agent.QPS,
		Concurrent:       cfg.Agent.Concurrent,
		DailyQuota:       cfg.Agent.DailyQuota,
		ProxyConcurrency: cfg.Agent.ProxyConcurrency,
		WSMaxConns:       cfg.Agent.WSMaxConns,
		RunTimeout:       cfg.Agent.RunTimeout,
		TTFTTimeout:      cfg.Agent.TTFTTimeout,
		MaxLineBytes:     cfg.Agent.MaxLineMB << 20,
	})

	// 语音转文字：同样要在 router.New 之前注入，否则 handler 读到空配置
	asr.Init(cfg.ASRBaseURL, cfg.ASRAPIKey, cfg.ASRModel, cfg.HTTP.ASRTimeout)
	// 实时语音识别：腾讯云 WebSocket 凭据与连接闸门（未配置时该路由返回 503）
	asr.InitStream(cfg.TencentASR.AppID, cfg.TencentASR.SecretID, cfg.TencentASR.SecretKey,
		cfg.TencentASR.Engine, cfg.TencentASR.MaxConns)

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: router.New(cfg),
		// 不设超时的话，慢速发送请求头的连接可以长期占住资源（Slowloris）
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
	}

	// ListenAndServe 的错误经通道回传：goroutine 里 log.Fatalf 会跳过 defer、
	// 绕过优雅关闭路径（连接池 / Redis 客户端都不会收尾）
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("server listening on %s (env=%s)", cfg.Addr, cfg.Env)
		serverErr <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	// 长连接先关：srv.Shutdown 不会等待被 hijack 的 WebSocket，
	// 不主动关会一直等到上面 5 秒超时才被硬断
	ws.CloseAll()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	// 连接池与 Redis 客户端收尾，避免退出时悬挂连接
	if sqlDB, err := pg.DB(); err == nil {
		_ = sqlDB.Close()
	}
	if rdb != nil {
		_ = rdb.Close()
	}
	log.Printf("server stopped")
}
