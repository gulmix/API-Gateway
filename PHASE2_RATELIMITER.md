# Phase 2 — Rate Limiter

Только те файлы, которые ещё нужно написать руками.

## Уже готово (не трогать)

| Файл | Что там |
|------|---------|
| `pkg/redis/client.go` | `NewClient(cfg config.Config)` → `*redis.Client` |
| `internal/config/config.go` | `Config`, `RedisConfig`, `RateLimitConfig`, `ScopeLimit`, `LoadConfig()` |
| `config/gateway.yaml` | server + backends + redis |
| `config/routes.yaml` | маршруты с rate_limit конфигом |
| `internal/server/server.go` | `New()`, `Run()`, `Shutdown()` |
| `internal/proxy/reverse_proxy.go` | `httputil.ReverseProxy` обёртка |
| `internal/loadbalancer/balancer.go` | `Balancer` интерфейс + `NewRoundRobin` |
| `internal/loadbalancer/algorithms/round_robin.go` | атомарный round-robin |
| `internal/middleware/observability/logging.go` | Zap request logger |

---

## Зависимости (если ещё не добавил)

```bash
go get github.com/redis/go-redis/v9
go get github.com/alicebob/miniredis/v2
go get github.com/prometheus/client_golang/prometheus
go get github.com/stretchr/testify
```

---

## Файл: internal/middleware/ratelimiter/store/redis_store.go

Это ядро Phase 2. Все Lua-скрипты здесь.
Lua-скрипт в Redis — атомарная операция: никакой WATCH/MULTI не нужен.

```go
package store

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Result — результат одной проверки лимита.
type Result struct {
	Allowed    bool
	Remaining  int64
	RetryAfter time.Duration
}

type Store struct {
	rdb *redis.Client

	fixedWindow   *redis.Script
	tokenBucket   *redis.Script
	slidingWindow *redis.Script
	leakyBucket   *redis.Script
}

func New(rdb *redis.Client) *Store {
	return &Store{
		rdb:           rdb,
		fixedWindow:   redis.NewScript(luaFixedWindow),
		tokenBucket:   redis.NewScript(luaTokenBucket),
		slidingWindow: redis.NewScript(luaSlidingWindow),
		leakyBucket:   redis.NewScript(luaLeakyBucket),
	}
}

// ─── Fixed Window ────────────────────────────────────────────────────────────
//
// KEYS[1] = ключ счётчика
// ARGV[1] = лимит
// ARGV[2] = размер окна в секундах
//
// INCR атомарен сам по себе. EXPIRE ставим только при создании (current == 1),
// чтобы не сбрасывать таймер каждым запросом.

const luaFixedWindow = `
local key    = KEYS[1]
local limit  = tonumber(ARGV[1])
local window = tonumber(ARGV[2])

local current = redis.call("INCR", key)
if current == 1 then
    redis.call("EXPIRE", key, window)
end

if current > limit then
    return {current, 0}
end
return {current, 1}
`

func (s *Store) AllowFixedWindow(ctx context.Context, key string, limit int, window time.Duration) (Result, error) {
	windowSec := int64(window.Seconds())

	vals, err := s.fixedWindow.Run(ctx, s.rdb, []string{key}, limit, windowSec).Int64Slice()
	if err != nil {
		return Result{}, err
	}

	current := vals[0]
	allowed := vals[1] == 1
	remaining := int64(limit) - current
	if remaining < 0 {
		remaining = 0
	}

	var retryAfter time.Duration
	if !allowed {
		if ttl, err := s.rdb.TTL(ctx, key).Result(); err == nil && ttl > 0 {
			retryAfter = ttl
		}
	}

	return Result{Allowed: allowed, Remaining: remaining, RetryAfter: retryAfter}, nil
}

// ─── Token Bucket ─────────────────────────────────────────────────────────────
//
// KEYS[1] = ключ хэша {tokens, last_refill}
// ARGV[1] = capacity (максимум токенов = burst size)
// ARGV[2] = refill_rate (токенов в секунду)
// ARGV[3] = текущее время (unix, секунды с дробью)
//
// Принцип: читаем состояние → добавляем токены за прошедшее время →
// пытаемся взять 1 токен → записываем обратно. Всё в одном скрипте = атомарно.

const luaTokenBucket = `
local key          = KEYS[1]
local capacity     = tonumber(ARGV[1])
local refill_rate  = tonumber(ARGV[2])
local now          = tonumber(ARGV[3])

