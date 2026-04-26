package proxy_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gulmix/apigateway/internal/middleware/auth"
	"github.com/gulmix/apigateway/internal/proxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
}

func (r *closeNotifyRecorder) CloseNotify() <-chan bool {
	return make(chan bool, 1)
}

func newRecorder() *closeNotifyRecorder {
	return &closeNotifyRecorder{httptest.NewRecorder()}
}

func newProxyContext(t *testing.T, addr string) (*gin.Context, *closeNotifyRecorder) {
	t.Helper()
	w := newRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.URL.Scheme = "http"
	req.URL.Host = addr
	c.Request = req
	return c, w
}

func TestServeHTTP_Returns502WhenNoHost(t *testing.T) {
	w := newRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)

	proxy.NewHandler().ServeHTTP(c)

	assert.Equal(t, http.StatusBadGateway, w.Code)
}

func TestServeHTTP_ForwardsAllIdentityHeaders(t *testing.T) {
	var received http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
	}))
	defer upstream.Close()

	c, _ := newProxyContext(t, upstream.Listener.Addr().String())
	c.Request.Header.Set("X-API-Key", "secret")
	c.Set(auth.CtxUser, "alice")
	c.Set(auth.CtxOwner, "org-1")
	c.Set(auth.CtxType, "jwt")

	proxy.NewHandler().ServeHTTP(c)

	require.NotNil(t, received)
	assert.Equal(t, "alice", received.Get("X-Auth-User"))
	assert.Equal(t, "org-1", received.Get("X-Auth-Owner"))
	assert.Equal(t, "jwt", received.Get("X-Auth-Type"))
	assert.Empty(t, received.Get("X-API-Key"), "X-API-Key must be stripped")
}

func TestServeHTTP_PartialIdentity(t *testing.T) {
	var received http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
	}))
	defer upstream.Close()

	c, _ := newProxyContext(t, upstream.Listener.Addr().String())
	c.Set(auth.CtxOwner, "org-2")

	proxy.NewHandler().ServeHTTP(c)

	require.NotNil(t, received)
	assert.Empty(t, received.Get("X-Auth-User"))
	assert.Equal(t, "org-2", received.Get("X-Auth-Owner"))
	assert.Empty(t, received.Get("X-Auth-Type"))
}

func TestServeHTTP_StripsAPIKeyWithoutAuthContext(t *testing.T) {
	var received http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
	}))
	defer upstream.Close()

	c, _ := newProxyContext(t, upstream.Listener.Addr().String())
	c.Request.Header.Set("X-API-Key", "my-secret-key")

	proxy.NewHandler().ServeHTTP(c)

	require.NotNil(t, received)
	assert.Empty(t, received.Get("X-API-Key"))
}

func TestServeHTTP_ProxiesResponseStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte("teapot")) //nolint:errcheck
	}))
	defer upstream.Close()

	c, w := newProxyContext(t, upstream.Listener.Addr().String())
	proxy.NewHandler().ServeHTTP(c)

	assert.Equal(t, http.StatusTeapot, w.Code)
}
