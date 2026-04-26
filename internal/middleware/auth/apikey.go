package auth

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gulmix/apigateway/internal/config"
	"github.com/redis/go-redis/v9"
)

type apiKeyPayload struct {
	Owner  string   `json:"owner"`
	Scopes []string `json:"scopes"`
	Tier   string   `json:"tier"`
}

type apiKeyMiddleware struct {
	rdb    *redis.Client
	header string
}

func newAPIKeyMiddleware(rdb *redis.Client, cfg config.APIKeyConfig) *apiKeyMiddleware {
	h := cfg.Header
	if h == "" {
		h = "X-API-Key"
	}
	return &apiKeyMiddleware{rdb: rdb, header: h}
}

func (m *apiKeyMiddleware) tryAuth(c *gin.Context) bool {
	raw := c.GetHeader(m.header)
	if raw == "" {
		raw = c.Query("api_key")
	}
	if raw == "" {
		return false
	}

	val, err := m.rdb.Get(context.Background(), "apiKey:"+raw).Result()
	if err != nil {
		return false
	}

	var p apiKeyPayload
	if err := json.Unmarshal([]byte(val), &p); err != nil {
		return false
	}

	c.Set(CtxType, "apikey")
	c.Set(CtxOwner, p.Owner)
	c.Set(CtxScopes, p.Scopes)
	return true
}

func CreateAPIKeyHandler(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Key    string   `json:"key" binding:"required"`
			Owner  string   `json:"owner" binding:"required"`
			Scopes []string `json:"scopes"`
			Tier   string   `json:"tier"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		payload := apiKeyPayload{Owner: body.Owner, Scopes: body.Scopes, Tier: body.Tier}
		data, _ := json.Marshal(payload)
		if err := rdb.Set(context.Background(), "apiKey:"+body.Key, data, 0).Err(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "redis error"})
			return
		}
		c.JSON(http.StatusCreated, gin.H{"status": "created"})
	}
}

func DeleteAPIKeyHandler(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.Param("key")
		rdb.Del(context.Background(), "apikey:"+key)
		c.JSON(http.StatusOK, gin.H{"status": "revoked"})
	}
}

func GetAPIKeyHandler(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		val, err := rdb.Get(context.Background(), "apikey:"+c.Param("key")).Result()
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.Data(http.StatusOK, "application/json", []byte(val))
	}
}
