package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gulmix/apigateway/internal/config"
	"github.com/gulmix/apigateway/internal/middleware/observability"
	"github.com/redis/go-redis/v9"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

const (
	CtxType   = "auth.type"
	CtxOwner  = "auth.owner"
	CtxUser   = "auth.user"
	CtxScopes = "auth.scopes"
)

func Middleware(rdb *redis.Client, cfg config.AuthConfig, routes []config.RouteConfig) gin.HandlerFunc {
	apiKeyMiddleware := newAPIKeyMiddleware(rdb, cfg.APIKeys)
	jwtMiddleware, err := newJWTMiddleware(cfg.JWT)
	if err != nil {
		panic("auth: failed to init JWT middleware: " + err.Error())
	}

	return func(c *gin.Context) {
		mode := routeAuthMode(routes, c.Request.URL.Path)
		if mode == "none" {
			c.Next()
			return
		}

		ctx, span := observability.Tracer().Start(c.Request.Context(), "auth")
		c.Request = c.Request.WithContext(ctx)
		defer span.End()

		if ok := apiKeyMiddleware.tryAuth(c); ok {
			span.SetAttributes(semconv.EnduserID(c.GetString(CtxOwner)))
			c.Next()
			return
		}

		if ok := jwtMiddleware.tryAuth(c); ok {
			span.SetAttributes(semconv.EnduserID(c.GetString(CtxUser)))
			c.Next()
			return
		}

		if mode == "required" {
			span.SetAttributes(semconv.HTTPResponseStatusCode(http.StatusUnauthorized))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		c.Next()
	}
}

func routeAuthMode(routes []config.RouteConfig, path string) string {
	for _, r := range routes {
		if strings.HasPrefix(path, r.Path) {
			if r.Auth == "" {
				return "required"
			}
			return r.Auth
		}
	}
	return "required"
}
