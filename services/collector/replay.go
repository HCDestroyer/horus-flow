package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	dto "github.com/prometheus/client_model/go"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/pcapread"
	"github.com/hcdestroyer/horus-flow/services/collector/internal/app"
	modcfg "github.com/hcdestroyer/horus-flow/services/collector/internal/config"
)

// ReplayOptions ajusta Replay.
type ReplayOptions struct {
	// Transform modifica cada mensaje antes de publicarlo (p. ej. trasladar
	// en el tiempo un fixture antiguo); nil = sin cambios.
	Transform func(*nats.Msg) *nats.Msg
	Logger    *slog.Logger
}

// ReplayStats resume una reproducción.
type ReplayStats struct {
	Datagrams    int
	Records      int
	SequenceGaps uint64
	LostRecords  uint64
}

type replaySink struct {
	js jetstream.JetStream
	tr func(*nats.Msg) *nats.Msg
}

func (s replaySink) PublishMsg(ctx context.Context, m *nats.Msg) error {
	if s.tr != nil {
		m = s.tr(m)
	}
	_, err := s.js.PublishMsg(ctx, m)
	return err
}

// Replay pasa datagramas capturados (pcap, pcapng o hfsim) por el motor del
// collector conservando su IP de origen, con la configuración HORUS_* de
// environ (HORUS_NATS_URL, HORUS_FLOWS_INVENTORY_FILE…), y publica los lotes
// en TLM_FLOWS. Sirve para el test dorado con la captura real, el
// laboratorio y los fixtures del simulador: un socket UDP no puede emitir
// desde la IP de túnel del router original.
func Replay(ctx context.Context, environ []string, datagrams []pcapread.Datagram, o ReplayOptions) (ReplayStats, error) {
	var st ReplayStats
	cfg, err := config.Load[modcfg.Config](environ)
	if err != nil {
		return st, err
	}
	if cfg.NATSURL == "" || cfg.InventoryFile == "" {
		return st, errors.New("collector replay: HORUS_NATS_URL and HORUS_FLOWS_INVENTORY_FILE are required")
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	snap, err := flowinv.LoadFile(cfg.InventoryFile)
	if err != nil {
		return st, fmt.Errorf("collector replay: inventory: %w", err)
	}
	nc, js, err := flowbus.Connect(cfg.NATSURL, "horus-collector-replay")
	if err != nil {
		return st, err
	}
	defer nc.Close()
	if cfg.EnsureStreams {
		if err := flowbus.EnsureStreams(ctx, js, cfg.TLMMaxBytes); err != nil {
			return st, err
		}
	}
	m := app.NewMetrics(nil)
	eng := app.NewEngine(app.EngineOptions{Workers: cfg.Workers, QueueDatagrams: max(cfg.QueueDatagrams, len(datagrams)),
		BatchMaxRecords: cfg.BatchMaxRecords, BatchMaxAge: cfg.BatchMaxAge, BufferBytes: cfg.BufferBytes,
		PendingTTL: cfg.PendingTTL, CollectorID: cfg.CollectorID + "-replay", DecodeWorkers: cfg.EffectiveDecodeWorkers(),
		State: app.StateOptions{SilentAfter: cfg.SilentAfter, LossThreshold: cfg.LossThreshold, LossWindow: cfg.LossWindow,
			ClockSkew: cfg.ClockSkew, Interval: time.Hour}},
		flowinv.NewStore(snap), replaySink{js: js, tr: o.Transform}, nil, m, log)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- eng.Run(runCtx) }()
	for _, d := range datagrams {
		eng.Submit(app.Datagram{Src: d.Src, Payload: d.Payload, At: time.Now()})
	}
	// Run vacía colas y lotes abiertos al cancelar.
	time.Sleep(100 * time.Millisecond)
	stop()
	if err := <-done; err != nil {
		return st, err
	}
	if err := nc.Flush(); err != nil {
		return st, err
	}
	st.Datagrams = len(datagrams)
	eng.States.Evaluate(ctx, false)
	for _, ex := range eng.States.Snapshot() {
		st.SequenceGaps += ex.SequenceGaps
		st.LostRecords += ex.LostRecords
	}
	var dm dto.Metric
	if err := m.Records.Write(&dm); err == nil {
		st.Records = int(dm.GetCounter().GetValue())
	}
	return st, nil
}
