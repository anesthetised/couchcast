// Package metrics owns the Prometheus registry and the collectors shared by
// the web server and the ingest worker. Each process creates one Metrics
// value and passes it to the components that need to record something.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the registry and every collector registered on it.
type Metrics struct {
	registry *prometheus.Registry

	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec

	ingestJobs     *prometheus.CounterVec
	ingestStep     *prometheus.HistogramVec
	mediaProxyByte prometheus.Counter
}

// New creates a registry with process/Go collectors and the application
// collectors registered. The process label distinguishes web from ingest
// when both scrape into the same Prometheus.
func New(process string) *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	labels := prometheus.Labels{"process": process}

	m := &Metrics{
		registry: reg,
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace:   "couchcast",
			Subsystem:   "http",
			Name:        "requests_total",
			Help:        "HTTP requests by route pattern, method and status code.",
			ConstLabels: labels,
		}, []string{"route", "method", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace:   "couchcast",
			Subsystem:   "http",
			Name:        "request_duration_seconds",
			Help:        "HTTP request latency by route pattern and method.",
			ConstLabels: labels,
			Buckets:     prometheus.DefBuckets,
		}, []string{"route", "method"}),
	}
	m.ingestJobs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "couchcast", Subsystem: "ingest", Name: "jobs_total",
		Help: "Ingest jobs by final result.", ConstLabels: labels,
	}, []string{"result"})
	m.ingestStep = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "couchcast", Subsystem: "ingest", Name: "step_duration_seconds",
		Help: "Duration of each ingest step.", ConstLabels: labels,
		Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800},
	}, []string{"step"})
	m.mediaProxyByte = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "couchcast", Subsystem: "media", Name: "proxied_bytes_total",
		Help: "Bytes served from object storage to viewers.", ConstLabels: labels,
	})
	reg.MustRegister(m.httpRequests, m.httpDuration, m.ingestJobs, m.ingestStep, m.mediaProxyByte)

	return m
}

// IngestJob counts a finished job; result is "done" or "failed". Safe on
// a nil receiver so components can run without metrics in tests.
func (m *Metrics) IngestJob(result string) {
	if m != nil {
		m.ingestJobs.WithLabelValues(result).Inc()
	}
}

// IngestStep records how long one pipeline step took.
func (m *Metrics) IngestStep(step string, d time.Duration) {
	if m != nil {
		m.ingestStep.WithLabelValues(step).Observe(d.Seconds())
	}
}

// MediaProxied adds bytes served by the media proxy.
func (m *Metrics) MediaProxied(n int64) {
	if m != nil {
		m.mediaProxyByte.Add(float64(n))
	}
}

// Registry exposes the underlying registry for components that register
// their own collectors.
func (m *Metrics) Registry() prometheus.Registerer { return m.registry }

// Handler serves the registry in the Prometheus exposition format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// HTTPMiddleware records request counts and latency keyed by the chi route
// pattern, so that /rooms/{slug} does not explode into one series per slug.
func (m *Metrics) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}

		m.httpRequests.WithLabelValues(route, r.Method, strconv.Itoa(rec.status)).Inc()
		m.httpDuration.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer for
// flushing and hijacking (needed by WebSocket upgrades).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
