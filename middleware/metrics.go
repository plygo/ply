package middleware

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/plygo/ply/core"
)

var defaultDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

type metricKey struct {
	method string
	status string
}

type metricHistogram struct {
	buckets []uint64
	sum     float64
	count   uint64
}

type Metrics struct {
	mu        sync.Mutex
	requests  map[metricKey]uint64
	durations map[metricKey]*metricHistogram
	buckets   []float64
}

func NewMetrics() *Metrics {
	return &Metrics{
		requests:  make(map[metricKey]uint64),
		durations: make(map[metricKey]*metricHistogram),
		buckets:   append([]float64(nil), defaultDurationBuckets...),
	}
}

func (m *Metrics) Middleware() core.Handler {
	return func(c *core.Context) {
		start := time.Now()
		c.Next()

		status := c.Ctx.Response.StatusCode()
		if status == 0 {
			status = 200
		}
		key := metricKey{
			method: string(c.Ctx.Method()),
			status: strconv.Itoa(status),
		}
		m.observe(key, time.Since(start).Seconds())
	}
}

func (m *Metrics) observe(key metricKey, seconds float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.requests[key]++
	h, ok := m.durations[key]
	if !ok {
		h = &metricHistogram{buckets: make([]uint64, len(m.buckets)+1)}
		m.durations[key] = h
	}

	for i, bucket := range m.buckets {
		if seconds <= bucket {
			h.buckets[i]++
		}
	}
	h.buckets[len(m.buckets)]++
	h.sum += seconds
	h.count++
}

func (m *Metrics) Handler() core.Handler {
	return func(c *core.Context) {
		c.Ctx.Response.Header.Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		c.Ctx.Response.Header.Set("Cache-Control", "no-store")
		c.Ctx.SetBodyString(m.render())
	}
}

func (m *Metrics) render() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	keys := make([]metricKey, 0, len(m.requests))
	for key := range m.requests {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		return keys[i].status < keys[j].status
	})

	var b strings.Builder
	b.WriteString("# HELP ply_http_requests_total Total number of HTTP requests.\n")
	b.WriteString("# TYPE ply_http_requests_total counter\n")
	for _, key := range keys {
		fmt.Fprintf(&b, "ply_http_requests_total{method=%q,status=%q} %d\n",
			key.method, key.status, m.requests[key])
	}

	b.WriteString("# HELP ply_http_request_duration_seconds HTTP request duration in seconds.\n")
	b.WriteString("# TYPE ply_http_request_duration_seconds histogram\n")
	for _, key := range keys {
		h := m.durations[key]
		for i, bucket := range m.buckets {
			fmt.Fprintf(&b, "ply_http_request_duration_seconds_bucket{method=%q,status=%q,le=%q} %d\n",
				key.method, key.status, strconv.FormatFloat(bucket, 'g', -1, 64), h.buckets[i])
		}
		fmt.Fprintf(&b, "ply_http_request_duration_seconds_bucket{method=%q,status=%q,le=%q} %d\n",
			key.method, key.status, "+Inf", h.buckets[len(m.buckets)])
		fmt.Fprintf(&b, "ply_http_request_duration_seconds_sum{method=%q,status=%q} %s\n",
			key.method, key.status, strconv.FormatFloat(h.sum, 'g', -1, 64))
		fmt.Fprintf(&b, "ply_http_request_duration_seconds_count{method=%q,status=%q} %d\n",
			key.method, key.status, h.count)
	}

	return b.String()
}
