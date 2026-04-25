package metrics

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/libops/triplet/internal/vips"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	registryOnce sync.Once
	registry     *prometheus.Registry

	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "triplet_http_requests_total",
			Help: "Total number of HTTP requests handled by triplet.",
		},
		[]string{"method", "status"},
	)
	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "triplet_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "status"},
	)
	inFlight = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "triplet_http_in_flight_requests",
			Help: "Current number of in-flight HTTP requests.",
		},
	)
)

// Handler returns the Prometheus scrape handler.
func Handler() http.Handler {
	return promhttp.HandlerFor(reg(), promhttp.HandlerOpts{})
}

// Middleware records request counters and duration histograms.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		inFlight.Inc()
		defer inFlight.Dec()

		ww := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(ww, r)

		status := strconv.Itoa(ww.status)
		requestsTotal.WithLabelValues(r.Method, status).Inc()
		requestDuration.WithLabelValues(r.Method, status).Observe(time.Since(start).Seconds())
	})
}

func reg() *prometheus.Registry {
	registryOnce.Do(func() {
		registry = prometheus.NewRegistry()
		registry.MustRegister(
			collectors.NewGoCollector(),
			collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
			requestsTotal,
			requestDuration,
			inFlight,
			vipsCollector{},
		)
	})
	return registry
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

type vipsCollector struct{}

func (vipsCollector) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(vipsCollector{}, ch)
}

func (vipsCollector) Collect(ch chan<- prometheus.Metric) {
	stats := vips.ReadMemStats()
	ch <- prometheus.MustNewConstMetric(
		prometheus.NewDesc("triplet_vips_memory_bytes", "Current libvips tracked memory in bytes.", nil, nil),
		prometheus.GaugeValue,
		float64(stats.Mem),
	)
	ch <- prometheus.MustNewConstMetric(
		prometheus.NewDesc("triplet_vips_memory_highwater_bytes", "High-water libvips tracked memory in bytes.", nil, nil),
		prometheus.GaugeValue,
		float64(stats.MemHigh),
	)
	ch <- prometheus.MustNewConstMetric(
		prometheus.NewDesc("triplet_vips_open_files", "Current libvips tracked open files.", nil, nil),
		prometheus.GaugeValue,
		float64(stats.Files),
	)
	ch <- prometheus.MustNewConstMetric(
		prometheus.NewDesc("triplet_vips_allocations_total", "Total libvips tracked allocations.", nil, nil),
		prometheus.CounterValue,
		float64(stats.Allocs),
	)
}
