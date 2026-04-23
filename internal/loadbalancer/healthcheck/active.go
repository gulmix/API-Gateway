package healthcheck

import (
	"context"
	"net/http"
	"time"

	"github.com/gulmix/apigateway/internal/config"
	lb "github.com/gulmix/apigateway/internal/loadbalancer"
	"go.uber.org/zap"
)

type ActiveChecker struct {
	pool     *lb.Pool
	upstream string
	cfg      config.HealthCheckConfig
	client   *http.Client
	log      *zap.Logger
	stopCh   chan struct{}
}

func NewActiveChecker(pool *lb.Pool, upstream string, cfg config.HealthCheckConfig, log *zap.Logger) *ActiveChecker {
	return &ActiveChecker{
		pool:     pool,
		upstream: upstream,
		cfg:      cfg,
		client:   &http.Client{Timeout: cfg.Timeout},
		log:      log,
		stopCh:   make(chan struct{}),
	}
}

func (ac *ActiveChecker) Start() {
	if !ac.cfg.Enabled {
		return
	}

	interval := ac.cfg.Interval
	if interval == 0 {
		interval = 10 * time.Second
	}

	path := ac.cfg.Path
	if path == "" {
		path = "/health"
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		ac.checkAll(path)
		for {
			select {
			case <-ticker.C:
				ac.checkAll(path)
			case <-ac.stopCh:
				return
			}
		}
	}()
}

func (ac *ActiveChecker) Stop() {
	close(ac.stopCh)
}

func (ac *ActiveChecker) checkAll(path string) {
	for _, b := range ac.pool.Backends() {
		go ac.probe(b, path)
	}
}

func (ac *ActiveChecker) probe(b *lb.Backend, path string) {
	url := "http://" + b.Addr + path
	ctx, cancel := context.WithTimeout(context.Background(), ac.client.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		ac.markDown(b, "bad_request", err)
		return
	}

	resp, err := ac.client.Do(req)
	if err != nil {
		ac.markDown(b, "connection_error", err)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 500 {
		ac.markDown(b, "5xx", nil)
		return
	}

	if !b.IsHealthy() {
		ac.log.Info("backend recovered (active check)",
			zap.String("upstream", ac.upstream),
			zap.String("backend", b.Addr),
		)
	}
	b.SetHealthy(true)
	lb.RecordHealthStatus(ac.upstream, b.Addr, 1)
}

func (ac *ActiveChecker) markDown(b *lb.Backend, reason string, err error) {
	if b.IsHealthy() {
		ac.log.Warn("backend down (active check)",
			zap.String("upstream", ac.upstream),
			zap.String("backend", b.Addr),
			zap.String("reason", reason),
			zap.Error(err),
		)
	}
	b.SetHealthy(false)
	lb.RecordHealthStatus(ac.upstream, b.Addr, 0)
}
