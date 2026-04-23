package healthcheck

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/gulmix/apigateway/internal/config"
	lb "github.com/gulmix/apigateway/internal/loadbalancer"
	"go.uber.org/zap"
)

type cbState int32

const (
	stateClosed   cbState = 0
	stateOpen     cbState = 1
	stateHalfOpen cbState = 2
)

type breakerEntry struct {
	mu             sync.Mutex
	state          cbState
	window         *slidingWindow
	openedAt       time.Time
	halfOpenTrials atomic.Int32
}

type PassiveBreaker struct {
	cfg      config.CircuitBreakerConfig
	backends sync.Map
	log      *zap.Logger
}

func NewPassiveBreaker(cfg config.CircuitBreakerConfig, log *zap.Logger) *PassiveBreaker {
	size := cfg.WindowSize
	if size == 0 {
		size = 100
	}
	if cfg.Threshold == 0 {
		cfg.Threshold = 0.5
	}
	if cfg.HalfOpenMax == 0 {
		cfg.HalfOpenMax = 5
	}
	if cfg.RecoveryTime == 0 {
		cfg.RecoveryTime = 30 * time.Second
	}
	cfg.WindowSize = size
	return &PassiveBreaker{cfg: cfg, log: log}
}

func (pb *PassiveBreaker) Record(upstream string, b *lb.Backend, statusCode int) {
	entry := pb.entry(b.Addr)
	success := statusCode < 500

	entry.mu.Lock()
	defer entry.mu.Unlock()

	switch entry.state {
	case stateClosed:
		failureRate := entry.window.record(success)
		if failureRate >= pb.cfg.Threshold {
			pb.trip(upstream, b, entry)
		}
	case stateOpen:
		if time.Since(entry.openedAt) >= pb.cfg.RecoveryTime {
			pb.halfOpen(upstream, b, entry)
			pb.recordProbe(upstream, b, entry, success)
		}
	case stateHalfOpen:
		pb.recordProbe(upstream, b, entry, success)
	}

	lb.RecordCircuitState(upstream, b.Addr, int(entry.state))
}

func (pb *PassiveBreaker) recordProbe(upstream string, b *lb.Backend, entry *breakerEntry, success bool) {
	if !success {
		pb.trip(upstream, b, entry)
		return
	}

	trials := entry.halfOpenTrials.Add(1)
	if int(trials) >= pb.cfg.HalfOpenMax {
		pb.close(upstream, b, entry)
	}
}

func (pb *PassiveBreaker) trip(upstream string, b *lb.Backend, entry *breakerEntry) {
	entry.state = stateOpen
	entry.openedAt = time.Now()
	b.SetHealthy(false)
	lb.RecordHealthStatus(upstream, b.Addr, 0)
	pb.log.Warn("circuit breaker opened",
		zap.String("upstream", upstream),
		zap.String("backend", b.Addr),
	)
}

func (pb *PassiveBreaker) halfOpen(upstream string, b *lb.Backend, entry *breakerEntry) {
	entry.state = stateHalfOpen
	entry.halfOpenTrials.Store(0)
	b.SetHealthy(true)
	pb.log.Info("circuit breaker half-open",
		zap.String("upstream", upstream),
		zap.String("backend", b.Addr),
	)
}

func (pb *PassiveBreaker) close(upstream string, b *lb.Backend, entry *breakerEntry) {
	entry.state = stateClosed
	entry.window.reset()
	b.SetHealthy(true)
	lb.RecordHealthStatus(upstream, b.Addr, 1)
	pb.log.Info("circuit breaker closed (recovered)",
		zap.String("upstream", upstream),
		zap.String("backend", b.Addr),
	)
}

func (pb *PassiveBreaker) entry(addr string) *breakerEntry {
	if v, ok := pb.backends.Load(addr); ok {
		return v.(*breakerEntry)
	}
	e := &breakerEntry{
		state:  stateClosed,
		window: newSlidingWindow(pb.cfg.WindowSize),
	}
	actual, _ := pb.backends.LoadOrStore(addr, e)
	return actual.(*breakerEntry)
}

type slidingWindow struct {
	results  []bool
	size     int
	pos      int
	count    int
	failures int
}

func newSlidingWindow(size int) *slidingWindow {
	return &slidingWindow{results: make([]bool, size), size: size}
}

func (w *slidingWindow) record(success bool) float64 {
	if w.count == w.size {
		if !w.results[w.pos] {
			w.failures--
		}
	} else {
		w.count++
	}
	w.results[w.pos] = success
	if !success {
		w.failures++
	}
	w.pos = (w.pos + 1) % w.size
	return float64(w.failures) / float64(w.count)
}

func (w *slidingWindow) reset() {
	w.pos = 0
	w.count = 0
	w.failures = 0
}
