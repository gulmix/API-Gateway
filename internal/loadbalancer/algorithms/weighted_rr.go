package algorithms

import (
	"math"
	"sync"

	lb "github.com/gulmix/apigateway/internal/loadbalancer"
)

type wrrEntry struct {
	backend       *lb.Backend
	currentWeight int64
}

type WeightedRoundRobin struct {
	mu      sync.Mutex
	entries map[string]*wrrEntry
}

func NewWeightedRoundRobin() *WeightedRoundRobin {
	return &WeightedRoundRobin{entries: make(map[string]*wrrEntry)}
}

func (wrr *WeightedRoundRobin) Next(backends []*lb.Backend, _ string) *lb.Backend {
	wrr.mu.Lock()
	defer wrr.mu.Unlock()

	live := make(map[string]struct{}, len(backends))
	for _, b := range backends {
		live[b.Addr] = struct{}{}
		if _, ok := wrr.entries[b.Addr]; !ok {
			wrr.entries[b.Addr] = &wrrEntry{backend: b}
		}
	}
	for addr := range wrr.entries {
		if _, ok := live[addr]; !ok {
			delete(wrr.entries, addr)
		}
	}

	var totalWeight int64
	for _, b := range backends {
		totalWeight += int64(b.Weight)
	}
	if totalWeight == 0 {
		return nil
	}

	var best *wrrEntry
	var bestW int64 = math.MinInt64

	for _, e := range wrr.entries {
		if !e.backend.IsHealthy() {
			continue
		}

		e.currentWeight += int64(e.backend.Weight)
		if e.currentWeight > bestW {
			bestW = e.currentWeight
			best = e
		}
	}

	if best == nil {
		return nil
	}

	best.currentWeight -= totalWeight
	return best.backend
}
