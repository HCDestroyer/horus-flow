package engine

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ddosFlows construye 4 minutos de subida hacia un único destino con el
// tamaño medio de paquete indicado (bytes y packets ya corregidos por muestreo,
// como los guarda el ingester).
func ddosFlows(k ClientKey, start time.Time, pps float64, avgPacket float64, sampling uint32) []FlowRec {
	var flows []FlowRec
	for m := 0; m < 4; m++ {
		s := start.Add(time.Duration(m) * time.Minute)
		pk := uint64(pps * 60)
		flows = append(flows, FlowRec{Key: k, Up: true, Remote: netip.MustParseAddr("203.0.113.9"), RemotePort: 80, Protocol: 6,
			Start: s, End: s.Add(time.Minute - time.Millisecond), Bytes: uint64(float64(pk) * avgPacket), Packets: pk, Sampling: sampling})
	}
	return flows
}

func runDDoS(t *testing.T, flows []FlowRec, start time.Time) []map[string]any {
	t.Helper()
	k := flows[0].Key
	sig := &sustainedSignals{fakeSignals: fakeSignals{security: []SecurityRow{{Key: k, Site: uuid.New(), UpBytes: 1, UpPackets: 1_000_000}},
		loc: Location{Site: uuid.New(), Router: uuid.New()}}, flows: flows, tz: "UTC"}
	p := smtpOnly()
	p.SMTP.Enabled, p.DDoS.Enabled = false, true
	rep, err := New(sig, &fakeSink{params: p}, nil, Options{}).Evaluate(context.Background(), uuid.New(), start.Add(6*time.Minute), true)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, c := range rep.Candidates {
		out = append(out, c.Evidence)
	}
	return out
}

// Una inundación (paquetes pequeños, muchos pps, un destino) es hallazgo; la
// misma tasa de paquetes con paquetes llenos (copia de seguridad, vídeo) no.
func TestDDoSSmallPacketsOnly(t *testing.T) {
	k := ClientKey{uuid.New(), netip.MustParseAddr("10.20.0.40")}
	start := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	flood := runDDoS(t, ddosFlows(k, start, 3000, 80, 1), start)
	if len(flood) != 1 {
		t.Fatalf("inundación: %d hallazgos", len(flood))
	}
	if flood[0]["avg_packet_bytes"].(float64) != 80 || flood[0]["pps_peak"].(float64) != 3000 {
		t.Fatalf("evidencia = %v", flood[0])
	}
	if backup := runDDoS(t, ddosFlows(k, start, 3000, 1400, 1), start); len(backup) != 0 {
		t.Fatalf("subida con paquetes llenos generó %d hallazgos", len(backup))
	}
}

// Los flujos ya vienen corregidos por muestreo: un sampling_rate de 10 no
// multiplica otra vez los pps (antes inflaba ×10 y disparaba falsas ráfagas).
func TestDDoSSamplingNotDoubleCounted(t *testing.T) {
	k := ClientKey{uuid.New(), netip.MustParseAddr("10.20.0.41")}
	start := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	// 1 500 pps < 2 000: por debajo del umbral aunque el muestreo sea 1:10.
	if got := runDDoS(t, ddosFlows(k, start, 1500, 80, 10), start); len(got) != 0 {
		t.Fatalf("muestreo 1:10 contado dos veces: %d hallazgos (%v)", len(got), got)
	}
	got := runDDoS(t, ddosFlows(k, start, 3000, 80, 10), start)
	if len(got) != 1 || got[0]["pps_peak"].(float64) != 3000 {
		t.Fatalf("pico = %v", got)
	}
}

// withDownload añade, por cada minuto de subida, la bajada que el mismo destino
// devuelve al cliente (bytes por minuto).
func withDownload(flows []FlowRec, downBytesPerMinute uint64) []FlowRec {
	out := append([]FlowRec(nil), flows...)
	for _, f := range flows {
		out = append(out, FlowRec{Key: f.Key, Up: false, Remote: f.Remote, RemotePort: f.RemotePort, Protocol: f.Protocol,
			Start: f.Start, End: f.End, Bytes: downBytesPerMinute, Packets: downBytesPerMinute / 1400, Sampling: f.Sampling})
	}
	return out
}

// Una descarga rápida (500 Mbit/s desde una CDN) hace que el cliente devuelva
// ~20 000 acuses TCP de 60 B por segundo al mismo servidor: vista solo desde la
// subida es idéntica a una inundación. El servidor devuelve ~50 veces más bytes
// de los que recibe, así que no es hallazgo. Una víctima que no responde, sí.
func TestDDoSIgnoresTCPAcksOfDownloads(t *testing.T) {
	k := ClientKey{uuid.New(), netip.MustParseAddr("10.20.0.42")}
	start := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	acks := ddosFlows(k, start, 20000, 60, 1)
	download := uint64(500e6 / 8 * 60) // 500 Mbit/s durante un minuto
	if got := runDDoS(t, withDownload(acks, download), start); len(got) != 0 {
		t.Fatalf("los acuses de una descarga generaron %d hallazgos: %v", len(got), got)
	}
	// La misma subida sin respuesta (o con una respuesta mínima) sigue siendo ráfaga.
	if got := runDDoS(t, acks, start); len(got) != 1 {
		t.Fatalf("inundación sin respuesta: %d hallazgos", len(got))
	}
	upPerMinute := uint64(20000 * 60 * 60)
	if got := runDDoS(t, withDownload(acks, upPerMinute/10), start); len(got) != 1 {
		t.Fatalf("víctima que apenas responde (10 %%): %d hallazgos", len(got))
	}
}