local data        = redis.call("HMGET", key, "tokens", "last_refill")
local tokens      = tonumber(data[1])
local last_refill = tonumber(data[2])

if tokens == nil then
    tokens      = capacity
    last_refill = now
end

local elapsed  = now - last_refill
local refilled = elapsed * refill_rate
tokens = math.min(capacity, tokens + refilled)

local allowed = 0
if tokens >= 1 then
    tokens  = tokens - 1
    allowed = 1
end

redis.call("HMSET", key, "tokens", tokens, "last_refill", now)
-- TTL чуть больше времени, нужного для полного заполнения бакета
redis.call("EXPIRE", key, math.ceil(capacity / refill_rate) + 10)

return {math.floor(tokens), allowed}
`

func (s *Store) AllowTokenBucket(ctx context.Context, key string, capacity int, refillRate float64) (Result, error) {
	// Секунды с наносекундной точностью — нужно для корректного elapsed в Lua
	now := float64(time.Now().UnixNano()) / 1e9

	vals, err := s.tokenBucket.Run(ctx, s.rdb, []string{key}, capacity, refillRate, now).Int64Slice()
	if err != nil {
		return Result{}, err
	}

	remaining := vals[0]
	allowed := vals[1] == 1

	var retryAfter time.Duration
	if !allowed && refillRate > 0 {
		retryAfter = time.Duration(float64(time.Second) / refillRate)
	}

	return Result{Allowed: allowed, Remaining: remaining, RetryAfter: retryAfter}, nil
}

// ─── Sliding Window ───────────────────────────────────────────────────────────
//
// KEYS[1] = ключ sorted set
// ARGV[1] = лимит
// ARGV[2] = размер окна в секундах
// ARGV[3] = текущее время в миллисекундах
//
// Member = "now_ms:random" — нужно чтобы избежать коллизий
// при двух запросах в одну миллисекунду.

const luaSlidingWindow = `
local key       = KEYS[1]
local limit     = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2]) * 1000
local now_ms    = tonumber(ARGV[3])
local oldest    = now_ms - window_ms

redis.call("ZREMRANGEBYSCORE", key, "-inf", oldest)
local current = redis.call("ZCARD", key)

if current >= limit then
    return {current, 0}
end

local member = now_ms .. ":" .. math.random(1, 1000000)
redis.call("ZADD", key, now_ms, member)
redis.call("PEXPIRE", key, window_ms + 1000)

return {current + 1, 1}
`

func (s *Store) AllowSlidingWindow(ctx context.Context, key string, limit int, window time.Duration) (Result, error) {
	nowMs := time.Now().UnixMilli()
	windowSec := int64(window.Seconds())

	vals, err := s.slidingWindow.Run(ctx, s.rdb, []string{key}, limit, windowSec, nowMs).Int64Slice()
	if err != nil {
		return Result{}, err
	}

	current := vals[0]
	allowed := vals[1] == 1
	remaining := int64(limit) - current
	if remaining < 0 {
		remaining = 0
	}

	var retryAfter time.Duration
	if !allowed {
		retryAfter = time.Second // минимальная оценка
	}

	return Result{Allowed: allowed, Remaining: remaining, RetryAfter: retryAfter}, nil
}

// ─── Leaky Bucket ─────────────────────────────────────────────────────────────
//
// KEYS[1] = ключ хэша {queue, last_drip}
// ARGV[1] = rate (запросов в секунду которые "вытекают")
// ARGV[2] = capacity (максимальный размер очереди)
// ARGV[3] = текущее время (unix, секунды с дробью)
//
// Вычисляем сколько запросов вытекло за прошедшее время,
// уменьшаем очередь, пытаемся добавить новый запрос.

const luaLeakyBucket = `
local key      = KEYS[1]
local rate     = tonumber(ARGV[1])
local capacity = tonumber(ARGV[2])
local now      = tonumber(ARGV[3])

local data      = redis.call("HMGET", key, "queue", "last_drip")
local queue     = tonumber(data[1])
local last_drip = tonumber(data[2])

if queue == nil then
    queue     = 0
    last_drip = now
