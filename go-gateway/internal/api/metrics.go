package api

import "github.com/prometheus/client_golang/prometheus"

var (
	httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "HTTP requests by route and status code.",
	}, []string{"route", "code"})
	httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency by route (SSE excluded).",
		Buckets: []float64{.0005, .001, .0025, .005, .01, .025, .05, .075, .1, .25, .5, 1},
	}, []string{"route"})
	gamesActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "games_active",
		Help: "Games currently in progress.",
	})
	gamesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "games_total",
		Help: "Games created by mode.",
	}, []string{"mode"})
	modelCall = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "model_call_seconds",
		Help:    "Player move latency by backend and outcome.",
		Buckets: []float64{.1, .25, .5, 1, 2, 4, 6, 10, 15, 20, 30},
	}, []string{"backend", "outcome"})
	fallbackTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fallback_total",
		Help: "Player moves served by a fallback backend.",
	}, []string{"seat", "backend"})
	validationRejects = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "validation_rejects_total",
		Help: "Rejected word claims by reason.",
	}, []string{"reason"})
	rateLimited = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "rate_limited_total",
		Help: "Requests rejected by rate limits or caps.",
	}, []string{"limit"})
	pathClaims = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "model_path_claims_total",
		Help: "Model-supplied tile paths by result (correct, wrong, none).",
	}, []string{"result"})
	authFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "auth_failures_total",
		Help: "Requests rejected for missing or invalid API keys.",
	})
)

func init() {
	prometheus.MustRegister(httpRequests, httpDuration, gamesActive, gamesTotal, modelCall,
		fallbackTotal, validationRejects, rateLimited, pathClaims, authFailures)
}
