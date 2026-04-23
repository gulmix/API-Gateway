package loadbalancer

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gulmix/apigateway/internal/config"
)

type Balancer interface {
	Next(backends []*Backend, key string) *Backend
}

func HealthyBackends(backends []*Backend) []*Backend {
	out := make([]*Backend, 0, len(backends))
	for _, b := range backends {
		if b.IsHealthy() {
			out = append(out, b)
		}
	}
	return out
}

func Middleware(reg *Registry, routes []config.RouteConfig) gin.HandlerFunc {
	type routeEntry struct {
		upstream string
		hashKey  string
	}

	table := make(map[string]routeEntry, len(routes))
	for _, r := range routes {
		table[r.Path] = routeEntry{upstream: r.Upstream, hashKey: r.HashKey}
	}

	return func(c *gin.Context) {
		entry := matchRoute(table, c.Request.URL.Path)
		pool := reg.Pool(entry.upstream)
		if pool == nil {
			c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "unknown upstream"})
			return
		}

		key := resolveKey(c, entry.hashKey)
		b := pool.Pick(key)
		if b == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "no healthy backends"})
			return
		}

		c.Set("lb.backend", b)
		c.Set("lb.pool", pool)

		c.Request.URL.Scheme = "http"
		c.Request.URL.Host = b.Addr

		c.Next()

		pool.Done(b, c.Writer.Status())
	}
}

func resolveKey(c *gin.Context, hashKey string) string {
	if strings.HasPrefix(hashKey, "header:") {
		return c.GetHeader(strings.TrimPrefix(hashKey, "header:"))
	}
	return c.ClientIP()
}

func matchRoute[V any](table map[string]V, path string) V {
	for prefix, v := range table {
		if strings.HasPrefix(path, prefix) {
			return v
		}
	}
	var zero V
	return zero
}
