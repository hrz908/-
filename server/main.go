package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"stock-agent-server/internal/auth"
	"stock-agent-server/internal/config"
	"stock-agent-server/internal/db"
	"stock-agent-server/internal/deps"
	"stock-agent-server/internal/module/alert"
	"stock-agent-server/internal/module/user"
	"stock-agent-server/internal/module/watchlist"
	"stock-agent-server/internal/quote"
	"stock-agent-server/internal/router"
	"stock-agent-server/internal/upload"
	"stock-agent-server/internal/wechat"
)

func main() {
	cfg := config.Load()

	// 退出信号：收到后走优雅关闭
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pg, err := db.NewPostgres(cfg.DSN)
	if err != nil {
		log.Fatalf("init postgres: %v", err)
	}
	// 各模块的模型在这里汇总：新增模块时把它导出的 Models 加进这个切片
	models := append([]any{}, watchlist.Models...)
	models = append(models, alert.Models...)
	models = append(models, user.Models...)
	if err := db.AutoMigrate(pg, models...); err != nil {
		log.Fatalf("auto migrate: %v", err)
	}

	rdb := db.NewRedis(ctx, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	deps.Init(pg, rdb)

	auth.Init(cfg.JWTSecret)
	quote.QuoteAPIBase = cfg.QuoteAPI
	// 小程序凭据：两个都有值时登录走微信 code2session，否则退化为演示账号
	wechat.AppID = cfg.WechatAppID
	wechat.Secret = cfg.WechatSecret
	if err := upload.Init(cfg.UploadDir); err != nil {
		log.Fatalf("init upload dir: %v", err)
	}

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: router.New(),
	}

	go func() {
		log.Printf("server listening on %s", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
