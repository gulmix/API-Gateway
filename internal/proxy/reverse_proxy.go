package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/gulmix/apigateway/internal/middleware/auth"
)

type Handler struct{}

func NewHandler() *Handler {
	return &Handler{}
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

func (h *Handler) ServeHTTP(c *gin.Context) {
	target := &url.URL{
		Scheme: c.Request.URL.Scheme,
		Host:   c.Request.URL.Host,
	}
	if target.Host == "" {
		c.AbortWithStatus(http.StatusBadGateway)
		return
	}
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			injectIdentityHeaders(c, req)
		},
	}
	rp.ServeHTTP(c.Writer, c.Request)
}
