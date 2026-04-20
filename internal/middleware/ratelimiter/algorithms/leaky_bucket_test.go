package algorithms_test

import (
	"context"
	"testing"
	"time"

	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/algorithms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLeakyBucket_FillsAndOverflows(t *testing.T) {
	s, _ := newTestStore(t)
	alg := algorithms.NewLeakyBucket(s, 1.0, 3)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		r, err := alg.Allow(ctx, "ip:1.2.3.4")
		require.NoError(t, err)
		assert.True(t, r.Allowed, "request %d should fill the bucket", i+1)
	}

	r, err := alg.Allow(ctx, "ip:1.2.3.4")
	require.NoError(t, err)
	assert.False(t, r.Allowed, "full bucket should overflow")
}

func TestLeakyBucket_LeaksOverTime(t *testing.T) {
	s, _, advance := newTestEnv(t)
	alg := algorithms.NewLeakyBucket(s, 1.0, 2)
	ctx := context.Background()

	alg.Allow(ctx, "ip:1.2.3.4")
	alg.Allow(ctx, "ip:1.2.3.4")

	r, _ := alg.Allow(ctx, "ip:1.2.3.4")
	assert.False(t, r.Allowed, "full bucket")

	advance(2 * time.Second)

	r, err := alg.Allow(ctx, "ip:1.2.3.4")
	require.NoError(t, err)
	assert.True(t, r.Allowed, "bucket should have leaked")
}
