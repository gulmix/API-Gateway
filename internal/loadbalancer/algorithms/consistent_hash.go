package algorithms

import (
	"hash/fnv"

	lb "github.com/gulmix/apigateway/internal/loadbalancer"
)

type ConsistentHash struct{}

func NewConsistentHash() *ConsistentHash {
	return &ConsistentHash{}
}

func (ch *ConsistentHash) Next(backends []*lb.Backend, key string) *lb.Backend {
	healthy := lb.HealthyBackends(backends)
	if len(healthy) == 0 {
		return nil
	}

	h := fnv64a(key)
	idx := jumpHash(h, len(healthy))
	return healthy[idx]
}

func jumpHash(key uint64, numBuckets int) int {
	var b, j int64 = -1, 0
	for j < int64(numBuckets) {
		b = j
		key = key*2862933555777941757 + 1
		j = int64(float64(b+1) * (float64(int64(1)<<31) / float64((key>>33)+1)))
	}
	return int(b)
}

func fnv64a(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}
