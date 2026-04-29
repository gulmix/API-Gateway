package ratelimiter

import (
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gulmix/apigateway/internal/config"
	"github.com/gulmix/apigateway/internal/middleware/observability"
	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/algorithms"
	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
	"go.opentelemetry.io/otel/attribute"
)

type routeLimiter struct {
	path       string
	enabled    bool
	algorithm  string
	scopes     []string
	algByScope map[string]algorithms.Algorithm
	limByScope map[string]config.ScopeLimit
}

func RateLimit(s *store.Store, routes []config.RouteConfig) gin.HandlerFunc {
	limiters := buildLimiters(s, routes)

	return func(c *gin.Context) {
		rl := findLimiter(limiters, c.Request.URL.Path)
		if rl == nil || !rl.enabled {
			c.Next()
			return
		}

		ctx, span := observability.Tracer().Start(c.Request.Context(), "rate_limit")
		c.Request = c.Request.WithContext(ctx)
		defer span.End()

		var minRemaining int64 = -1

		for _, scope := range rl.scopes {
			scopeKey, ok := ScopeKey(c, scope)
			if !ok {
				continue
			}
			alg := rl.algByScope[scope]
			lim := rl.limByScope[scope]
			rediskey := fmt.Sprintf("rl:%s:%s", scopeKey, sanitizePath(rl.path))

			result, err := alg.Allow(c.Request.Context(), rediskey)
			if err != nil {
				continue
			}

			recordMetrics(rl.path, scope, rl.algorithm, result.Allowed, result.Remaining)

			if minRemaining < 0 || result.Remaining < minRemaining {
				minRemaining = result.Remaining
			}

			c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", lim.Requests))
			c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", result.Remaining))

			if !result.Allowed {
				span.SetAttributes(
					attribute.String("rl.scope", scope),
					attribute.Bool("rl.rejected", true),
				)
				if result.RetryAfter > 0 {
					secs := int(math.Ceil(result.RetryAfter.Seconds()))
					c.Header("Retry-After", fmt.Sprintf("%d", secs))
				}
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded", "scope": scope})
				return
			}
		}

		if minRemaining >= 0 {
			c.Set("rl.remaining", minRemaining)
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
			algorithm:  route.RateLimit.Algorithm,
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
		refillRate := float64(lim.Requests) / lim.Window.Seconds()
		return algorithms.NewTokenBucket(s, lim.Requests, refillRate)
	case "sliding_window":
		return algorithms.NewSlidingWindow(s, lim.Requests, lim.Window)
	case "leaky_bucket":
		rate := float64(lim.Requests) / lim.Window.Seconds()
		return algorithms.NewLeakyBucket(s, rate, lim.Requests)
	default:
		return algorithms.NewFixedWindow(s, lim.Requests, lim.Window)
	}
}

func sanitizePath(path string) string {
	return strings.ReplaceAll(path, "/", "_")
}
