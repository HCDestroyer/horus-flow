package natsx

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"gopkg.in/yaml.v3"

	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
)

//go:embed streams.v0.yaml
var streamsYAML []byte

// StreamsYAML devuelve la copia embebida de packages/events/streams/streams.yaml
// (un test comprueba que no diverge del contrato).
func StreamsYAML() []byte { return streamsYAML }

// ContractStreams devuelve los streams del contrato C4 embebidos.
func ContractStreams() ([]StreamDef, error) {
	var f struct {
		Streams []StreamDef `yaml:"streams"`
	}
	if err := yaml.Unmarshal(streamsYAML, &f); err != nil {
		return nil, fmt.Errorf("natsx: streams: %w", err)
	}
	return f.Streams, nil
}

// ServiceBus es el nombre del bus compartido en module.Services.
const ServiceBus = "natsx.Bus"

// Bus es la conexión NATS del proceso y su contexto JetStream.
type Bus struct {
	NC *nats.Conn
	JS jetstream.JetStream
	// DurablePrefix (HORUS_NATS_DURABLE_PREFIX) aísla los durables de
	// varias instancias de prueba sobre el mismo servidor; vacío en despliegue.
	DurablePrefix string
}

// Durable devuelve el nombre del durable con el prefijo de la instancia.
func (b *Bus) Durable(name string) string { return b.DurablePrefix + name }

var busMu sync.Mutex

// EnvValue devuelve el último valor de key en environ.
func EnvValue(environ []string, key string) string {
	v := ""
	for _, kv := range environ {
		if k, val, ok := strings.Cut(kv, "="); ok && k == key {
			v = val
		}
	}
	return v
}

// Shared devuelve el bus del proceso (una conexión compartida por todos los
// módulos locales, registrada en services) a partir de HORUS_NATS_URL; nil
// si no está configurado. Con HORUS_NATS_ENSURE_STREAMS=true (dev y tests)
// aplica los streams del contrato.
func Shared(ctx context.Context, services *module.Services, environ []string, logger *slog.Logger) (*Bus, error) {
	busMu.Lock()
	defer busMu.Unlock()
	if b, ok := module.Lookup[*Bus](services, ServiceBus); ok {
		return b, nil
	}
	url := EnvValue(environ, "HORUS_NATS_URL")
	if url == "" {
		return nil, nil
	}
	process := EnvValue(environ, "HORUS_PROCESS")
	if process == "" {
		process = "horus"
	}
	nc, err := Connect(url, process, logger)
	if err != nil {
		return nil, err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("natsx: jetstream: %w", err)
	}
	if EnvValue(environ, "HORUS_NATS_ENSURE_STREAMS") == "true" {
		defs, err := ContractStreams()
		if err != nil {
			nc.Close()
			return nil, err
		}
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = EnsureStreams(sctx, js, ExpandStreams(defs, environ), 0)
		cancel()
		if err != nil {
			nc.Close()
			return nil, err
		}
	}
	b := &Bus{NC: nc, JS: js, DurablePrefix: EnvValue(environ, "HORUS_NATS_DURABLE_PREFIX")}
	if services != nil {
		_ = services.Provide(ServiceBus, b)
	}
	return b, nil
}

// RunRelay ejecuta el relay del outbox de schema hasta que ctx se cancela
// (no hace nada si bus es nil: sin NATS, los eventos esperan en PostgreSQL).
func RunRelay(ctx context.Context, bus *Bus, db *pgdb.DB, schema string, logger *slog.Logger) error {
	if bus == nil || db == nil {
		return nil
	}
	r := &Relay{DB: db, Schema: schema, JS: bus.JS, Logger: logger}
	return r.Run(ctx)
}
