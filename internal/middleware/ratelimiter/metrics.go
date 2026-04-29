package ratelimiter

import "github.com/prometheus/client_golang/prometheus"

var (
	hitCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_rate_limit_hits_total",
			Help: "Total rate limit checks.",
		},
		[]string{"route", "scope", "algorithm", "result"},
	)

	remainingGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_rate_limit_remaining",
			Help: "Remaining tokens/requests for current window.",
		},
		[]string{"route", "scope"},
	)
)

func init() {
	prometheus.MustRegister(hitCounter, remainingGauge)
}

func recordMetrics(route, scope, algorithm string, allowed bool, remaining int64) {
	result := "allowed"
	if !allowed {
		result = "rejected"
	}
	hitCounter.WithLabelValues(route, scope, algorithm, result).Inc()
	remainingGauge.WithLabelValues(route, scope).Set(float64(remaining))
}
