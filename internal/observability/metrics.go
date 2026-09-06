// Package observability owns process-local Prometheus registries. Each
// executable gets its own registry so API and worker metrics do not collide
// when they run in the same test process.
package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// APIMetrics is the bounded-cardinality HTTP telemetry emitted by the public
// API. Callers must use a route template (for example
// /v1/projects/{projectID}), never a raw request path containing tenant IDs.
type APIMetrics struct {
	Registry        *prometheus.Registry
	Requests        *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	ResponseBytes   *prometheus.CounterVec
	InFlight        prometheus.Gauge
}

// NewAPIMetrics constructs an isolated registry instead of using the global
// Prometheus registry. That makes the API safe to embed in tests and keeps
// process metrics correctly scoped to this executable.
func NewAPIMetrics() *APIMetrics {
	registry := prometheus.NewRegistry()
	metrics := &APIMetrics{
		Registry: registry,
		Requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stealth",
			Subsystem: "api",
			Name:      "http_requests_total",
			Help:      "Completed HTTP requests handled by the Stealth API.",
		}, []string{"method", "route", "status"}),
		RequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "stealth",
			Subsystem: "api",
			Name:      "http_request_duration_seconds",
			Help:      "End-to-end duration of HTTP requests handled by the Stealth API.",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300},
		}, []string{"method", "route"}),
		ResponseBytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stealth",
			Subsystem: "api",
			Name:      "http_response_bytes_total",
			Help:      "Response body bytes written by the Stealth API.",
		}, []string{"method", "route", "status"}),
		InFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "stealth",
			Subsystem: "api",
			Name:      "http_requests_in_flight",
			Help:      "HTTP requests currently being handled by the Stealth API.",
		}),
	}
	registry.MustRegister(metrics.Requests, metrics.RequestDuration, metrics.ResponseBytes, metrics.InFlight, prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	return metrics
}

