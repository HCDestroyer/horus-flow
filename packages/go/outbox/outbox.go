// Package outbox escribe eventos de dominio en la tabla `<esquema>.outbox`
// dentro de la transacción del cambio (transactional outbox, ADR-0016) con
// el sobre del contrato C4 (`horus.events.v1.Envelope` en JSON protojson,
// packages/events/v0/envelope.schema.json). El relay del módulo los publica
// después en NATS JetStream con `Nats-Msg-Id` = id y `Horus-Tenant`.
package outbox

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

// Actor del sobre (sin datos personales ni secretos).
type Actor struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	SID         string `json:"sid,omitempty"`
	Via         string `json:"via,omitempty"`
	ViaPlatform bool   `json:"via_platform"`
}

// ActorFrom construye el actor a partir del principal de la petición.
func ActorFrom(p *authz.Principal) Actor {
	if p == nil {
		return Actor{Type: "system", ID: "system:horus"}
	}
	a := Actor{Type: p.Type, ID: p.Subject, ViaPlatform: p.ViaPlatform}
	if p.SessionID != uuid.Nil {
		a.SID, a.Via = p.SessionID.String(), "session"
	}
	return a
}

// Event es un evento de dominio.
type Event struct {
	ID               uuid.UUID // vacío = UUIDv7 nuevo
	Type             string    // horus.<dominio>.<entidad>.<evento>
	Source           string    // horus/<módulo>
	TenantID         *uuid.UUID
	AggregateType    string
	AggregateID      uuid.UUID
	AggregateVersion int
	Actor            Actor
	OccurredAt       time.Time
	Data             any
}

var (
	typeRe   = regexp.MustCompile(`^horus\.[a-z_]+\.[a-z_]+\.[a-z_]+$`)
	schemaRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// TimeFormat es el formato de instantes del contrato (UTC, ms, Z).
const TimeFormat = "2006-01-02T15:04:05.000Z07:00"

// Insert escribe ev en schema.outbox con tx. El subject NATS es
// `<type>.<aggregate_id>`.
func Insert(ctx context.Context, tx pgx.Tx, schema string, ev Event) error {
	if !schemaRe.MatchString(schema) || !typeRe.MatchString(ev.Type) {
		return fmt.Errorf("outbox: invalid schema %q or type %q", schema, ev.Type)
	}
	if ev.ID == uuid.Nil {
		ev.ID = uuid.Must(uuid.NewV7())
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now()
	}
	// trace_parent (docs/observability.md §4.2): el de la petición o trabajo
	// que crea el evento; el relay lo publica tal cual (sobre y cabecera) y
	// el consumidor continúa la traza.
	var traceParent *string
	if tp := observability.TraceParentFrom(ctx); tp != "" {
		traceParent = &tp
	}
	var tenant *string
	if ev.TenantID != nil {
		s := ev.TenantID.String()
		tenant = &s
	}
	env := map[string]any{
		"id": ev.ID.String(), "type": ev.Type, "source": ev.Source, "subject": ev.AggregateID.String(),
		"time": ev.OccurredAt.UTC().Format(TimeFormat), "schema_version": 1, "tenant_id": tenant,
		"aggregate_type": ev.AggregateType, "aggregate_version": ev.AggregateVersion, "actor": ev.Actor,
		"trace_parent": traceParent, "correlation_id": nil, "causation_id": nil, "data": ev.Data,
	}
	headers := map[string]string{"Nats-Msg-Id": ev.ID.String(), "Horus-Type": ev.Type}
	if tenant != nil {
		headers["Horus-Tenant"] = *tenant
	}
	if traceParent != nil {
		headers[observability.HeaderTraceParent] = *traceParent
	}
	_, err := tx.Exec(ctx, `INSERT INTO `+pgx.Identifier{schema, "outbox"}.Sanitize()+`
		(id, tenant_id, subject, aggregate_id, headers, payload, occurred_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		ev.ID, ev.TenantID, ev.Type+"."+ev.AggregateID.String(), ev.AggregateID, headers, env, ev.OccurredAt)
	if err != nil {
		return fmt.Errorf("outbox: insert %s: %w", ev.Type, err)
	}
	return nil
}
