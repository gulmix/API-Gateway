package discovery

import "github.com/prometheus/client_golang/prometheus"

var (
	backendsRegistered = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_discovery_backends_registered_total",
		Help: "Backends registered via Kubernetes service discovery.",
	}, []string{"upstream", "addr"})

	backendsDeregistered = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_discovery_backends_deregistered_total",
		Help: "Backends deregistered via Kubernetes service discovery.",
	}, []string{"upstream", "addr"})
)

func init() {
	prometheus.MustRegister(backendsRegistered, backendsDeregistered)
}

func RecordRegistered(upstream, addr string) {
	backendsRegistered.WithLabelValues(upstream, addr).Inc()
}

func RecordDeregistered(upstream, addr string) {
	backendsDeregistered.WithLabelValues(upstream, addr).Inc()
}
