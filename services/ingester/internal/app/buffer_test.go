package app

import (
	"context"
	"log/slog"
	"testing"
)

func TestBufferMonitorThreshold(t *testing.T) {
	st := BufferState{Bytes: 10, MaxBytes: 100}
	m := NewBufferMonitor(nil, func(context.Context) (BufferState, error) { return st, nil }, 0.7, slog.New(slog.DiscardHandler))
	m.Poll(context.Background())
	if err := m.Probe(context.Background()); err != nil {
		t.Fatalf("10%%: %v", err)
	}
	st.Bytes = 75
	m.Poll(context.Background())
	if err := m.Probe(context.Background()); err == nil {
		t.Fatal("75% should be degraded")
	}
	st.Bytes = 20
	m.Poll(context.Background())
	if err := m.Probe(context.Background()); err != nil {
		t.Fatalf("back to 20%%: %v", err)
	}
	if (BufferState{Bytes: 5}).Ratio() != 0 {
		t.Fatal("ratio without max_bytes")
	}
}
