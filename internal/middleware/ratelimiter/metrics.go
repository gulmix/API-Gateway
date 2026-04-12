package ratelimiter

import "github.com/prometheus/client_golang/prometheus"

var (
	hitCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "gateway",
			Subsystem: "rate_limit",
			Name:      "hits_total",
			Help:      "Total number of rate limit checks.",
		},
		[]string{"route", "scope", "result"},
	)

	remainingGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "gateway",
			Subsystem: "rate_limit",
			Name:      "remaining",
			Help:      "Remaining requests/tokens for the current window",
		},
		[]string{"route", "scope"},
	)
)

func init() {
	prometheus.MustRegister(hitCounter, remainingGauge)
}

func recordMetrics(route, scope string, allowed bool, remaining int64) {
	result := "allowed"
	if !allowed {
		result = "rejected"
	}
	hitCounter.WithLabelValues(route, scope, result).Inc()
	remainingGauge.WithLabelValues(route, scope).Set(float64(remaining))
}
