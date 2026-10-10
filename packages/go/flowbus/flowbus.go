// Package flowbus publica y consume los mensajes NATS JetStream del dominio
// flows según el contrato C4 (docs/events.md §2, §5): telemetría en Protobuf
// con el sobre en cabeceras y eventos de dominio en JSON con el sobre en el
// cuerpo. Es un subconjunto acotado de lo que hará packages/go/natsx; cuando
// exista, flows migrará a él sin cambiar subjects ni cabeceras.
package flowbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Cabeceras NATS del contrato (docs/events.md §5.2).
const (
	HeaderMsgID         = "Nats-Msg-Id"
	HeaderContentType   = "Content-Type"
	HeaderType          = "Horus-Type"
	HeaderSchemaVersion = "Horus-Schema-Version"
	HeaderSource        = "Horus-Source"
	HeaderTime          = "Horus-Time"
	HeaderTenant        = "Horus-Tenant"
	// TenantPlatform es el valor de Horus-Tenant en tipos de plataforma.
	TenantPlatform = "platform"
)

// Tipos y subjects de flows (docs/events.md §8.5; packages/events/catalog/flows.yaml).
const (
	TypeBatchReceived        = "horus.flows.batch.received"
	TypeClientFirstSeen      = "horus.flows.client.first_seen"
	TypeClientActivity       = "horus.flows.client.activity_summary"
	TypeTrafficSummary       = "horus.flows.traffic_summary.observed"
	TypeExporterUnregistered = "horus.flows.exporter.unregistered"
	TypeExporterSilent       = "horus.flows.exporter.silent"
	TypeExporterRecovered    = "horus.flows.exporter.recovered"
	TypeExporterStateChanged = "horus.flows.exporter.state_changed"
	TypeCollectorDataGap     = "horus.flows.collector.data_gap"

	SubjectBatchPrefix    = "horus.telemetry.flows.batch."
	SubjectActivityPrefix = "horus.telemetry.flows.client_activity."
	SubjectSummaryPrefix  = "horus.telemetry.flows.summary."
	StreamTelemetry       = "TLM_FLOWS"
	StreamEvents          = "FLOWS_EVENTS"
	StreamDevices         = "DEVICES_EVENTS"
	ConsumerIngester      = "flows-ingester"
	ExporterStateBucket   = "flow_exporter_state"
	TimeFormat            = "2006-01-02T15:04:05.000Z07:00"
)

// Subject construye el subject de un evento de dominio: <type>.<entity>.
func Subject(typ, entity string) string { return typ + "." + entity }

// Telemetry es un mensaje de telemetría (sobre solo en cabeceras).
type Telemetry struct {
	Subject     string
	Type        string
	Source      string
	TenantID    string // vacío = platform
	MsgID       string // batch_id
	ContentType string
	Time        time.Time
	Body        []byte
}

// Msg construye el mensaje NATS.
func (t Telemetry) Msg() *nats.Msg {
	m := nats.NewMsg(t.Subject)
	m.Data = t.Body
	m.Header.Set(HeaderMsgID, t.MsgID)
	m.Header.Set(HeaderContentType, t.ContentType)
	m.Header.Set(HeaderType, t.Type)
	m.Header.Set(HeaderSchemaVersion, "1")
	m.Header.Set(HeaderSource, t.Source)
	m.Header.Set(HeaderTime, t.Time.UTC().Format(TimeFormat))
	tenant := t.TenantID
	if tenant == "" {
		tenant = TenantPlatform
	}
	m.Header.Set(HeaderTenant, tenant)
	return m
}

// Actor del sobre.
type Actor struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	ViaPlatform bool   `json:"via_platform"`
}

// Envelope es el sobre de un evento de dominio (docs/events.md §5.1).
type Envelope struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Source           string          `json:"source"`
	Subject          string          `json:"subject"`
	Time             string          `json:"time"`
	SchemaVersion    int             `json:"schema_version"`
	TenantID         *string         `json:"tenant_id"`
	AggregateType    string          `json:"aggregate_type"`
	AggregateVersion int             `json:"aggregate_version"`
	Actor            Actor           `json:"actor"`
	TraceParent      *string         `json:"trace_parent"`
	CorrelationID    *string         `json:"correlation_id"`
	CausationID      *string         `json:"causation_id"`
	Data             json.RawMessage `json:"data"`
}

// Event es un evento de dominio a publicar.
type Event struct {
	ID               uuid.UUID // vacío = UUIDv7
	Type             string
	Source           string // horus/flows/<rol>
	Entity           string // 5º token y `subject` del sobre
	TenantID         *uuid.UUID
	AggregateType    string
	AggregateVersion int
	Time             time.Time
	Data             any
}

