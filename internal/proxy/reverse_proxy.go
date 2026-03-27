package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/gulmix/apigateway/internal/loadbalancer"
)

type Handler struct {
	lb loadbalancer.Balancer
}

func NewHandler(lb loadbalancer.Balancer) *Handler {
	return &Handler{lb: lb}
}

func (h *Handler) ServeHTTP(c *gin.Context) {
	target, err := url.Parse(h.lb.Next())
	if err != nil {
		c.AbortWithStatus(http.StatusBadGateway)
		return
	}
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.ServeHTTP(c.Writer, c.Request)
}
