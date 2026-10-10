package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// BufferState es la ocupación del stream TLM_FLOWS (el búfer ante caídas de
// ClickHouse o del ingester).
type BufferState struct {
	Bytes, MaxBytes uint64
	Msgs            uint64
	// Pending son los lotes que el ingester aún no ha confirmado.
	Pending uint64
}

// BufferMonitor publica la ocupación de TLM_FLOWS y la marca como degradada
// cuando supera WarnRatio de max_bytes (docs/architecture.md §10.1: alerta
// "buffer FLOWS > 70 %"). Pasado max_bytes, JetStream descarta lo más antiguo.
type BufferMonitor struct {
	Read      func(ctx context.Context) (BufferState, error)
	WarnRatio float64
	Log       *slog.Logger

	bytes, ratio prometheus.Gauge

	mu    sync.Mutex
	state BufferState
	over  bool
}

// NewBufferMonitor crea el monitor y registra sus métricas (reg puede ser nil).
func NewBufferMonitor(reg prometheus.Registerer, read func(context.Context) (BufferState, error), warn float64, log *slog.Logger) *BufferMonitor {
	m := &BufferMonitor{Read: read, WarnRatio: warn, Log: log,
		bytes: prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_ingester_tlm_buffer_bytes",
			Help: "Bytes retenidos en TLM_FLOWS (sin comprimir, como cuenta max_bytes)."}),
		ratio: prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_ingester_tlm_buffer_ratio",
			Help: "Ocupación de TLM_FLOWS respecto a max_bytes (0–1)."}),
	}
	if reg != nil {
		_ = reg.Register(m.bytes)
		_ = reg.Register(m.ratio)
	}
	return m
}

// Ratio es la ocupación (0 si max_bytes no está fijado).
func (s BufferState) Ratio() float64 {
	if s.MaxBytes == 0 {
		return 0
	}
	return float64(s.Bytes) / float64(s.MaxBytes)
}

// Poll lee una vez el estado.
func (m *BufferMonitor) Poll(ctx context.Context) {
	st, err := m.Read(ctx)
	if err != nil {
		return
	}
	m.bytes.Set(float64(st.Bytes))
	m.ratio.Set(st.Ratio())
	m.mu.Lock()
	was := m.over
	m.state, m.over = st, m.WarnRatio > 0 && st.Ratio() >= m.WarnRatio
	now := m.over
	m.mu.Unlock()
	switch {
	case now && !was:
		m.Log.Warn("TLM_FLOWS buffer above threshold: ClickHouse or the ingester is not keeping up",
			"bytes", st.Bytes, "max_bytes", st.MaxBytes, "ratio", st.Ratio(), "pending_batches", st.Pending)
	case !now && was:
		m.Log.Info("TLM_FLOWS buffer back below threshold", "ratio", st.Ratio())
	}
}

// Run sondea cada interval hasta que ctx se cancela.
func (m *BufferMonitor) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	m.Poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Poll(ctx)
		}
	}
}

// Probe es la comprobación de salud (no crítica) del búfer.
func (m *BufferMonitor) Probe(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.over {
		return fmt.Errorf("TLM_FLOWS buffer at %.0f%% of max_bytes (%d of %d bytes, %d batches pending)",
			m.state.Ratio()*100, m.state.Bytes, m.state.MaxBytes, m.state.Pending)
	}
	return nil
}
