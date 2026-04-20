package algorithms_test

import (
	"context"
	"testing"
	"time"

	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/algorithms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenBucket_AllowsBurst(t *testing.T) {
	s, _ := newTestStore(t)
	alg := algorithms.NewTokenBucket(s, 5, 1.0)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		r, err := alg.Allow(ctx, "ip:1.2.3.4")
		require.NoError(t, err)
		assert.True(t, r.Allowed, "burst request %d should pass", i+1)
	}
}

func TestTokenBucket_RejectesWhenEmpty(t *testing.T) {
	s, _ := newTestStore(t)
	alg := algorithms.NewTokenBucket(s, 2, 1.0)
	ctx := context.Background()

	alg.Allow(ctx, "ip:1.2.3.4")
	alg.Allow(ctx, "ip:1.2.3.4")

	r, err := alg.Allow(ctx, "ip:1.2.3.4")
	require.NoError(t, err)
	assert.False(t, r.Allowed)
	assert.Greater(t, r.RetryAfter, time.Duration(0))
}

func TestTokenBucket_RefillsOverTime(t *testing.T) {
	s, _, advance := newTestEnv(t)
	alg := algorithms.NewTokenBucket(s, 2, 2.0)
	ctx := context.Background()

	alg.Allow(ctx, "ip:1.2.3.4")
	alg.Allow(ctx, "ip:1.2.3.4")

	r, _ := alg.Allow(ctx, "ip:1.2.3.4")
	assert.False(t, r.Allowed, "empty bucket should reject")

	advance(time.Second)

	r, err := alg.Allow(ctx, "ip:1.2.3.4")
	require.NoError(t, err)
	assert.True(t, r.Allowed, "bucket should have refilled")
}
