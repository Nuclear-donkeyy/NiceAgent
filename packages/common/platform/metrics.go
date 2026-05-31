package platform

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Metrics struct {
	service string

	mu           sync.Mutex
	httpRequests map[httpMetricKey]requestMetric
	counters     map[counterMetricKey]float64
	durations    map[durationMetricKey]durationMetric
}

type httpMetricKey struct {
	Method string
	Path   string
	Status string
}

type requestMetric struct {
	Count       uint64
	DurationSum float64
}

type counterMetricKey struct {
	Name   string
	Labels string
}

type durationMetricKey struct {
	Name   string
	Labels string
}

type durationMetric struct {
	Count uint64
	Sum   float64
}

type Labels map[string]string

func NewMetrics(service string) *Metrics {
	return &Metrics{
		service:      service,
		httpRequests: map[httpMetricKey]requestMetric{},
		counters:     map[counterMetricKey]float64{},
		durations:    map[durationMetricKey]durationMetric{},
	}
}

func (m *Metrics) ObserveHTTPRequest(method, path string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	key := httpMetricKey{
		Method: strings.ToUpper(method),
		Path:   safeMetricLabel(path),
		Status: strconv.Itoa(status),
	}
	m.mu.Lock()
	metric := m.httpRequests[key]
	metric.Count++
	metric.DurationSum += duration.Seconds()
	m.httpRequests[key] = metric
	m.mu.Unlock()
}

func (m *Metrics) IncCounter(name string, labels Labels) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.counters[counterMetricKey{Name: sanitizeMetricName(name), Labels: encodeLabels(labels)}]++
	m.mu.Unlock()
}

func (m *Metrics) ObserveDuration(name string, labels Labels, duration time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	key := durationMetricKey{Name: sanitizeMetricName(name), Labels: encodeLabels(labels)}
	metric := m.durations[key]
	metric.Count++
	metric.Sum += duration.Seconds()
	m.durations[key] = metric
	m.mu.Unlock()
}

func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(m.Render()))
	})
}

func (m *Metrics) Render() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var out strings.Builder
	serviceLabel := fmt.Sprintf(`service="%s"`, escapeLabelValue(m.service))
	out.WriteString("# HELP niceagent_http_requests_total Total HTTP requests handled by service.\n")
	out.WriteString("# TYPE niceagent_http_requests_total counter\n")
	httpKeys := make([]httpMetricKey, 0, len(m.httpRequests))
	for key := range m.httpRequests {
		httpKeys = append(httpKeys, key)
	}
	sort.Slice(httpKeys, func(i, j int) bool {
		return fmt.Sprint(httpKeys[i]) < fmt.Sprint(httpKeys[j])
	})
	for _, key := range httpKeys {
		metric := m.httpRequests[key]
		fmt.Fprintf(&out, "niceagent_http_requests_total{%s,method=%q,path=%q,status=%q} %d\n", serviceLabel, key.Method, key.Path, key.Status, metric.Count)
	}
	out.WriteString("# HELP niceagent_http_request_duration_seconds HTTP request duration summary.\n")
	out.WriteString("# TYPE niceagent_http_request_duration_seconds summary\n")
	for _, key := range httpKeys {
		metric := m.httpRequests[key]
		fmt.Fprintf(&out, "niceagent_http_request_duration_seconds_count{%s,method=%q,path=%q,status=%q} %d\n", serviceLabel, key.Method, key.Path, key.Status, metric.Count)
		fmt.Fprintf(&out, "niceagent_http_request_duration_seconds_sum{%s,method=%q,path=%q,status=%q} %.6f\n", serviceLabel, key.Method, key.Path, key.Status, metric.DurationSum)
	}
	out.WriteString("# HELP niceagent_events_total Domain events and operational counters.\n")
	out.WriteString("# TYPE niceagent_events_total counter\n")
	counterKeys := make([]counterMetricKey, 0, len(m.counters))
	for key := range m.counters {
		counterKeys = append(counterKeys, key)
	}
	sort.Slice(counterKeys, func(i, j int) bool {
		return counterKeys[i].Name+counterKeys[i].Labels < counterKeys[j].Name+counterKeys[j].Labels
	})
	for _, key := range counterKeys {
		labels := joinMetricLabels(serviceLabel, key.Labels)
		fmt.Fprintf(&out, "%s{%s} %.0f\n", key.Name, labels, m.counters[key])
	}
	out.WriteString("# HELP niceagent_duration_seconds Operational duration summary.\n")
	out.WriteString("# TYPE niceagent_duration_seconds summary\n")
	durationKeys := make([]durationMetricKey, 0, len(m.durations))
	for key := range m.durations {
		durationKeys = append(durationKeys, key)
	}
	sort.Slice(durationKeys, func(i, j int) bool {
		return durationKeys[i].Name+durationKeys[i].Labels < durationKeys[j].Name+durationKeys[j].Labels
	})
	for _, key := range durationKeys {
		labels := joinMetricLabels(serviceLabel, key.Labels)
		metric := m.durations[key]
		fmt.Fprintf(&out, "%s_count{%s} %d\n", key.Name, labels, metric.Count)
		fmt.Fprintf(&out, "%s_sum{%s} %.6f\n", key.Name, labels, metric.Sum)
	}
	return out.String()
}

func MetricsMiddleware(metrics *Metrics, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &metricResponseWriter{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		path := r.Pattern
		if path == "" {
			path = r.URL.Path
		}
		metrics.ObserveHTTPRequest(r.Method, path, status, time.Since(start))
	})
}

type metricResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *metricResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *metricResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

func (w *metricResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func encodeLabels(labels Labels) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", sanitizeLabelName(key), safeMetricLabel(labels[key])))
	}
	return strings.Join(parts, ",")
}

func joinMetricLabels(serviceLabel, labels string) string {
	if labels == "" {
		return serviceLabel
	}
	return serviceLabel + "," + labels
}

func sanitizeMetricName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "niceagent_events_total"
	}
	var out strings.Builder
	for i, ch := range name {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' || (i > 0 && ch >= '0' && ch <= '9') {
			out.WriteRune(ch)
			continue
		}
		out.WriteByte('_')
	}
	return out.String()
}

func sanitizeLabelName(name string) string {
	name = sanitizeMetricName(name)
	if strings.HasPrefix(name, "niceagent_") {
		return strings.TrimPrefix(name, "niceagent_")
	}
	return name
}

func safeMetricLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	if len(value) > 160 {
		value = value[:160]
	}
	return value
}

func escapeLabelValue(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`)
}