end

local elapsed = now - last_drip
local leaked  = math.floor(elapsed * rate)
queue     = math.max(0, queue - leaked)
last_drip = now

local allowed = 0
if queue < capacity then
    queue   = queue + 1
    allowed = 1
end

redis.call("HMSET", key, "queue", queue, "last_drip", last_drip)
redis.call("EXPIRE", key, math.ceil(capacity / rate) + 10)

return {queue, allowed}
`

func (s *Store) AllowLeakyBucket(ctx context.Context, key string, rate float64, capacity int) (Result, error) {
	now := float64(time.Now().UnixNano()) / 1e9

	vals, err := s.leakyBucket.Run(ctx, s.rdb, []string{key}, rate, capacity, now).Int64Slice()
	if err != nil {
		return Result{}, err
	}

	queueSize := vals[0]
	allowed := vals[1] == 1
	remaining := int64(capacity) - queueSize
	if remaining < 0 {
		remaining = 0
	}

	var retryAfter time.Duration
	if !allowed && rate > 0 {
		retryAfter = time.Duration(float64(time.Second) / rate)
	}

	return Result{Allowed: allowed, Remaining: remaining, RetryAfter: retryAfter}, nil
}
```

---

## Файл: internal/middleware/ratelimiter/algorithms/algorithm.go

```go
package algorithms

import (
	"context"

	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
)

// Algorithm — единый интерфейс для всех алгоритмов.
// key — уже готовый Redis-ключ, его собирает limiter.go.
type Algorithm interface {
	Allow(ctx context.Context, key string) (store.Result, error)
}
```

---

## Файл: internal/middleware/ratelimiter/algorithms/fixed_window.go

```go
package algorithms

import (
	"context"
	"time"

	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
)

type FixedWindow struct {
	store  *store.Store
	limit  int
	window time.Duration
}

func NewFixedWindow(s *store.Store, limit int, window time.Duration) *FixedWindow {
	return &FixedWindow{store: s, limit: limit, window: window}
}

func (f *FixedWindow) Allow(ctx context.Context, key string) (store.Result, error) {
	return f.store.AllowFixedWindow(ctx, key, f.limit, f.window)
}
```

---

## Файл: internal/middleware/ratelimiter/algorithms/token_bucket.go

```go
package algorithms

import (
	"context"

	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
)

type TokenBucket struct {
	store      *store.Store
	capacity   int
	refillRate float64 // tokens per second
}

func NewTokenBucket(s *store.Store, capacity int, refillRate float64) *TokenBucket {
	return &TokenBucket{store: s, capacity: capacity, refillRate: refillRate}
}

func (t *TokenBucket) Allow(ctx context.Context, key string) (store.Result, error) {
	return t.store.AllowTokenBucket(ctx, key, t.capacity, t.refillRate)
}
```

---

## Файл: internal/middleware/ratelimiter/algorithms/sliding_window.go

```go
package algorithms

import (
	"context"
	"time"

	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
)

type SlidingWindow struct {
	store  *store.Store
	limit  int
	window time.Duration
}

func NewSlidingWindow(s *store.Store, limit int, window time.Duration) *SlidingWindow {
	return &SlidingWindow{store: s, limit: limit, window: window}
}

func (sw *SlidingWindow) Allow(ctx context.Context, key string) (store.Result, error) {
	return sw.store.AllowSlidingWindow(ctx, key, sw.limit, sw.window)
}
```

---

## Файл: internal/middleware/ratelimiter/algorithms/leaky_bucket.go

```go
package algorithms

import (
	"context"

	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
)

type LeakyBucket struct {
	store    *store.Store
	rate     float64 // requests per second that leak out
	capacity int     // max queue size
}

func NewLeakyBucket(s *store.Store, rate float64, capacity int) *LeakyBucket {
	return &LeakyBucket{store: s, rate: rate, capacity: capacity}
}

func (lb *LeakyBucket) Allow(ctx context.Context, key string) (store.Result, error) {
	return lb.store.AllowLeakyBucket(ctx, key, lb.rate, lb.capacity)
}
```

---

## Файл: internal/middleware/ratelimiter/scope.go

