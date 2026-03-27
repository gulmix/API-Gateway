package algorithms

import (
	"context"

	"github.com/gulmix/apigateway/internal/middleware/ratelimiter/store"
)

type Algorithm interface {
	Allow(ctx context.Context, key string) (store.Result, error)
}
