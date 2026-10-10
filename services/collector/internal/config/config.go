// Package config define la configuración del rol collector.
package config

import (
	"errors"
	"runtime"
	"time"
)

// Config del collector. Las variables comunes están en packages/go/config.Common.
type Config struct {
	// Listen son las direcciones UDP de escucha (IPFIX 4739, NetFlow v9 2055);
	// en ambas se aceptan las dos versiones. Vacío = rol inactivo.
	Listen []string `env:"HORUS_COLLECTOR_LISTEN" envSeparator:"," envDefault:":4739,:2055"`
	// NATSURL es el servidor NATS. Vacío en dev = rol inactivo.
	NATSURL string `env:"HORUS_NATS_URL"`
	// EnsureStreams crea TLM_FLOWS/FLOWS_EVENTS si faltan (dev y tests).
	EnsureStreams bool `env:"HORUS_NATS_ENSURE_STREAMS" envDefault:"false"`
	// InventoryFile es el inventario de exportadores y prefijos (YAML,
	// packages/go/flowinv). Se recarga en caliente.
	InventoryFile string `env:"HORUS_FLOWS_INVENTORY_FILE"`
	// CollectorID identifica esta instancia en los lotes.
	CollectorID string `env:"HORUS_COLLECTOR_ID" envDefault:"flows-collector-1"`
	// Workers es el nº de trabajadores de decodificación (por exportador).
	Workers int `env:"HORUS_COLLECTOR_WORKERS" envDefault:"4"`
	// DecodeWorkers son los hilos que decodifican en paralelo los datagramas
	// de un mismo exportador (0 = uno por CPU; 1 = un hilo por exportador,
	// el comportamiento anterior).
	DecodeWorkers int `env:"HORUS_COLLECTOR_DECODE_WORKERS" envDefault:"0"`
	// QueueDatagrams es la cola por trabajador; llena = descarte contado.
	QueueDatagrams int `env:"HORUS_COLLECTOR_QUEUE_DATAGRAMS" envDefault:"8192"`
	// BatchMaxRecords y BatchMaxAge cortan los lotes (events.md §5.5).
	BatchMaxRecords int           `env:"HORUS_COLLECTOR_BATCH_MAX_RECORDS" envDefault:"500"`
	BatchMaxAge     time.Duration `env:"HORUS_COLLECTOR_BATCH_MAX_AGE" envDefault:"1s"`
	// BufferBytes es el búfer en memoria ante caídas del bus (events.md §9.3).
	BufferBytes int64 `env:"HORUS_COLLECTOR_BUFFER_BYTES" envDefault:"268435456"`
	// Spool a disco (docs/architecture.md §10.15): vacío = sin spool. Con el
	// bus caído (o el búfer en memoria por encima de SpoolAfterRatio) los
	// lotes van a segmentos en SpoolDir y se reenvían en orden al volver.
	SpoolDir          string        `env:"HORUS_COLLECTOR_SPOOL_DIR"`
	SpoolBytes        int64         `env:"HORUS_COLLECTOR_SPOOL_BYTES" envDefault:"8589934592"`
	SpoolSegmentBytes int64         `env:"HORUS_COLLECTOR_SPOOL_SEGMENT_BYTES" envDefault:"67108864"`
	SpoolFsync        time.Duration `env:"HORUS_COLLECTOR_SPOOL_FSYNC" envDefault:"1s"`
	SpoolAfterRatio   float64       `env:"HORUS_COLLECTOR_SPOOL_AFTER_RATIO" envDefault:"0.5"`
	// UDPReadBuffer es el búfer de recepción de cada socket UDP; el kernel lo
	// limita a net.core.rmem_max (el instalador lo sube a 32 MiB).
	UDPReadBuffer int `env:"HORUS_COLLECTOR_UDP_RCVBUF" envDefault:"33554432"`
	// PendingTTL es cuánto se retienen datos que llegan antes de su plantilla.
	PendingTTL time.Duration `env:"HORUS_COLLECTOR_PENDING_TTL" envDefault:"30s"`
	// Estado del exportador (I1-09).
	ClockSkew     time.Duration `env:"HORUS_COLLECTOR_CLOCK_SKEW" envDefault:"30s"`
	SilentAfter   time.Duration `env:"HORUS_COLLECTOR_SILENT_AFTER" envDefault:"2m"`
	LossThreshold float64       `env:"HORUS_COLLECTOR_LOSS_THRESHOLD" envDefault:"0.01"`
	LossWindow    time.Duration `env:"HORUS_COLLECTOR_LOSS_WINDOW" envDefault:"5m"`
	StateInterval time.Duration `env:"HORUS_COLLECTOR_STATE_INTERVAL" envDefault:"5s"`
	// TLMMaxBytes es el max_bytes de TLM_FLOWS si EnsureStreams crea el stream.
	TLMMaxBytes int64 `env:"HORUS_TLM_FLOWS_MAX_BYTES" envDefault:"53687091200"`
}

// EffectiveDecodeWorkers resuelve DecodeWorkers (0 = GOMAXPROCS, que en Go
// respeta el límite de CPU del contenedor).
func (c *Config) EffectiveDecodeWorkers() int {
	if c.DecodeWorkers > 0 {
		return c.DecodeWorkers
	}
	return runtime.GOMAXPROCS(0)
}

// Validate implementa config.Validator.
func (c *Config) Validate() error {
	var errs []error
	if c.Workers < 1 {
		errs = append(errs, errors.New("HORUS_COLLECTOR_WORKERS must be >= 1"))
	}
	if c.DecodeWorkers < 0 || c.DecodeWorkers > 256 {
		errs = append(errs, errors.New("HORUS_COLLECTOR_DECODE_WORKERS must be in 0..256"))
	}
	if c.BatchMaxRecords < 1 || c.BatchMaxRecords > 2000 {
		errs = append(errs, errors.New("HORUS_COLLECTOR_BATCH_MAX_RECORDS must be in 1..2000"))
	}
	if c.BatchMaxAge <= 0 || c.SilentAfter <= 0 || c.LossWindow <= 0 || c.StateInterval <= 0 {
		errs = append(errs, errors.New("collector durations must be positive"))
	}
	if c.SpoolDir != "" && (c.SpoolBytes < 1<<20 || c.SpoolSegmentBytes < 1<<16) {
		errs = append(errs, errors.New("HORUS_COLLECTOR_SPOOL_BYTES must be >= 1 MiB and HORUS_COLLECTOR_SPOOL_SEGMENT_BYTES >= 64 KiB"))
	}
	if c.SpoolAfterRatio < 0 || c.SpoolAfterRatio > 1 {
		errs = append(errs, errors.New("HORUS_COLLECTOR_SPOOL_AFTER_RATIO must be in 0..1"))
	}
	return errors.Join(errs...)
}