```go
package ratelimiter

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// ScopeKey возвращает Redis-ключ для данного scope и запроса.
// Второй возврат false = этот scope неприменим к запросу
// (например, "user" когда пользователь не аутентифицирован).
func ScopeKey(c *gin.Context, scope string) (string, bool) {
	switch scope {
	case "user":
		// auth middleware (Phase 3) кладёт user ID сюда
		user := c.GetString("auth.user")
		if user == "" {
			return "", false
		}
		return "user:" + user, true

	case "api_key":
		key := c.GetHeader("X-API-Key")
		if key == "" {
			return "", false
		}
		return "apikey:" + key, true

	case "ip":
		return "ip:" + clientIP(c), true

	default:
		return "", false
	}
}

func clientIP(c *gin.Context) string {
	// X-Forwarded-For может содержать цепочку прокси: "client, proxy1, proxy2"
	// берём первый (самый левый) — это оригинальный клиент
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	return c.ClientIP()
}
```

---

## Файл: internal/middleware/ratelimiter/metrics.go

```go
package ratelimiter

import "github.com/prometheus/client_golang/prometheus"

var (
	// Итого проверок: {route, scope, result="allowed"|"rejected"}
	hitCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "gateway",
			Subsystem: "rate_limit",
			Name:      "hits_total",
			Help:      "Total number of rate limit checks.",
		},
		[]string{"route", "scope", "result"},
	)

	// Текущий остаток токенов/запросов: {route, scope}
	remainingGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "gateway",
			Subsystem: "rate_limit",
			Name:      "remaining",
			Help:      "Remaining requests/tokens for the current window.",
		},
		[]string{"route", "scope"},
	)
)

func init() {
	prometheus.MustRegister(hitCounter, remainingGauge)
}

func recordMetrics(route, scope string, allowed bool, remaining int64) {
	result := "allowed"
	if !allowed {
		result = "rejected"
	}
	hitCounter.WithLabelValues(route, scope, result).Inc()
	remainingGauge.WithLabelValues(route, scope).Set(float64(remaining))
}
```

---

## Файл: internal/middleware/ratelimiter/limiter.go

```go
package ratelimiter

import (
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gulmix/apigateway/internal/config"
	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/algorithms"
	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
)

// routeLimiter — предсобранные алгоритмы для одного маршрута.
// Создаём один раз при старте сервера, не при каждом запросе.
type routeLimiter struct {
	path       string
	enabled    bool
	scopes     []string
	algByScope map[string]algorithms.Algorithm // scope → algorithm instance
	limByScope map[string]config.ScopeLimit    // scope → limit config (для заголовков)
}

// RateLimit возвращает Gin middleware.
// Принимает store и конфиг маршрутов; алгоритмы создаются один раз здесь.
func RateLimit(s *store.Store, routes []config.RouteConfig) gin.HandlerFunc {
	limiters := buildLimiters(s, routes)

	return func(c *gin.Context) {
		rl := findLimiter(limiters, c.Request.URL.Path)
		if rl == nil || !rl.enabled {
			c.Next()
			return
		}

		for _, scope := range rl.scopes {
			scopeKey, ok := ScopeKey(c, scope)
			if !ok {
				// scope неприменим к этому запросу — пропускаем
				continue
			}

			alg := rl.algByScope[scope]
			lim := rl.limByScope[scope]

			// Формат ключа: "rl:<scope_key>:<sanitized_path>"
			// Разделяем по маршруту чтобы /search и /users не делили счётчик для одного IP.
			redisKey := fmt.Sprintf("rl:%s:%s", scopeKey, sanitizePath(rl.path))

			result, err := alg.Allow(c.Request.Context(), redisKey)
			if err != nil {
				// Redis недоступен → fail open: пропускаем запрос
				continue
			}

			recordMetrics(rl.path, scope, result.Allowed, result.Remaining)

			c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", lim.Requests))
			c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", result.Remaining))

			if !result.Allowed {
				if result.RetryAfter > 0 {
					secs := int(math.Ceil(result.RetryAfter.Seconds()))
					c.Header("Retry-After", fmt.Sprintf("%d", secs))
				}
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
					"error": "rate limit exceeded",
					"scope": scope,
				})
				return
			}
		}

		c.Next()
	}
}

func buildLimiters(s *store.Store, routes []config.RouteConfig) []routeLimiter {
	result := make([]routeLimiter, 0, len(routes))
	for _, route := range routes {
		rl := routeLimiter{
			path:       route.Path,
			enabled:    route.RateLimit.Enabled,
			scopes:     route.RateLimit.Scope,
			algByScope: make(map[string]algorithms.Algorithm),
			limByScope: make(map[string]config.ScopeLimit),
		}
		for _, scope := range route.RateLimit.Scope {
			lim := scopeLimit(route.RateLimit, scope)
			rl.limByScope[scope] = lim
			rl.algByScope[scope] = newAlgorithm(route.RateLimit.Algorithm, s, lim)
		}
		result = append(result, rl)
	}
	return result
}

// findLimiter — prefix match, первый подходящий маршрут выигрывает.
func findLimiter(limiters []routeLimiter, path string) *routeLimiter {
	for i := range limiters {
		if strings.HasPrefix(path, limiters[i].path) {
			return &limiters[i]
		}
	}
	return nil
}

func scopeLimit(rl config.RateLimitConfig, scope string) config.ScopeLimit {
	switch scope {
	case "ip":
		return rl.IP
	case "api_key":
		return rl.APIKey
	case "user":
		return rl.User
	default:
		return config.ScopeLimit{}
	}
}

func newAlgorithm(name string, s *store.Store, lim config.ScopeLimit) algorithms.Algorithm {
	switch name {
	case "fixed_window":
		return algorithms.NewFixedWindow(s, lim.Requests, lim.Window)
	case "token_bucket":
		// capacity = Requests (burst size), refill = Requests / Window
		refillRate := float64(lim.Requests) / lim.Window.Seconds()
		return algorithms.NewTokenBucket(s, lim.Requests, refillRate)
	case "sliding_window":
		return algorithms.NewSlidingWindow(s, lim.Requests, lim.Window)
	case "leaky_bucket":
		rate := float64(lim.Requests) / lim.Window.Seconds()
		return algorithms.NewLeakyBucket(s, rate, lim.Requests)
	default:
		// безопасный fallback
		return algorithms.NewFixedWindow(s, lim.Requests, lim.Window)
	}
}

func sanitizePath(path string) string {
	return strings.ReplaceAll(path, "/", "_")
}
```

