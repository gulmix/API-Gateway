//go:build integration

// Run with: go test -tags=integration ./tests/integration/...
package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gulmix/apigateway/internal/config"
	"github.com/gulmix/apigateway/internal/loadbalancer"
	"github.com/gulmix/apigateway/internal/loadbalancer/algorithms"
	"github.com/gulmix/apigateway/internal/loadbalancer/healthcheck"
	"github.com/gulmix/apigateway/internal/middleware/auth"
	"github.com/gulmix/apigateway/internal/middleware/cache"
	"github.com/gulmix/apigateway/internal/middleware/ratelimiter"
	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
	"github.com/gulmix/apigateway/internal/proxy"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"go.uber.org/zap"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

func startRedis(t *testing.T) *goredis.Client {
	t.Helper()
	ctx := context.Background()

	ctr, err := tcredis.Run(ctx, "redis:7-alpine")
	require.NoError(t, err, "start Redis container")
	t.Cleanup(func() { ctr.Terminate(ctx) })

	addr, err := ctr.ConnectionString(ctx)
	require.NoError(t, err)
	addr = strings.TrimPrefix(addr, "redis://")

	rdb := goredis.NewClient(&goredis.Options{Addr: addr})
	require.NoError(t, rdb.Ping(ctx).Err(), "ping Redis")
	t.Cleanup(func() { rdb.Close() })
	return rdb
}

func buildRouter(t *testing.T, rdb *goredis.Client, backendURL string, routes []config.RouteConfig) *gin.Engine {
	t.Helper()

	cacheCfg := config.CacheConfig{
		L1: struct {
			MaxItems   int           `mapstructure:"max_items"`
			DefaultTTL time.Duration `mapstructure:"default_ttl"`
		}{MaxItems: 1000, DefaultTTL: 30 * time.Second},
		L2: struct {
			DefaultTTL time.Duration `mapstructure:"default_ttl"`
		}{DefaultTTL: 60 * time.Second},
		InvalidationChannel: "cache:invalidate",
	}
	mgr, err := cache.NewManager(rdb, cacheCfg)
	require.NoError(t, err)

	backendAddr := strings.TrimPrefix(backendURL, "http://")
	reg := loadbalancer.NewRegistry()
	registered := map[string]bool{}
	for _, r := range routes {
		if r.Upstream == "" || registered[r.Upstream] {
			continue
		}
		pool := loadbalancer.NewPool(r.Upstream, algorithms.NewRoundRobin(),
			healthcheck.NewPassiveBreaker(config.CircuitBreakerConfig{}, zap.NewNop()))
		pool.Add(loadbalancer.NewBackend(backendAddr, 1))
		reg.Register(r.Upstream, pool)
		registered[r.Upstream] = true
	}

	authCfg := config.AuthConfig{
		JWT:     config.JWTConfig{JWKSCacheTTL: 5 * time.Minute},
		APIKeys: config.APIKeyConfig{Header: "X-API-Key"},
	}

	r := gin.New()
	r.Use(auth.Middleware(rdb, authCfg, routes))
	r.Use(ratelimiter.RateLimit(store.New(rdb), routes))
	r.Use(mgr.Middleware(routes))
	r.Use(loadbalancer.Middleware(reg, routes))
	r.Any("/*path", proxy.NewHandler().ServeHTTP)
	return r
}

