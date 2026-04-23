package algorithms

import (
	"math"

	lb "github.com/gulmix/apigateway/internal/loadbalancer"
)

type LeastConnections struct{}

func NewLeastConnections() *LeastConnections {
	return &LeastConnections{}
}

func (lc *LeastConnections) Next(backends []*lb.Backend, _ string) *lb.Backend {
	healthy := lb.HealthyBackends(backends)
	if len(healthy) == 0 {
		return nil
	}

	var pick *lb.Backend
	var min int64 = math.MaxInt64

	for _, b := range healthy {
		if c := b.ActiveConns.Load(); c < min {
			min = c
			pick = b
		}
	}

	return pick
}