---

## Файл: internal/middleware/ratelimiter/algorithms/fixed_window_test.go

```go
package algorithms_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/algorithms"
	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestStore — вспомогательная функция: поднимает miniredis и создаёт Store.
// Используется во всех тестах алгоритмов.
func newTestStore(t *testing.T) (*store.Store, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return store.New(rdb), mr
}

func TestFixedWindow_AllowsUpToLimit(t *testing.T) {
	s, _ := newTestStore(t)
	alg := algorithms.NewFixedWindow(s, 3, 60*time.Second)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		r, err := alg.Allow(ctx, "ip:1.2.3.4")
		require.NoError(t, err)
		assert.True(t, r.Allowed, "request %d should be allowed", i+1)
	}
}

func TestFixedWindow_RejectsOverLimit(t *testing.T) {
	s, _ := newTestStore(t)
	alg := algorithms.NewFixedWindow(s, 3, 60*time.Second)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		alg.Allow(ctx, "ip:1.2.3.4") //nolint
	}

	r, err := alg.Allow(ctx, "ip:1.2.3.4")
	require.NoError(t, err)
	assert.False(t, r.Allowed)
	assert.EqualValues(t, 0, r.Remaining)
	assert.Greater(t, r.RetryAfter, time.Duration(0))
}

func TestFixedWindow_ResetsAfterWindow(t *testing.T) {
	s, mr := newTestStore(t)
	alg := algorithms.NewFixedWindow(s, 2, 10*time.Second)
	ctx := context.Background()

	alg.Allow(ctx, "ip:1.2.3.4") //nolint
	alg.Allow(ctx, "ip:1.2.3.4") //nolint

	r, _ := alg.Allow(ctx, "ip:1.2.3.4")
	assert.False(t, r.Allowed, "should be rejected before window reset")

	mr.FastForward(11 * time.Second)

	r, err := alg.Allow(ctx, "ip:1.2.3.4")
	require.NoError(t, err)
	assert.True(t, r.Allowed, "should be allowed after window reset")
}

func TestFixedWindow_IndependentKeys(t *testing.T) {
	s, _ := newTestStore(t)
	alg := algorithms.NewFixedWindow(s, 1, 60*time.Second)
	ctx := context.Background()

	r1, _ := alg.Allow(ctx, "ip:1.1.1.1")
	r2, _ := alg.Allow(ctx, "ip:2.2.2.2")

	assert.True(t, r1.Allowed)
	assert.True(t, r2.Allowed, "different keys must have independent counters")
}
```

