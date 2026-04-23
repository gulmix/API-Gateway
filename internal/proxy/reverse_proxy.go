package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/gin-gonic/gin"
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
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.ServeHTTP(c.Writer, c.Request)
}
