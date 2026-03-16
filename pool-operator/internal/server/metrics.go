// pool-operator/internal/server/metrics.go
package server

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricsOnce     sync.Once
	metricsInstance *Metrics
)

// Metrics holds Prometheus metrics for the pool operator HTTP server.
type Metrics struct {
	Available      *prometheus.GaugeVec
	Claimed        *prometheus.GaugeVec
	Warming        *prometheus.GaugeVec
	ClaimTotal     *prometheus.CounterVec
	ClaimDuration  *prometheus.HistogramVec
	ExhaustedTotal *prometheus.CounterVec
}

// NewMetrics creates and registers Prometheus metrics using promauto.
// It is safe to call multiple times; metrics are registered only once.
func NewMetrics() *Metrics {
	metricsOnce.Do(func() {
		metricsInstance = &Metrics{
			Available: promauto.NewGaugeVec(prometheus.GaugeOpts{
				Name: "pool_available",
				Help: "Number of available pods in the pool.",
			}, []string{"pool"}),

			Claimed: promauto.NewGaugeVec(prometheus.GaugeOpts{
				Name: "pool_claimed",
				Help: "Number of claimed pods in the pool.",
			}, []string{"pool"}),

			Warming: promauto.NewGaugeVec(prometheus.GaugeOpts{
				Name: "pool_warming",
				Help: "Number of warming pods in the pool.",
			}, []string{"pool"}),

			ClaimTotal: promauto.NewCounterVec(prometheus.CounterOpts{
				Name: "pool_claim_total",
				Help: "Total number of successful claims.",
			}, []string{"pool"}),

			ClaimDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
				Name:    "pool_claim_duration_seconds",
				Help:    "Time to process a claim request.",
				Buckets: []float64{.0001, .0005, .001, .005, .01, .05, .1},
			}, []string{"pool"}),

			ExhaustedTotal: promauto.NewCounterVec(prometheus.CounterOpts{
				Name: "pool_exhausted_total",
				Help: "Total number of claims that failed due to pool exhaustion.",
			}, []string{"pool"}),
		}
	})
	return metricsInstance
}

// UpdateGauges sets the gauge values for a given pool.
func (m *Metrics) UpdateGauges(poolName string, available, claimed, warming int) {
	m.Available.WithLabelValues(poolName).Set(float64(available))
	m.Claimed.WithLabelValues(poolName).Set(float64(claimed))
	m.Warming.WithLabelValues(poolName).Set(float64(warming))
}
