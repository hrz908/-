package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"stock-agent-server/internal/agent"
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

	pg, err := db.NewPostgres(cfg.DSN)
	if err != nil {
		log.Fatalf("init postgres: %v", err)
	}
	// 各模块的模型在这里汇总：新增模块时把它导出的 Models 加进这个切片
	models := slices.Concat(watchlist.Models, alert.Models, model.Models)
	if err := db.AutoMigrate(pg, models...); err != nil {
		log.Fatalf("auto migrate: %v", err)
	}

	rdb := db.NewRedis(ctx, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	deps.Init(pg, rdb)

	auth.Init(cfg.JWTSecret)
	quote.SetQuoteAPI(cfg.QuoteAPI)
	// 小程序凭据：两个都有值时登录走微信 code2session，否则退化为演示账号
	wechat.AppID = cfg.WechatAppID
	wechat.Secret = cfg.WechatSecret
	// 企业微信自建应用凭据：三项齐全时，命中的提醒会额外推送给企业成员（@all），
	// 与小程序内的 WebSocket 提醒并存；未配置则只走 WebSocket
	wecom.CorpID = cfg.WeComCorpID
	wecom.Secret = cfg.WeComSecret
	wecom.AgentID = cfg.WeComAgentID
	if err := upload.Init(cfg.UploadDir); err != nil {
		log.Fatalf("init upload dir: %v", err)
	}

	// Agent 网关：注入 Redis（三道限流闸门）与 Python Agent 服务地址。
	// 必须在 router.New 之前完成，否则路由 handler 取到的 limiter/proxy 为 nil
	agent.Init(rdb, cfg.AgentBaseURL)

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: router.New(cfg.AllowOrigins),
		// 不设超时的话，慢速发送请求头的连接可以长期占住资源（Slowloris）
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("server listening on %s (env=%s)", cfg.Addr, cfg.Env)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