---

## Файл: internal/middleware/ratelimiter/algorithms/token_bucket_test.go

```go
package algorithms_test

import (
	"context"
	"testing"
	"time"

	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/algorithms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenBucket_AllowsBurst(t *testing.T) {
	s, _ := newTestStore(t)
	// capacity=5, refill=1/s → можно сделать 5 запросов подряд (burst)
	alg := algorithms.NewTokenBucket(s, 5, 1.0)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		r, err := alg.Allow(ctx, "ip:1.2.3.4")
		require.NoError(t, err)
		assert.True(t, r.Allowed, "burst request %d should pass", i+1)
	}
}

func TestTokenBucket_RejectsWhenEmpty(t *testing.T) {
	s, _ := newTestStore(t)
	alg := algorithms.NewTokenBucket(s, 2, 1.0)
	ctx := context.Background()

	alg.Allow(ctx, "ip:1.2.3.4") //nolint
	alg.Allow(ctx, "ip:1.2.3.4") //nolint

	r, err := alg.Allow(ctx, "ip:1.2.3.4")
	require.NoError(t, err)
	assert.False(t, r.Allowed)
	assert.Greater(t, r.RetryAfter, time.Duration(0))
}

func TestTokenBucket_RefillsOverTime(t *testing.T) {
	s, mr := newTestStore(t)
	alg := algorithms.NewTokenBucket(s, 2, 2.0) // rate=2 токена/сек
	ctx := context.Background()

	alg.Allow(ctx, "ip:1.2.3.4") //nolint
	alg.Allow(ctx, "ip:1.2.3.4") //nolint

	r, _ := alg.Allow(ctx, "ip:1.2.3.4")
	assert.False(t, r.Allowed, "empty bucket should reject")

	// Ждём 1 секунду → добавится 2 токена (rate=2/s)
	mr.FastForward(time.Second)

	r, err := alg.Allow(ctx, "ip:1.2.3.4")
	require.NoError(t, err)
	assert.True(t, r.Allowed, "bucket should have refilled")
}
```

---

## Файл: internal/middleware/ratelimiter/algorithms/sliding_window_test.go

```go
package algorithms_test

import (
	"context"
	"testing"
	"time"

	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/algorithms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlidingWindow_AllowsUpToLimit(t *testing.T) {
	s, _ := newTestStore(t)
	alg := algorithms.NewSlidingWindow(s, 3, 60*time.Second)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		r, err := alg.Allow(ctx, "ip:1.2.3.4")
		require.NoError(t, err)
		assert.True(t, r.Allowed)
	}
}

func TestSlidingWindow_RejectsOverLimit(t *testing.T) {
	s, _ := newTestStore(t)
	alg := algorithms.NewSlidingWindow(s, 3, 60*time.Second)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		alg.Allow(ctx, "ip:1.2.3.4") //nolint
	}

	r, err := alg.Allow(ctx, "ip:1.2.3.4")
	require.NoError(t, err)
	assert.False(t, r.Allowed)
}

func TestSlidingWindow_SlidesCorrectly(t *testing.T) {
	s, mr := newTestStore(t)
	alg := algorithms.NewSlidingWindow(s, 3, 10*time.Second)
	ctx := context.Background()

	// T=0: 3 запроса
	for i := 0; i < 3; i++ {
		alg.Allow(ctx, "ip:1.2.3.4") //nolint
	}

	r, _ := alg.Allow(ctx, "ip:1.2.3.4")
	assert.False(t, r.Allowed, "should be rejected at T=0")

	// Сдвигаем время: все старые записи вышли из окна
	mr.FastForward(11 * time.Second)

	r, err := alg.Allow(ctx, "ip:1.2.3.4")
	require.NoError(t, err)
	assert.True(t, r.Allowed, "old entries should have slid out of window")
}
```

