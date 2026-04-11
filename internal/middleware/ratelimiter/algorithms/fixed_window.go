package algorithms

import (
	"context"
	"time"

	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
)

type FixedWindow struct {
	store  *store.Store
	limit  int
	window time.Duration
}

func NewFixedWindow(s *store.Store, limit int, window time.Duration) *FixedWindow {
	return &FixedWindow{
		store:  s,
		limit:  limit,
		window: window,
	}
}

func (f *FixedWindow) Allow(ctx context.Context, key string) (store.Result, error) {
	return f.store.AllowFixedWindow(ctx, key, f.limit, f.window)
}