// Handler exposes the registry using the Prometheus text format. The caller
// is responsible for network isolation; it intentionally has no application
// session requirement so Prometheus can scrape it.
func (m *APIMetrics) Handler() http.Handler {
	if m == nil || m.Registry == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

// WorkerMetrics covers queue activity rather than individual executions.
// Labels deliberately contain only a small fixed vocabulary so one tenant or
// function cannot create unbounded Prometheus time series.
type WorkerMetrics struct {
	Registry           *prometheus.Registry
	Polls              prometheus.Counter
	JobsClaimed        prometheus.Counter
	JobsCompleted      *prometheus.CounterVec
	JobDuration        *prometheus.HistogramVec
	Requeued           prometheus.Counter
	Errors             *prometheus.CounterVec
	InFlight           prometheus.Gauge
	BuildsClaimed      prometheus.Counter
	BuildsCompleted    *prometheus.CounterVec
	BuildDuration      *prometheus.HistogramVec
	BuildRequeued      prometheus.Counter
	BuildInFlight      prometheus.Gauge
	AgentPolls         prometheus.Counter
	AgentJobsClaimed   prometheus.Counter
	AgentJobsCompleted *prometheus.CounterVec
	AgentJobDuration   *prometheus.HistogramVec
	AgentRequeued      prometheus.Counter
	AgentErrors        *prometheus.CounterVec
	AgentInFlight      prometheus.Gauge
}

// NewWorkerMetrics constructs a separate worker registry. It can be served
// on a private listener even though the API's public listener is reachable by
// application clients.
func NewWorkerMetrics() *WorkerMetrics {
	registry := prometheus.NewRegistry()
	metrics := &WorkerMetrics{
		Registry: registry,
		Polls: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "stealth",
			Subsystem: "functions_worker",
			Name:      "polls_total",
			Help:      "Queue poll cycles performed by the Functions worker.",
		}),
		JobsClaimed: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "stealth",
			Subsystem: "functions_worker",
			Name:      "jobs_claimed_total",
			Help:      "Function execution jobs claimed by the worker.",
		}),
		JobsCompleted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stealth",
			Subsystem: "functions_worker",
			Name:      "jobs_completed_total",
			Help:      "Function execution jobs transitioned to a terminal result by the worker.",
		}, []string{"result"}),
		JobDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "stealth",
			Subsystem: "functions_worker",
			Name:      "job_duration_seconds",
			Help:      "Time spent processing a claimed Function execution job.",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		}, []string{"result"}),
		Requeued: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "stealth",
			Subsystem: "functions_worker",
			Name:      "stale_jobs_requeued_total",
			Help:      "Previously leased Function jobs returned to the queue after their lease expired.",
		}),
		Errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stealth",
			Subsystem: "functions_worker",
			Name:      "errors_total",
			Help:      "Worker errors grouped by a fixed internal operation name.",
		}, []string{"operation"}),
		InFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "stealth",
			Subsystem: "functions_worker",
			Name:      "jobs_in_flight",
			Help:      "Function execution jobs currently being processed by this worker.",
		}),
		BuildsClaimed: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "stealth",
			Subsystem: "functions_worker",
			Name:      "builds_claimed_total",
			Help:      "Function deployment builds claimed by the worker.",
		}),
		BuildsCompleted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stealth",
			Subsystem: "functions_worker",
			Name:      "builds_completed_total",
			Help:      "Function deployment builds transitioned to a terminal result.",
		}, []string{"result"}),
		BuildDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "stealth",
			Subsystem: "functions_worker",
			Name:      "build_duration_seconds",
			Help:      "Time spent building one immutable Function deployment artifact.",
			Buckets:   []float64{0.01, 0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 900},
		}, []string{"result"}),
		BuildRequeued: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "stealth",
			Subsystem: "functions_worker",
			Name:      "stale_builds_requeued_total",
			Help:      "Previously leased Function builds returned to the queue after their lease expired.",
		}),
		BuildInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "stealth",
			Subsystem: "functions_worker",
			Name:      "builds_in_flight",
			Help:      "Function deployment builds currently being processed by this worker.",
		}),
		AgentPolls: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "stealth",
			Subsystem: "agent_worker",
			Name:      "polls_total",
			Help:      "Queue poll cycles performed by the Agent worker.",
		}),
		AgentJobsClaimed: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "stealth",
			Subsystem: "agent_worker",
			Name:      "jobs_claimed_total",
			Help:      "Agent runs claimed by a trusted provider worker.",
		}),
		AgentJobsCompleted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stealth",
			Subsystem: "agent_worker",
			Name:      "jobs_completed_total",
			Help:      "Agent runs transitioned to a terminal result by a trusted worker.",
		}, []string{"result"}),
		AgentJobDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "stealth",
			Subsystem: "agent_worker",
			Name:      "job_duration_seconds",
			Help:      "Time spent processing one claimed Agent run.",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		}, []string{"result"}),
		AgentRequeued: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "stealth",
			Subsystem: "agent_worker",
			Name:      "stale_jobs_requeued_total",
			Help:      "Previously leased Agent runs returned to the queue after their lease expired.",
		}),
		AgentErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stealth",
			Subsystem: "agent_worker",
			Name:      "errors_total",
			Help:      "Agent worker errors grouped by a fixed internal operation name.",
		}, []string{"operation"}),
		AgentInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "stealth",
			Subsystem: "agent_worker",
			Name:      "jobs_in_flight",
			Help:      "Agent runs currently being processed by this worker.",
		}),
	}
	registry.MustRegister(metrics.Polls, metrics.JobsClaimed, metrics.JobsCompleted, metrics.JobDuration, metrics.Requeued, metrics.Errors, metrics.InFlight, metrics.BuildsClaimed, metrics.BuildsCompleted, metrics.BuildDuration, metrics.BuildRequeued, metrics.BuildInFlight, metrics.AgentPolls, metrics.AgentJobsClaimed, metrics.AgentJobsCompleted, metrics.AgentJobDuration, metrics.AgentRequeued, metrics.AgentErrors, metrics.AgentInFlight, prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	return metrics
}

// Handler exposes the process-local worker registry for a private Prometheus
// scrape target.
func (m *WorkerMetrics) Handler() http.Handler {
	if m == nil || m.Registry == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
}