---

## Файл: internal/middleware/ratelimiter/algorithms/leaky_bucket_test.go

```go
package algorithms_test

import (
	"context"
	"testing"
	"time"

	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/algorithms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLeakyBucket_FillsAndOverflows(t *testing.T) {
	s, _ := newTestStore(t)
	// rate=1/s, capacity=3
	alg := algorithms.NewLeakyBucket(s, 1.0, 3)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		r, err := alg.Allow(ctx, "ip:1.2.3.4")
		require.NoError(t, err)
		assert.True(t, r.Allowed, "request %d should fill the bucket", i+1)
	}

	r, err := alg.Allow(ctx, "ip:1.2.3.4")
	require.NoError(t, err)
	assert.False(t, r.Allowed, "full bucket should overflow")
}

func TestLeakyBucket_LeaksOverTime(t *testing.T) {
	s, mr := newTestStore(t)
	alg := algorithms.NewLeakyBucket(s, 1.0, 2) // rate=1/s, capacity=2
	ctx := context.Background()

	alg.Allow(ctx, "ip:1.2.3.4") //nolint
	alg.Allow(ctx, "ip:1.2.3.4") //nolint

	r, _ := alg.Allow(ctx, "ip:1.2.3.4")
	assert.False(t, r.Allowed, "full bucket")

	// Ждём 2 секунды → 2 запроса вытекут (rate=1/s)
	mr.FastForward(2 * time.Second)

	r, err := alg.Allow(ctx, "ip:1.2.3.4")
	require.NoError(t, err)
	assert.True(t, r.Allowed, "bucket should have leaked")
}
```

---

## Обновить: cmd/gateway/main.go

Когда напишешь `store` и `limiter.go` — добавь в существующий `main.go` эти строки:

```go
// В import добавить:
"context"
"github.com/gulmix/apigateway/internal/middleware/ratelimiter"
"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
redispkg "github.com/gulmix/apigateway/pkg/redis"

// После logger.Sync():
rdb := redispkg.NewClient(*cfg)   // NewClient принимает config.Config
if err := rdb.Ping(context.Background()).Err(); err != nil {
    logger.Fatal("redis: ping failed", zap.Error(err))
}
rlStore := store.New(rdb)

// В r.Use(...) добавить после observability.Logger:
r.Use(ratelimiter.RateLimit(rlStore, cfg.Routes))
```

---

## Итоговая структура (полная, соответствует README)

```
cmd/
  gateway/
    main.go                                           ← уже обновлён

internal/
  config/
    config.go                                         ← уже обновлён (Redis + Routes)
  server/
    server.go                                         ← уже создан
  proxy/
    reverse_proxy.go                                  ← уже создан
  loadbalancer/
    balancer.go                                       ← уже обновлён
    algorithms/
      round_robin.go                                  ← уже создан
  middleware/
    observability/
      logging.go                                      ← уже создан
    ratelimiter/                                      ← ПИСАТЬ РУКАМИ (Phase 2)
      store/
        redis_store.go                                ← Lua скрипты
      algorithms/
        algorithm.go                                  ← интерфейс
        fixed_window.go
        token_bucket.go
        sliding_window.go
        leaky_bucket.go
        fixed_window_test.go
        token_bucket_test.go
        sliding_window_test.go
        leaky_bucket_test.go
      scope.go
      metrics.go
      limiter.go                                      ← Gin middleware

pkg/
  redis/
    client.go                                         ← уже создан

config/
  gateway.yaml                                        ← уже обновлён (server + redis)
  routes.yaml                                         ← уже создан (маршруты)
```

## Порядок написания

1. `go get` зависимости (если ещё не)
2. `store/redis_store.go` — начни с `luaFixedWindow`, проверь `AllowFixedWindow` тестом
3. `algorithms/algorithm.go` — интерфейс `Algorithm`
4. `algorithms/fixed_window.go` — самый простой, поймёшь паттерн
5. `algorithms/token_bucket.go`
6. `algorithms/sliding_window.go`
7. `algorithms/leaky_bucket.go`
8. `scope.go`
9. `metrics.go`
10. `limiter.go`
11. Тесты (`*_test.go`)
12. Добавить Redis + rate limiter в `main.go` (см. секцию выше)
