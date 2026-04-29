package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total HTTP requests processed by the gateway.",
		},
		[]string{"method", "path", "status", "upstream"},
	)

	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_request_duration_seconds",
			Help:    "End-to-end request latency.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path", "upstream"},
	)
)

func init() {
	prometheus.MustRegister(requestsTotal, requestDuration)
}

func MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		upstream := c.GetString("lb.upstream")
		if upstream == "" {
			upstream = "unknown"
		}
		status := strconv.Itoa(c.Writer.Status())
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}

		requestsTotal.WithLabelValues(c.Request.Method, path, status, upstream).Inc()
		requestDuration.WithLabelValues(c.Request.Method, path, upstream).Observe(time.Since(start).Seconds())
	}
}

func MetricsHandler() gin.HandlerFunc {
	h := promhttp.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}

func MetricsHTTPHandler() http.Handler {
	return promhttp.Handler()
}
