package loadbalancer

import "sync/atomic"

type RoundRobin struct {
	backends []string
	counter  atomic.Uint64
}

func NewRoundRobin(backends []string) *RoundRobin {
	return &RoundRobin{backends: backends}
}

func (rr *RoundRobin) Next() string {
	idx := rr.counter.Add(1) - 1
	return rr.backends[idx%uint64(len(rr.backends))]
}
