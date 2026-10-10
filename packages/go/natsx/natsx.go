// Package natsx es la librería común de NATS JetStream de los módulos
// (docs/events.md §2.4, §4, §6; ADR-0006, ADR-0016, ADR-0027):
//
//   - [Connect] abre la conexión (reconexión indefinida);
//   - [Relay] publica el transactional outbox de un esquema PostgreSQL con
//     `Nats-Msg-Id` = id del evento y la cabecera `Horus-Tenant`;
//   - [Consume] ejecuta un consumidor durable pull con validación del tenant
//     (cabecera `Horus-Tenant` obligatoria e igual al `tenant_id` del sobre),
//     reintentos con backoff y dead-letter en `horus.dlq.<servicio>.<durable>`;
//   - [Envelope] es el sobre de los eventos de dominio (C4).
//
// El tenant nunca va en el subject (ADR-0027): viaja en el sobre y en la
// cabecera; el consumidor recibe el tenant ya validado en [Message.Tenant].
package natsx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

// Cabeceras del contrato (docs/events.md §5.2).
const (
	HeaderMsgID         = "Nats-Msg-Id"
	HeaderTenant        = "Horus-Tenant"
	HeaderType          = "Horus-Type"
	HeaderSchemaVersion = "Horus-Schema-Version"
	HeaderSource        = "Horus-Source"
	HeaderTime          = "Horus-Time"
	HeaderContentType   = "Content-Type"
	// TenantPlatform es el valor de Horus-Tenant de los tipos de plataforma.
	TenantPlatform = "platform"
)

// TimeFormat es el formato de instantes del contrato (UTC, ms, Z).
const TimeFormat = "2006-01-02T15:04:05.000Z07:00"

// Connect abre una conexión NATS con reconexión indefinida. name identifica
// el proceso en el servidor (`horus-app/devices`).
func Connect(url, name string, logger *slog.Logger) (*nats.Conn, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	nc, err := nats.Connect(url,
		nats.Name(name),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
		nats.RetryOnFailedConnect(true),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				logger.Warn("nats disconnected", slog.Any("error", err))
			}
		}),
		nats.ReconnectHandler(func(c *nats.Conn) { logger.Info("nats reconnected", slog.String("url", c.ConnectedUrlRedacted())) }),
	)
	if err != nil {
		return nil, fmt.Errorf("natsx: connect: %w", err)
	}
	return nc, nil
}

// Actor del sobre.
type Actor struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	SID         string `json:"sid,omitempty"`
	Via         string `json:"via,omitempty"`
	ViaPlatform bool   `json:"via_platform"`
}

// Envelope es el sobre de un evento de dominio (packages/events/v0/envelope.schema.json).
type Envelope struct {
	ID               uuid.UUID       `json:"id"`
	Type             string          `json:"type"`
	Source           string          `json:"source"`
	Subject          string          `json:"subject"`
	Time             time.Time       `json:"time"`
	SchemaVersion    int             `json:"schema_version"`
	TenantID         *uuid.UUID      `json:"tenant_id"`
	AggregateType    string          `json:"aggregate_type"`
	AggregateVersion int             `json:"aggregate_version"`
	Actor            *Actor          `json:"actor,omitempty"`
	TraceParent      *string         `json:"trace_parent,omitempty"`
	CorrelationID    *string         `json:"correlation_id,omitempty"`
	CausationID      *string         `json:"causation_id,omitempty"`
	Data             json.RawMessage `json:"data"`
}

// DecodeEnvelope interpreta un sobre JSON.
func DecodeEnvelope(b []byte) (*Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("natsx: envelope: %w", err)
	}
	if e.ID == uuid.Nil || e.Type == "" {
		return nil, errors.New("natsx: envelope without id or type")
	}
	return &e, nil
}

// Publish publica un sobre de dominio en `<type>.<subject>` con las
// cabeceras del contrato y espera el PubAck (lo usan tests y productores sin
// PostgreSQL; los módulos con PostgreSQL publican por el outbox).
func Publish(ctx context.Context, js jetstream.JetStream, env *Envelope) error {
	if env.ID == uuid.Nil {
		env.ID = uuid.Must(uuid.NewV7())
	}
	if env.SchemaVersion == 0 {
		env.SchemaVersion = 1
	}
	if env.Time.IsZero() {
		env.Time = time.Now()
	}
	if env.TraceParent == nil {
		if tp := observability.TraceParentFrom(ctx); tp != "" {
			env.TraceParent = &tp
		}
	}
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("natsx: marshal: %w", err)
	}
	m := nats.NewMsg(env.Type + "." + env.Subject)
	m.Data = body
	m.Header.Set(HeaderMsgID, env.ID.String())
	m.Header.Set(HeaderType, env.Type)
	m.Header.Set(HeaderContentType, "application/json")
	m.Header.Set(HeaderTime, env.Time.UTC().Format(TimeFormat))
	if env.TenantID != nil {
		m.Header.Set(HeaderTenant, env.TenantID.String())
	} else {
		m.Header.Set(HeaderTenant, TenantPlatform)
	}
	if env.TraceParent != nil {
		m.Header.Set(observability.HeaderTraceParent, *env.TraceParent)
	}
	if _, err := js.PublishMsg(ctx, m); err != nil {
		return fmt.Errorf("natsx: publish %s: %w", env.Type, err)
	}
	return nil
}
