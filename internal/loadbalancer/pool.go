package loadbalancer

import (
	"sync"
	"sync/atomic"
)

type Breaker interface {
	Record(upstream string, b *Backend, statusCode int)
}

type Backend struct {
	Addr        string
	Weight      int
	ActiveConns atomic.Int64
	healthy     atomic.Bool
}

func NewBackend(addr string, weight int) *Backend {
	b := &Backend{Addr: addr, Weight: weight}
	b.healthy.Store(true)
	return b
}

func (b *Backend) IsHealthy() bool {
	return b.healthy.Load()
}

func (b *Backend) SetHealthy(v bool) {
	b.healthy.Store(v)
}

type Pool struct {
	mu       sync.Mutex
	ptr      atomic.Pointer[[]*Backend]
	upstream string
	balancer Balancer
	breaker  Breaker
}

func NewPool(upstream string, alg Balancer, breaker Breaker) *Pool {
	p := &Pool{upstream: upstream, balancer: alg, breaker: breaker}
	empty := make([]*Backend, 0)
	p.ptr.Store(&empty)
	return p
}

func (p *Pool) Add(b *Backend) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cur := *p.ptr.Load()
	next := make([]*Backend, len(cur)+1)
	copy(next, cur)
	next[len(cur)] = b
	p.ptr.Store(&next)
}

func (p *Pool) Remove(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cur := *p.ptr.Load()
	next := make([]*Backend, 0, len(cur))
	for _, b := range cur {
		if b.Addr != addr {
			next = append(next, b)
		}
	}
	p.ptr.Store(&next)
}

func (p *Pool) Backends() []*Backend {
	return *p.ptr.Load()
}

func (p *Pool) Pick(key string) *Backend {
	backends := *p.ptr.Load()
	b := p.balancer.Next(backends, key)
	if b != nil {
		b.ActiveConns.Add(1)
		RecordActiveConns(p.upstream, b.Addr, b.ActiveConns.Load())
	}
	return b
}

func (p *Pool) Done(b *Backend, statusCode int) {
	conns := b.ActiveConns.Add(-1)
	RecordActiveConns(p.upstream, b.Addr, conns)
	if p.breaker != nil {
		p.breaker.Record(p.upstream, b, statusCode)
	}
}

type Registry struct {
	mu    sync.RWMutex
	pools map[string]*Pool
}

func NewRegistry() *Registry {
	return &Registry{pools: make(map[string]*Pool)}
}

func (r *Registry) Register(upstream string, pool *Pool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pools[upstream] = pool
}

func (r *Registry) Pool(upstream string) *Pool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pools[upstream]
}

func (r *Registry) All() map[string]*Pool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]*Pool, len(r.pools))
	for k, v := range r.pools {
		out[k] = v
	}
	return out
}
