package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gulmix/apigateway/internal/config"
	"github.com/gulmix/apigateway/internal/loadbalancer/algorithms"
	"github.com/gulmix/apigateway/internal/middleware/observability"
	"github.com/gulmix/apigateway/internal/middleware/ratelimiter"
	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
	"github.com/gulmix/apigateway/internal/proxy"
	"github.com/gulmix/apigateway/internal/server"
	"github.com/gulmix/apigateway/pkg/redis"
	"go.uber.org/zap"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	logger, _ := zap.NewProduction()
	defer logger.Sync()

	rdb := redis.NewClient(*cfg)
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		logger.Fatal("redis: ping failed", zap.Error(err))
	}
	rlStore := store.New(rdb)

	lb := algorithms.NewRoundRobin(cfg.Backends)
	handler := proxy.NewHandler(lb)

	r := gin.New()
	r.Use(observability.Logger(logger))
	r.Use(ratelimiter.RateLimit(rlStore, cfg.Routes))
	r.Any("/*path", handler.ServeHTTP)

	srv := server.New(cfg.Server.Host+":"+cfg.Server.Port, r)

	go func() {
		if err := srv.Run(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("listen", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	if err := srv.Shutdown(5 * time.Second); err != nil {
		logger.Error("shutdown", zap.Error(err))
	}
}
