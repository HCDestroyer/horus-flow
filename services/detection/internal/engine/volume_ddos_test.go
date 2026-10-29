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
	// Una copia de seguridad recibe un acuse TCP de 60 B cada dos segmentos.
	if backup := runDDoS(t, withReturn(ddosFlows(k, start, 3000, 1400, 1), 3000*60/2, 60), start); len(backup) != 0 {
		t.Fatalf("subida con paquetes llenos y acuses generó %d hallazgos", len(backup))
	}
}

// withReturn añade, por cada minuto de subida, los paquetes que el destino
// devuelve (packetsPerMinute de packetBytes cada uno).
func withReturn(flows []FlowRec, packetsPerMinute, packetBytes uint64) []FlowRec {
	out := append([]FlowRec(nil), flows...)
	for _, f := range flows {
		out = append(out, FlowRec{Key: f.Key, Up: false, Remote: f.Remote, RemotePort: f.RemotePort, Protocol: f.Protocol,
			Start: f.Start, End: f.End, Bytes: packetsPerMinute * packetBytes, Packets: packetsPerMinute, Sampling: f.Sampling})
	}
	return out
}

// Una inundación UDP con paquetes llenos (volumétrica: 20 000 pps de 1 000 B,
// como el escenario dos_out) no recibe respuesta: es hallazgo aunque el
// tamaño medio supere max_avg_packet_bytes. Con tráfico de vuelta como el de
// una videollamada (la mitad de paquetes), no.
func TestDDoSLargePacketOneWayFlood(t *testing.T) {
	k := ClientKey{uuid.New(), netip.MustParseAddr("10.20.0.43")}
	start := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	got := runDDoS(t, ddosFlows(k, start, 20000, 1000, 1), start)
	if len(got) != 1 || got[0]["avg_packet_bytes"].(float64) != 1000 {
		t.Fatalf("inundación de paquetes llenos sin respuesta: %v", got)
	}
	if call := runDDoS(t, withReturn(ddosFlows(k, start, 20000, 1000, 1), 20000*60/2, 200), start); len(call) != 0 {
		t.Fatalf("tráfico con vuelta generó %d hallazgos", len(call))
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

func withFlags(flows []FlowRec, flags uint8) []FlowRec {
	out := append([]FlowRec(nil), flows...)
	for i := range out {
		out[i].TCPFlags = flags
	}
	return out
}

// Los acuses TCP de una descarga (paquetes de 60 B, miles por segundo, una
// conexión establecida) no son una ráfaga aunque la bajada no se vea: ruta
// asimétrica, caché de CDN o NAT en otro equipo dejan al destino sin
// "respuesta" visible (max_response_ratio no sirve) y en un solo sentido.
func TestDDoSDownloadAcksWithoutVisibleDownload(t *testing.T) {
	k := ClientKey{uuid.New(), netip.MustParseAddr("10.20.0.44")}
	start := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	acks := withFlags(ddosFlows(k, start, 4000, 60, 1), 0x10|0x08) // ACK|PSH
	if got := runDDoS(t, acks, start); len(got) != 0 {
		t.Fatalf("acuses de una descarga generaron %d hallazgos: %v", len(got), got)
	}
	// Con muestreo 1:100 la conexión sigue siendo larga (2 400 paquetes observados por minuto).
	sampled := withFlags(ddosFlows(k, start, 4000, 60, 100), 0x10)
	if got := runDDoS(t, sampled, start); len(got) != 0 {
		t.Fatalf("acuses muestreados generaron %d hallazgos", len(got))
	}
}

// Una inundación SYN (sin ACK) sigue siendo hallazgo.
func TestDDoSSynFloodStillDetected(t *testing.T) {
	k := ClientKey{uuid.New(), netip.MustParseAddr("10.20.0.45")}
	start := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	if got := runDDoS(t, withFlags(ddosFlows(k, start, 4000, 60, 1), 0x02), start); len(got) != 1 {
		t.Fatalf("inundación SYN: %d hallazgos, se esperaba 1", len(got))
	}
}

// Una inundación de ACK con puertos aleatorios deja registros de pocos paquetes
// (aquí 10 por registro): no es una conexión establecida y sigue siendo hallazgo.
// Con muestreo fuerte, un registro de un paquete observado tampoco cuenta como
// sesión aunque el muestreo lo multiplique.
func TestDDoSAckFloodShortRecordsStillDetected(t *testing.T) {
	k := ClientKey{uuid.New(), netip.MustParseAddr("10.20.0.46")}
	start := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	remote := netip.MustParseAddr("203.0.113.9")
	var flows []FlowRec
	for s := 0; s < 4*60; s++ {
		ts := start.Add(time.Duration(s) * time.Second)
		for i := 0; i < 300; i++ { // 300 registros/s × 10 paquetes = 3 000 pps
			flows = append(flows, FlowRec{Key: k, Up: true, Remote: remote, RemotePort: 443, Protocol: 6, TCPFlags: 0x10,
				Start: ts, End: ts.Add(500 * time.Millisecond), Bytes: 10 * 60, Packets: 10, Sampling: 1})
		}
	}
	if got := runDDoS(t, flows, start); len(got) != 1 {
		t.Fatalf("inundación ACK de registros cortos: %d hallazgos, se esperaba 1", len(got))
	}
	sampled := withFlags(ddosFlows(k, start, 4000, 60, 1), 0x10)
	for i := range sampled { // un paquete observado por registro, multiplicado por 1:240 000
		sampled[i].Sampling = uint32(sampled[i].Packets)
	}
	if got := runDDoS(t, sampled, start); len(got) != 1 {
		t.Fatalf("registros de un paquete muestreado: %d hallazgos, se esperaba 1", len(got))
	}
}
