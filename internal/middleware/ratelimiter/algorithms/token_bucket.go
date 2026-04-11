package algorithms

import (
	"context"

	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
)

type TokenBucket struct {
	store      *store.Store
	capacity   int
	refillRate float64
}

func NewTokenBucket(s *store.Store, capacity int, refillRate float64) *TokenBucket {
	return &TokenBucket{
		store:      s,
		capacity:   capacity,
		refillRate: refillRate,
	}
}

func (t *TokenBucket) Allow(ctx context.Context, key string) (store.Result, error) {
	return t.store.AllowTokenBucket(ctx, key, t.capacity, t.refillRate)
}