func TestFullRequestLifecycle(t *testing.T) {
	rdb := startRedis(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer backend.Close()

	routes := []config.RouteConfig{{
		Path:     "/api/v1/test",
		Upstream: "test-svc",
		Auth:     "none",
	}}
	r := buildRouter(t, rdb, backend.URL, routes)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
}

func TestL2CacheBehavior(t *testing.T) {
	rdb := startRedis(t)

	var hits atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"cached":true}`))
	}))
	defer backend.Close()

	routes := []config.RouteConfig{{
		Path:     "/api/v1/cached",
		Upstream: "cache-svc",
		Auth:     "none",
		Cache:    config.RouteCacheConfig{Enabled: true, TTL: 30 * time.Second},
	}}
	r := buildRouter(t, rdb, backend.URL, routes)

	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/cached", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusOK, w1.Code)
	assert.Equal(t, int32(1), hits.Load(), "backend should be called on cache miss")
	assert.NotEqual(t, "HIT", w1.Header().Get("X-Cache"))

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/cached", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, int32(1), hits.Load(), "backend must not be called on cache hit")
	assert.Equal(t, "HIT", w2.Header().Get("X-Cache"), "X-Cache header must be HIT on second request")
}

func TestRateLimitEnforcement(t *testing.T) {
	rdb := startRedis(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	const limit = 3
	routes := []config.RouteConfig{{
		Path:     "/api/v1/limited",
		Upstream: "limited-svc",
		Auth:     "none",
		RateLimit: config.RateLimitConfig{
			Enabled:   true,
			Algorithm: "fixed_window",
			Scope:     []string{"ip"},
			IP:        config.ScopeLimit{Requests: limit, Window: 60 * time.Second},
		},
	}}
	r := buildRouter(t, rdb, backend.URL, routes)

	for i := 0; i < limit; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/limited", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d should be allowed", i+1)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/limited", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "request beyond limit must return 429")
	assert.Contains(t, w.Body.String(), "rate limit exceeded")
}

func TestAPIKeyAuthentication(t *testing.T) {
	rdb := startRedis(t)
	ctx := context.Background()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	routes := []config.RouteConfig{{
		Path:     "/api/v1/secure",
		Upstream: "secure-svc",
	}}
	r := buildRouter(t, rdb, backend.URL, routes)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/secure", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code, "missing credentials should return 401")

	type payload struct {
		Owner  string   `json:"owner"`
		Scopes []string `json:"scopes"`
		Tier   string   `json:"tier"`
	}
	data, _ := json.Marshal(payload{Owner: "test-owner", Scopes: []string{"read"}, Tier: "free"})
	require.NoError(t, rdb.Set(ctx, "apiKey:integration-test-key", data, 0).Err())

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/secure", nil)
	req2.Header.Set("X-API-Key", "integration-test-key")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code, "valid API key should grant access")
}

func TestLBDistribution(t *testing.T) {
	rdb := startRedis(t)

	var hits1, hits2 atomic.Int32
	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits1.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend1.Close()
	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits2.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend2.Close()

	routes := []config.RouteConfig{{
		Path:     "/api/v1/lb",
		Upstream: "lb-svc",
		Auth:     "none",
	}}

	cacheCfg := config.CacheConfig{
		L1: struct {
			MaxItems   int           `mapstructure:"max_items"`
			DefaultTTL time.Duration `mapstructure:"default_ttl"`
		}{MaxItems: 1000, DefaultTTL: 30 * time.Second},
		L2: struct {
			DefaultTTL time.Duration `mapstructure:"default_ttl"`
		}{DefaultTTL: 60 * time.Second},
	}
	mgr, err := cache.NewManager(rdb, cacheCfg)
	require.NoError(t, err)

	pool := loadbalancer.NewPool("lb-svc", algorithms.NewRoundRobin(),
		healthcheck.NewPassiveBreaker(config.CircuitBreakerConfig{}, zap.NewNop()))
	pool.Add(loadbalancer.NewBackend(strings.TrimPrefix(backend1.URL, "http://"), 1))
	pool.Add(loadbalancer.NewBackend(strings.TrimPrefix(backend2.URL, "http://"), 1))
	reg := loadbalancer.NewRegistry()
	reg.Register("lb-svc", pool)

	authCfg := config.AuthConfig{
		JWT:     config.JWTConfig{JWKSCacheTTL: 5 * time.Minute},
		APIKeys: config.APIKeyConfig{Header: "X-API-Key"},
	}

	r := gin.New()
	r.Use(auth.Middleware(rdb, authCfg, routes))
	r.Use(ratelimiter.RateLimit(store.New(rdb), routes))
	r.Use(mgr.Middleware(routes))
	r.Use(loadbalancer.Middleware(reg, routes))
	r.Any("/*path", proxy.NewHandler().ServeHTTP)

	const total = 10
	for i := 0; i < total; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/lb", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	}

	assert.Equal(t, int32(total/2), hits1.Load(), "round-robin should split evenly")
	assert.Equal(t, int32(total/2), hits2.Load(), "round-robin should split evenly")
}
