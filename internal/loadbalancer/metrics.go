package loadbalancer

import "github.com/prometheus/client_golang/prometheus"

var (
	activeConnsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_upstream_active_connections",
			Help: "Current active connections per backend.",
		},
		[]string{"upstream", "backend"},
	)

	healthStatusGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_upstream_health",
			Help: "Backend health status: 1=up, 0=down.",
		},
		[]string{"upstream", "backend"},
	)

	circuitStateGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_circuit_breaker_state",
			Help: "Circuit breaker state: 0=closed, 1=open, 2=half_open.",
		},
		[]string{"upstream", "backend"},
	)

	lbRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_lb_requests_total",
			Help: "Total requests routed by the load balancer.",
		},
		[]string{"upstream", "backend", "result"},
	)
)

func init() {
	prometheus.MustRegister(
		activeConnsGauge,
		healthStatusGauge,
		circuitStateGauge,
		lbRequestsTotal,
	)
}

func RecordActiveConns(upstream, backend string, v int64) {
	activeConnsGauge.WithLabelValues(upstream, backend).Set(float64(v))
}

func RecordHealthStatus(upstream, backend string, v int) {
	healthStatusGauge.WithLabelValues(upstream, backend).Set(float64(v))
}

func RecordCircuitState(upstream, backend string, state int) {
	circuitStateGauge.WithLabelValues(upstream, backend).Set(float64(state))
}

func RecordLBRequest(upstream, backend, result string) {
	lbRequestsTotal.WithLabelValues(upstream, backend, result).Inc()
}