// Msg construye el mensaje NATS con el sobre JSON y sus cabeceras.
func (e Event) Msg() (*nats.Msg, error) {
	id := e.ID
	if id == uuid.Nil {
		var err error
		if id, err = uuid.NewV7(); err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(e.Data)
	if err != nil {
		return nil, fmt.Errorf("flowbus: %s data: %w", e.Type, err)
	}
	at := e.Time
	if at.IsZero() {
		at = time.Now()
	}
	env := Envelope{
		ID: id.String(), Type: e.Type, Source: e.Source, Subject: e.Entity,
		Time: at.UTC().Format(TimeFormat), SchemaVersion: 1,
		AggregateType: e.AggregateType, AggregateVersion: e.AggregateVersion,
		Actor: Actor{Type: "service", ID: "svc:flows"}, Data: data,
	}
	tenant := TenantPlatform
	if e.TenantID != nil {
		s := e.TenantID.String()
		env.TenantID, tenant = &s, s
	}
	body, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	m := nats.NewMsg(Subject(e.Type, e.Entity))
	m.Data = body
	m.Header.Set(HeaderMsgID, env.ID)
	m.Header.Set(HeaderContentType, "application/json")
	m.Header.Set(HeaderType, e.Type)
	m.Header.Set(HeaderSchemaVersion, strconv.Itoa(env.SchemaVersion))
	m.Header.Set(HeaderSource, e.Source)
	m.Header.Set(HeaderTime, env.Time)
	m.Header.Set(HeaderTenant, tenant)
	return m, nil
}

// Publish publica un evento de dominio con confirmación (docs/events.md §6.3).
func Publish(ctx context.Context, js jetstream.JetStream, e Event) error {
	m, err := e.Msg()
	if err != nil {
		return err
	}
	_, err = js.PublishMsg(ctx, m)
	return err
}

// Connect abre la conexión NATS con reconexión infinita.
func Connect(url, name string, opts ...nats.Option) (*nats.Conn, jetstream.JetStream, error) {
	if url == "" {
		return nil, nil, errors.New("flowbus: HORUS_NATS_URL is empty")
	}
	base := []nats.Option{
		nats.Name(name), nats.MaxReconnects(-1), nats.ReconnectWait(time.Second),
		nats.RetryOnFailedConnect(true), nats.ReconnectBufSize(-1),
	}
	nc, err := nats.Connect(url, append(base, opts...)...)
	if err != nil {
		return nil, nil, fmt.Errorf("flowbus: connect %s: %w", url, err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, nil, err
	}
	return nc, js, nil
}

// EnsureStreams crea (si faltan) los streams y el durable que usa flows con
// los parámetros de packages/events/streams/streams.yaml. En producción los
// aplica el job de infrastructure/nats; esto es para desarrollo y tests.
func EnsureStreams(ctx context.Context, js jetstream.JetStream, tlmMaxBytes int64) error {
	if tlmMaxBytes <= 0 {
		tlmMaxBytes = 50 << 30
	}
	for _, cfg := range []jetstream.StreamConfig{
		{Name: StreamTelemetry, Subjects: []string{"horus.telemetry.flows.>"}, Retention: jetstream.LimitsPolicy,
			MaxAge: 24 * time.Hour, MaxBytes: tlmMaxBytes, Duplicates: 2 * time.Minute, MaxMsgSize: 1 << 20,
			Storage: jetstream.FileStorage, Discard: jetstream.DiscardOld, AllowDirect: true,
			Compression: jetstream.S2Compression},
		{Name: StreamEvents, Subjects: []string{"horus.flows.>"}, Retention: jetstream.LimitsPolicy,
			MaxAge: 30 * 24 * time.Hour, MaxBytes: 1 << 30, Duplicates: 20 * time.Minute, MaxMsgSize: 64 << 10,
			Storage: jetstream.FileStorage, Discard: jetstream.DiscardOld, AllowDirect: true},
	} {
		if _, err := js.Stream(ctx, cfg.Name); err == nil {
			continue
		} else if !errors.Is(err, jetstream.ErrStreamNotFound) {
			return err
		}
		if _, err := js.CreateStream(ctx, cfg); err != nil && !errors.Is(err, jetstream.ErrStreamNameAlreadyInUse) {
			return fmt.Errorf("flowbus: create stream %s: %w", cfg.Name, err)
		}
	}
	return nil
}

// IngesterConsumerConfig es la configuración del durable flows-ingester
// (streams.yaml: provisioned_consumers). MaxDeliver es ilimitado: con
// ClickHouse caído horas, un lote prefetched alcanzaba las 5 entregas y, si el
// ingester se reiniciaba, JetStream ya no lo volvía a entregar (pérdida). Los
// lotes inválidos los termina el propio ingester tras 5 entregas (DLQ).
func IngesterConsumerConfig() jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Durable: ConsumerIngester, FilterSubjects: []string{SubjectBatchPrefix + ">"},
		AckPolicy: jetstream.AckExplicitPolicy, AckWait: 60 * time.Second, MaxDeliver: -1,
		MaxAckPending: 5000, DeliverPolicy: jetstream.DeliverAllPolicy,
		BackOff: []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 5 * time.Minute},
	}
}
