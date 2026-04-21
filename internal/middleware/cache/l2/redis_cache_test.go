package l2_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gulmix/apigateway/internal/middleware/cache/l2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestCache(t *testing.T) (*l2.Cache, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return l2.New(rdb), mr
}

func TestL2_SetAndGet(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	resp := &l2.CachedResponse{
		Status:  200,
		Headers: map[string]string{"Content-Type": "text/plain"},
		Body:    []byte("hello"),
	}
	require.NoError(t, c.Set(ctx, "cache:abc", resp, time.Minute))

	got, err := c.Get(ctx, "cache:abc")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 200, got.Status)
	assert.Equal(t, []byte("hello"), got.Body)
}

func TestL2_MissReturnsNil(t *testing.T) {
	c, _ := newTestCache(t)
	got, err := c.Get(context.Background(), "cache:noexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestL2_TTLExpiry(t *testing.T) {
	c, mr := newTestCache(t)
	ctx := context.Background()

	c.Set(ctx, "cache:k", &l2.CachedResponse{Status: 200, Body: []byte("x")}, time.Second)
	mr.FastForward(2 * time.Second)

	got, err := c.Get(ctx, "cache:k")
	require.NoError(t, err)
	assert.Nil(t, got, "entry should be expired")
}

func TestT2_Delete(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	c.Set(ctx, "cache:k", &l2.CachedResponse{Status: 200, Body: []byte("x")}, time.Minute)
	require.NoError(t, c.Delete(ctx, "cache:k"))

	got, _ := c.Get(ctx, "cache:k")
	assert.Nil(t, got)
}

func TestL2_Scan(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	c.Set(ctx, "cache:a1", &l2.CachedResponse{Status: 200}, time.Minute)
	c.Set(ctx, "cache:a2", &l2.CachedResponse{Status: 200}, time.Minute)
	c.Set(ctx, "cache:b1", &l2.CachedResponse{Status: 200}, time.Minute)

	keys, err := c.Scan(ctx, "cache:a*")
	require.NoError(t, err)
	assert.Len(t, keys, 2)
}
