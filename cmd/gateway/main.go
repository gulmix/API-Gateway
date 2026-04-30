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
	"github.com/gulmix/apigateway/internal/discovery"
	"github.com/gulmix/apigateway/internal/loadbalancer"
	"github.com/gulmix/apigateway/internal/loadbalancer/algorithms"
	"github.com/gulmix/apigateway/internal/loadbalancer/healthcheck"
	"github.com/gulmix/apigateway/internal/middleware/auth"
	"github.com/gulmix/apigateway/internal/middleware/cache"
	"github.com/gulmix/apigateway/internal/middleware/observability"
	"github.com/gulmix/apigateway/internal/middleware/ratelimiter"
	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
	"github.com/gulmix/apigateway/internal/proxy"
	"github.com/gulmix/apigateway/internal/server"
	"github.com/gulmix/apigateway/pkg/redis"
	"go.uber.org/zap"
)

func newAlgorithm(name string) loadbalancer.Balancer {
	switch name {
	case "least_connections":
		return algorithms.NewLeastConnections()
	case "weighted_rr":
		return algorithms.NewWeightedRoundRobin()
	case "consistent_hash":
		return algorithms.NewConsistentHash()
	default:
		return algorithms.NewRoundRobin()
	}
}

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	logger, err := observability.NewLogger(cfg.Observability.LogLevel)
	if err != nil {
		log.Fatalf("logger: %v", err)
	}
	defer logger.Sync()

	if cfg.Observability.Tracing.Enabled {
		shutdown, err := observability.InitTracer(
			cfg.Observability.Tracing.OTLPEndpoint,
			cfg.Observability.Tracing.SamplingRate,
		)
		if err != nil {
			logger.Fatal("tracer init failed", zap.Error(err))
		}
		defer shutdown(context.Background())
	}

	rdb := redis.NewClient(*cfg)
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		logger.Fatal("redis: ping failed", zap.Error(err))
	}
	rlStore := store.New(rdb)

	cacheManager, err := cache.NewManager(rdb, cfg.Cache)
	if err != nil {
		logger.Fatal("cache: init failed", zap.Error(err))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cacheManager.StartInvalidationSubscriber(ctx)

	reg := loadbalancer.NewRegistry()
	for name, upCfg := range cfg.Upstreams {
		alg := newAlgorithm(upCfg.Algorithm)
		breaker := healthcheck.NewPassiveBreaker(upCfg.CircuitBreaker, logger)
		pool := loadbalancer.NewPool(name, alg, breaker)
		reg.Register(name, pool)

		for _, bCfg := range upCfg.Backends {
			weight := bCfg.Weight
			if weight == 0 {
				weight = 1
			}
			pool.Add(loadbalancer.NewBackend(bCfg.Addr, weight))
		}

		checker := healthcheck.NewActiveChecker(pool, name, upCfg.HealthCheck, logger)
		checker.Start()
		defer checker.Stop()
	}

	if cfg.Discovery.Enabled {
		kubeClient, err := discovery.NewKubeClient(cfg.Discovery.Kubeconfig)
		if err != nil {
			logger.Fatal("discovery: failed to build kube client", zap.Error(err))
		}
		ctrl := discovery.NewController(kubeClient, reg, cfg.Discovery, logger)
		go ctrl.Run(ctx)
	}

	handler := proxy.NewHandler()

	r := gin.New()
	r.Use(observability.TracingMiddleware())
	r.Use(observability.MetricsMiddleware())
	r.Use(observability.Logger(logger))
	r.Use(auth.Middleware(rdb, cfg.Auth, cfg.Routes))
	r.Use(ratelimiter.RateLimit(rlStore, cfg.Routes))
	r.Use(cacheManager.Middleware(cfg.Routes))
	r.Use(loadbalancer.Middleware(reg, cfg.Routes))
	r.Any("/*path", handler.ServeHTTP)

	// Admin server on a separate port — not reachable from outside the cluster
	adminRouter := gin.New()
	adminRouter.Use(observability.Logger(logger))

	adminRouter.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	adminRouter.GET("/ready", func(c *gin.Context) {
		if err := rdb.Ping(c.Request.Context()).Err(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})
	adminRouter.GET("/metrics", observability.MetricsHandler())

	admin := adminRouter.Group("/admin")
	admin.DELETE("/cache", cacheManager.PurgeHandler())
	admin.GET("/cache/stats", cacheManager.StatsHandler())
	admin.POST("/api-keys", auth.CreateAPIKeyHandler(rdb))
	admin.DELETE("/api-keys/:key", auth.DeleteAPIKeyHandler(rdb))
	admin.GET("/api-keys/:key", auth.GetAPIKeyHandler(rdb))
	admin.PUT("/log-level", observability.LogLevelHandler())
	admin.GET("/log-level", func(c *gin.Context) {
		c.JSON(200, gin.H{"level": observability.GetLogLevel()})
	})

	srv := server.New(cfg.Server.Host+":"+cfg.Server.Port, r)
	adminSrv := server.New(cfg.Server.Host+":"+cfg.Server.AdminPort, adminRouter)

	go func() {
		if err := srv.Run(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("listen", zap.Error(err))
		}
	}()
	go func() {
		if err := adminSrv.Run(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("admin listen", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	if err := srv.Shutdown(5 * time.Second); err != nil {
		logger.Error("shutdown", zap.Error(err))
	}
	if err := adminSrv.Shutdown(5 * time.Second); err != nil {
		logger.Error("admin shutdown", zap.Error(err))
	}
}
