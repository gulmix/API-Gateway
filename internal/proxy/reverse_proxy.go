package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/gulmix/apigateway/internal/middleware/auth"
	"github.com/gulmix/apigateway/internal/middleware/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type Handler struct{}

func NewHandler() *Handler {
	return &Handler{}
}

func (h *Handler) ServeHTTP(c *gin.Context) {
	target := &url.URL{
		Scheme: c.Request.URL.Scheme,
		Host:   c.Request.URL.Host,
	}
	if target.Host == "" {
		c.AbortWithStatus(http.StatusBadGateway)
		return
	}

	ctx, span := observability.Tracer().Start(c.Request.Context(), "proxy_upstream",
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		oteltrace.WithAttributes(
			semconv.URLFull(target.String()),
			semconv.ServerAddress(target.Host),
		),
	)
	defer span.End()
	c.Request = c.Request.WithContext(ctx)

	propagator := otel.GetTextMapPropagator()

	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host

			propagator.Inject(req.Context(), propagation.HeaderCarrier(req.Header))

			injectIdentityHeaders(c, req)
		},
	}
	rp.ServeHTTP(c.Writer, c.Request)
}

func injectIdentityHeaders(c *gin.Context, req *http.Request) {
	if user := c.GetString(auth.CtxUser); user != "" {
		req.Header.Set("X-Auth-User", user)
	}
	if owner := c.GetString(auth.CtxOwner); owner != "" {
		req.Header.Set("X-Auth-Owner", owner)
	}
	if authType := c.GetString(auth.CtxType); authType != "" {
		req.Header.Set("X-Auth-Type", authType)
	}
	req.Header.Del("X-API-Key")
}
