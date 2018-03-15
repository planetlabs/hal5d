// Package metrics contains utilities to assist with exposing Prometheus
// metrics.
package metrics

import "github.com/prometheus/client_golang/prometheus"

// CounterVec is a a subset of the functionality of a prometheus.CounterVec.
type CounterVec interface {
	// With returns a counter with the supplied labels.
	With(prometheus.Labels) prometheus.Counter
}

// A NopCounter is a no-op implementation of a Prometheus counter.
type NopCounter struct {
	prometheus.Counter
}

// Inc does nothing.
func (c *NopCounter) Inc() {
	return
}

// Add does nothing.
func (c *NopCounter) Add(_ float64) {
	return
}

// A NopCounterVec is a no-op implementation of CounterVec.
type NopCounterVec struct {
	CounterVec
}

// With returns its underlying CounterVec.
func (v *NopCounterVec) With(_ prometheus.Labels) prometheus.Counter {
	return &NopCounter{}
}
