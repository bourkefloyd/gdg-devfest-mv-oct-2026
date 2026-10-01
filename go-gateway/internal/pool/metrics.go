package pool

import "github.com/prometheus/client_golang/prometheus"

var (
	queueWait = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "gemma_queue_wait_seconds",
		Help:    "Time spent waiting for a free Gemma worker slot.",
		Buckets: []float64{.001, .005, .025, .1, .25, .5, 1, 2, 5, 10},
	})
	queueDepth = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "gemma_queue_depth",
		Help: "Callers currently waiting for a Gemma worker slot.",
	})
	admissionRejects = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gemma_admission_rejects_total",
		Help: "Gemma calls rejected by admission control.",
	}, []string{"reason"})
	workerCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gemma_worker_calls_total",
		Help: "Gemma worker generations by worker and outcome.",
	}, []string{"worker", "outcome"})
)

func init() {
	prometheus.MustRegister(queueWait, queueDepth, admissionRejects, workerCalls)
}
