package algorithms

import (
	"sync/atomic"

	lb "github.com/gulmix/apigateway/internal/loadbalancer"
)

type RoundRobin struct {
	counter atomic.Uint64
}

func NewRoundRobin() *RoundRobin {
	return &RoundRobin{}
}

func (rr *RoundRobin) Next(backends []*lb.Backend, _ string) *lb.Backend {
	healthy := lb.HealthyBackends(backends)
	if len(healthy) == 0 {
		return nil
	}
	idx := rr.counter.Add(1) - 1
	return healthy[idx%uint64(len(healthy))]
}
