package l1_test

import (
	"testing"
	"time"

	"github.com/gulmix/apigateway/internal/middleware/cache/l1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestL1_SetAndGet(t *testing.T) {
	c, err := l1.New(100)
	require.NoError(t, err)

	c.Set("key", []byte("body"), map[string]string{"Content-Type": "application/json"}, 200, time.Minute)
	body, headers, status, ok := c.Get("key")

	assert.True(t, ok)
	assert.Equal(t, []byte("body"), body)
	assert.Equal(t, 200, status)
	assert.Equal(t, "application/json", headers["Content-Type"])
}

func TestL1_TTLExpiry(t *testing.T) {
	c, _ := l1.New(100)
	c.Set("key", []byte("x"), nil, 200, time.Millisecond)

	time.Sleep(5 * time.Millisecond)

	_, _, _, ok := c.Get("key")
	assert.False(t, ok, "entry should be expired")
}

func TestL1_Delete(t *testing.T) {
	c, _ := l1.New(100)
	c.Set("key", []byte("x"), nil, 200, time.Minute)
	c.Delete("key")

	_, _, _, ok := c.Get("key")
	assert.False(t, ok)
}

func TestL1_Eviction(t *testing.T) {
	c, _ := l1.New(2)
	c.Set("a", []byte("1"), nil, 200, time.Minute)
	c.Set("b", []byte("2"), nil, 200, time.Minute)
	c.Set("c", []byte("3"), nil, 200, time.Minute)

	_, _, _, ok := c.Get("a")
	assert.False(t, ok, "oldest entry should be evicted")
	assert.Equal(t, 2, c.Len())
}
