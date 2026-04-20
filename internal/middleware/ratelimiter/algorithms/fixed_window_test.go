package algorithms_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/algorithms"
	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) (*store.Store, *miniredis.Miniredis) {
	s, mr, _ := newTestEnv(t)
	return s, mr
}

func newTestEnv(t *testing.T) (*store.Store, *miniredis.Miniredis, func(time.Duration)) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	now := float64(time.Now().UnixNano()) / 1e9
	advance := func(d time.Duration) {
		now += d.Seconds()
		mr.FastForward(d)
	}
	return store.NewWithClock(rdb, func() float64 { return now }), mr, advance
}

func TestFixedWindow_AllowsUpToLimit(t *testing.T) {
	s, _ := newTestStore(t)
	alg := algorithms.NewFixedWindow(s, 3, 60*time.Second)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		r, err := alg.Allow(ctx, "ip:1.2.3.4")
		require.NoError(t, err)
		assert.True(t, r.Allowed, "request %d should be allowed", i+1)
	}
}

func TestFixedWindow_RejectsOverLimit(t *testing.T) {
	s, _ := newTestStore(t)
	alg := algorithms.NewFixedWindow(s, 3, 60*time.Second)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		alg.Allow(ctx, "ip:1.2.3.4")
	}

	r, err := alg.Allow(ctx, "ip:1.2.3.4")
	require.NoError(t, err)
	assert.False(t, r.Allowed)
	assert.EqualValues(t, 0, r.Remaining)
	assert.Greater(t, r.RetryAfter, time.Duration(0))
}

func TestFixedWindow_ResetsAfterWindow(t *testing.T) {
	s, mr := newTestStore(t)
	alg := algorithms.NewFixedWindow(s, 2, 10*time.Second)
	ctx := context.Background()

	alg.Allow(ctx, "ip:1.2.3.4")
	alg.Allow(ctx, "ip:1.2.3.4")

	r, _ := alg.Allow(ctx, "ip:1.2.3.4")
	assert.False(t, r.Allowed, "should be rejected before window reset")

	mr.FastForward(11 * time.Second)

	r, err := alg.Allow(ctx, "ip:1.2.3.4")
	require.NoError(t, err)
	assert.True(t, r.Allowed, "should be allowed after window reset")
}

func TestFixedWindow_IndependentKeys(t *testing.T) {
	s, _ := newTestStore(t)
	alg := algorithms.NewFixedWindow(s, 1, 60*time.Second)
	ctx := context.Background()

	r1, _ := alg.Allow(ctx, "ip:1.1.1.1")
	r2, _ := alg.Allow(ctx, "ip:2.2.2.2")

	assert.True(t, r1.Allowed)
	assert.True(t, r2.Allowed, "different keys must have independent counters")
}
