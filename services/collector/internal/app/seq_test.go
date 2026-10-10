package app

import "testing"

// TestSeqTrackerExporterRestart: un datagrama reordenado no mueve la
// secuencia, pero un salto atrás grande (el router reinició su secuencia) la
// reinicia y los huecos posteriores se siguen midiendo.
func TestSeqTrackerExporterRestart(t *testing.T) {
	var s seqTracker
	seq := uint32(5_000_000)
	for range 10 {
		if lost := s.observeSeq(true, seq, 10); lost != 0 {
			t.Fatalf("lost %d without gaps", lost)
		}
		seq += 10
	}
	// Reordenado: llega uno de hace 3 datagramas.
	if lost := s.observeSeq(true, seq-30, 10); lost != 0 || s.next != seq {
		t.Fatalf("late datagram moved the sequence (lost %d, next %d)", lost, s.next)
	}
	// El router reinicia: secuencia desde 0.
	if lost := s.observeSeq(true, 0, 10); lost != 0 || s.Resets != 1 || s.next != 10 {
		t.Fatalf("restart: lost %d, resets %d, next %d", lost, s.Resets, s.next)
	}
	// Tras el reinicio, un hueco de 20 registros se mide.
	if lost := s.observeSeq(true, 30, 10); lost != 20 {
		t.Fatalf("gap after restart = %d, want 20", lost)
	}
}
