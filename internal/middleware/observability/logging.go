package observability

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var atomicLevel zap.AtomicLevel

func NewLogger(level string) (*zap.Logger, error) {
	atomicLevel = zap.NewAtomicLevel()
	if err := atomicLevel.UnmarshalText([]byte(level)); err != nil {
		atomicLevel.SetLevel(zapcore.InfoLevel)
	}

	cfg := zap.NewProductionConfig()
	cfg.Level = atomicLevel
	return cfg.Build()
}

func Logger(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		spanCtx := trace.SpanFromContext(c.Request.Context()).SpanContext()
		traceID := ""
		if spanCtx.IsValid() {
			traceID = spanCtx.TraceID().String()
		}

		upstream := c.GetString("lb.upstream")
		cacheHit := c.GetString("cache.hit_layer")
		rlRemaining := c.GetInt64("rl.remaining")
		user := c.GetString("auth.user")
		if user == "" {
			user = c.GetString("auth.owner")
		}

		log.Info("request",
			zap.String("trace_id", traceID),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency_ms", time.Since(start)),
			zap.String("upstream", upstream),
			zap.String("cache_hit", cacheHit),
			zap.Int64("rate_limit_remaining", rlRemaining),
			zap.String("user", user),
			zap.String("ip", c.ClientIP()),
		)
	}
}

func LogLevelHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Level string `json:"level" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := atomicLevel.UnmarshalText([]byte(body.Level)); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid level: " + body.Level})
			return
		}
		c.JSON(http.StatusOK, gin.H{"level": body.Level})
	}
}

func GetLogLevel() string {
	return atomicLevel.Level().String()
}
