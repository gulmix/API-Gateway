package cache

import "github.com/prometheus/client_golang/prometheus"

var (
	hitsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_cache_hits_total",
			Help: "Total cache hits by layer and route.",
		},
		[]string{"layer", "route"},
	)

	missesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_cache_misses_total",
			Help: "Total cache misses by route.",
		},
		[]string{"route"},
	)
)

func init() {
	prometheus.MustRegister(hitsTotal, missesTotal)
}

func recordHit(layer, route string) {
	hitsTotal.WithLabelValues(layer, route).Inc()
}

func recordMisses(route string) {
	missesTotal.WithLabelValues(route).Inc()
}
