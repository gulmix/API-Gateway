package algorithms

import (
	"context"

	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
)

type LeakyBucket struct {
	store    *store.Store
	rate     float64
	capacity int
}

func NewLeakyBucket(s *store.Store, rate float64, capacity int) *LeakyBucket {
	return &LeakyBucket{
		store:    s,
		rate:     rate,
		capacity: capacity,
	}
}

func (lb *LeakyBucket) Allow(ctx context.Context, key string) (store.Result, error) {
	return lb.store.AllowLeakyBucket(ctx, key, lb.rate, lb.capacity)
}
