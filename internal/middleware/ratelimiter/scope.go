package ratelimiter

import (
	"strings"

	"github.com/gin-gonic/gin"
)

func ScopeKey(c *gin.Context, scope string) (string, bool) {
	switch scope {
	case "user":
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
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	return c.ClientIP()
}
