package cache_test

import (
	"net/http"
	"testing"

	"github.com/gulmix/apigateway/internal/middleware/cache"
	"github.com/stretchr/testify/assert"
)

func newReq(method, rawURL string) *http.Request {
	r, _ := http.NewRequest(method, rawURL, nil)
	return r
}

func TestKey_Deterministic(t *testing.T) {
	r := newReq("GET", "http://api.example.com/search?b=2&a=1")
	assert.Equal(t, cache.Key(r, nil), cache.Key(r, nil))
}

func TestKey_QueryOrderIndependent(t *testing.T) {
	r1 := newReq("GET", "http://host/path?a=1&b=2")
	r2 := newReq("GET", "http://host/path?b=2&a=1")
	assert.Equal(t, cache.Key(r1, nil), cache.Key(r2, nil))
}

func TestKey_DifferentMethodsDifferentKeys(t *testing.T) {
	get := newReq("GET", "http://host/path")
	head := newReq("HEAD", "http://host/path")
	assert.NotEqual(t, cache.Key(get, nil), cache.Key(head, nil))
}

func TestKey_VaryHeader(t *testing.T) {
	r1 := newReq("GET", "http://host/path")
	r1.Header.Set("Accept-Language", "en")
	r2 := newReq("GET", "http://host/path")
	r2.Header.Set("Accept-Language", "ru")
	assert.NotEqual(t, cache.Key(r1, []string{"Accept-Language"}), cache.Key(r2, []string{"Accept-Language"}))
}
