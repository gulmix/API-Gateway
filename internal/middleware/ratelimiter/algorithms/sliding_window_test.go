package algorithms_test

import (
	"context"
	"testing"
	"time"

	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/algorithms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlidingWindow_AllowsUpToLimit(t *testing.T) {
	s, _ := newTestStore(t)
	alg := algorithms.NewSlidingWindow(s, 3, 60*time.Second)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		r, err := alg.Allow(ctx, "ip:1.2.3.4")
		require.NoError(t, err)
		assert.True(t, r.Allowed)
	}
}

func TestSlidingWindow_RejectsOverLimit(t *testing.T) {
	s, _ := newTestStore(t)
	alg := algorithms.NewSlidingWindow(s, 3, 60*time.Second)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		alg.Allow(ctx, "ip:1.2.3.4")
	}

	r, err := alg.Allow(ctx, "ip:1.2.3.4")
	require.NoError(t, err)
	assert.False(t, r.Allowed)
}

func TestSlidingWindow_SlidesCorrectly(t *testing.T) {
	s, mr := newTestStore(t)
	alg := algorithms.NewSlidingWindow(s, 3, 10*time.Second)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		alg.Allow(ctx, "ip:1.2.3.4")
	}

	r, _ := alg.Allow(ctx, "ip:1.2.3.4")
	assert.False(t, r.Allowed, "should be rejected at T=0")

	mr.FastForward(11 * time.Second)

	r, err := alg.Allow(ctx, "ip:1.2.3.4")
	require.NoError(t, err)
	assert.True(t, r.Allowed, "old entries should have slid out of window")
}
