package cache

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (m *Manager) PurgeHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		pattern := c.Query("pattern")
		if pattern == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "pattern required"})
			return
		}

		if err := m.Publish(c.Request.Context(), pattern); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		m.invalidate(c.Request.Context(), pattern)
		c.JSON(http.StatusOK, gin.H{"invalidated": pattern})
	}
}

func (m *Manager) StatsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"l1_items": m.L1Len(),
		})
	}
}
