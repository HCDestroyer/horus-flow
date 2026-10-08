package httpx

import "github.com/prometheus/client_golang/prometheus"

// Requests expone el contador para los tests.
func (m *Metrics) Requests() *prometheus.CounterVec { return m.requests }
