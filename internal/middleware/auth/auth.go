package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gulmix/apigateway/internal/config"
	"github.com/redis/go-redis/v9"
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

		if ok := apiKeyMiddleware.tryAuth(c); ok {
			c.Next()
			return
		}

		if ok := jwtMiddleware.tryAuth(c); ok {
			c.Next()
			return
		}

		if mode == "required" {
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
