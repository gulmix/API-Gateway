package cache

import (
	"bytes"
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gulmix/apigateway/internal/config"
	"github.com/gulmix/apigateway/internal/middleware/cache/l1"
	"github.com/gulmix/apigateway/internal/middleware/cache/l2"
	"github.com/redis/go-redis/v9"
)

type Manager struct {
	l1  *l1.Cache
	l2  *l2.Cache
	rdb *redis.Client
	cfg config.CacheConfig
}

func NewManager(rdb *redis.Client, cfg config.CacheConfig) (*Manager, error) {
	l1c, err := l1.New(cfg.L1.MaxItems)
	if err != nil {
		return nil, err
	}
	return &Manager{
		l1:  l1c,
		l2:  l2.New(rdb),
		rdb: rdb,
		cfg: cfg,
	}, nil
}

func (m *Manager) Middleware(routes []config.RouteConfig) gin.HandlerFunc {
	routeMap := buildRouteMap(routes)

	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Next()
			return
		}

		entry := routeMap[matchRoute(routeMap, c.Request.URL.Path)]
		if !entry.cfg.Enabled {
			c.Next()
			return
		}
		ttl := entry.ttl
		vary := entry.vary
		if ttl == 0 {
			ttl = m.cfg.L2.DefaultTTL
		}

		key := Key(c.Request, vary)
		route := c.Request.URL.Path

		if body, headers, status, ok := m.l1.Get(key); ok {
			writeFromCache(c, body, headers, status)
			recordHit("l1", route)
			return
		}

		if resp, err := m.l2.Get(c.Request.Context(), key); err == nil && resp != nil {
			m.l1.Set(key, resp.Body, resp.Headers, resp.Status, m.cfg.L1.DefaultTTL)
			writeFromCache(c, resp.Body, resp.Headers, resp.Status)
			recordHit("l2", route)
			return
		}

		recordMisses(route)

		cap := &responseCapture{ResponseWriter: c.Writer, buf: &bytes.Buffer{}}
		c.Writer = cap
		c.Next()

		if cap.Status() < 200 || cap.Status() >= 300 {
			return
		}

		body := cap.buf.Bytes()
		header := captureHeader(cap)
		cached := &l2.CachedResponse{Status: cap.Status(), Headers: header, Body: body}

		m.l2.Set(c.Request.Context(), key, cached, ttl)
		m.l1.Set(key, body, header, cap.Status(), m.cfg.L1.DefaultTTL)
	}
}

func (m *Manager) StartInvalidationSubscriber(ctx context.Context) {
	ch := m.cfg.InvalidationChannel
	if ch == "" {
		return
	}
	sub := m.rdb.Subscribe(ctx, ch)
	go func() {
		defer sub.Close()
		msgs := sub.Channel()
		for {
			select {
			case msg, ok := <-msgs:
				if !ok {
					return
				}
				m.invalidate(ctx, msg.Payload)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (m *Manager) invalidate(ctx context.Context, pattern string) {
	m.l1.Delete(pattern)

	keys, err := m.l2.Scan(ctx, pattern)
	if err != nil {
		return
	}
	for _, k := range keys {
		m.l2.Delete(ctx, k)
		m.l1.Delete(k)
	}
}

func (m *Manager) Publish(ctx context.Context, pattern string) error {
	return m.rdb.Publish(ctx, m.cfg.InvalidationChannel, pattern).Err()
}

func (m *Manager) L1Len() int {
	return m.l1.Len()
}

type responseCapture struct {
	gin.ResponseWriter
	buf *bytes.Buffer
}

func (rc *responseCapture) Write(b []byte) (int, error) {
	rc.buf.Write(b)
	return rc.ResponseWriter.Write(b)
}

func (rc *responseCapture) WriteString(s string) (int, error) {
	rc.buf.WriteString(s)
	return rc.ResponseWriter.WriteString(s)
}

func writeFromCache(c *gin.Context, body []byte, headers map[string]string, status int) {
	for k, v := range headers {
		c.Header(k, v)
	}
	c.Header("X-Cache", "HIT")
	c.Status(status)
	c.Writer.Write(body)
	c.Abort()
}

func captureHeader(rc *responseCapture) map[string]string {
	keep := []string{"Content-Type", "Content-Encoding", "Cache-Control", "ETag", "Last-Modified"}
	out := make(map[string]string, len(keep))

	for _, h := range keep {
		if v := rc.Header().Get(h); v != "" {
			out[h] = v
		}
	}

	return out
}

type routeEntry struct {
	cfg  config.RouteCacheConfig
	ttl  time.Duration
	vary []string
}

func buildRouteMap(routes []config.RouteConfig) map[string]routeEntry {
	m := make(map[string]routeEntry, len(routes))

	for _, r := range routes {
		m[r.Path] = routeEntry{cfg: r.Cache, ttl: r.Cache.TTL, vary: r.Cache.Vary}
	}

	return m
}

func matchRoute(m map[string]routeEntry, path string) string {
	for prefix := range m {
		if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			return prefix
		}
	}
	return ""
}
