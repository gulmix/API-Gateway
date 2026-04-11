package algorithms

import (
	"context"
	"time"

	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
)

type SlidingWindow struct {
	store  *store.Store
	limit  int
	window time.Duration
}

func NewSlidingWindow(s *store.Store, limit int, window time.Duration) *SlidingWindow {
	return &SlidingWindow{
		store:  s,
		limit:  limit,
		window: window,
	}
}

func (sw *SlidingWindow) Allow(ctx context.Context, key string) (store.Result, error) {
	return sw.store.AllowSlidingWindow(ctx, key, sw.limit, sw.window)
}
