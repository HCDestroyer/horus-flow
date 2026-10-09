// Package realtime es el hub WebSocket del gateway (I1-13; api.md §4,
// contrato C6 en packages/schemas/websocket/v0): tickets de un uso, una
// conexión ligada al `tid` del token, topics por permiso (y por dashboard
// asignado para kioscos), fan-out desde NATS core enrutando por la cabecera
// `Horus-Tenant`, coalescencia de topics de estado, reanudación acotada y
// cierres 4401/4403/4408/4409/4429.
package realtime

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// Protocol es el subprotocolo WebSocket.
const Protocol = "horus.ws.v1"

// Clases de topic.
const (
	ClassEvent = "event"
	ClassState = "state"
)

// TopicDef describe un topic del contrato C6 (topics.yaml).
type TopicDef struct {
	Name  string
	Class string
	// Permission requerida al usuario ("" = autenticado).
	Permission string
	// SiteFilter: filtrar por data.site_id según el alcance del permiso.
	SiteFilter bool
	// PII: campos de data que se quitan a kioscos sin show_personal_data.
	PII []string
}

// Topics son los topics de I1.
var Topics = map[string]TopicDef{
	"routers":         {Name: "routers", Class: ClassEvent, Permission: "devices.read", SiteFilter: true},
	"customers":       {Name: "customers", Class: ClassEvent, Permission: "customers.read", SiteFilter: true, PII: []string{"address", "alias"}},
	"security":        {Name: "security", Class: ClassEvent, Permission: "security.findings.read", SiteFilter: true},
	"exporters":       {Name: "exporters", Class: ClassEvent, Permission: "flows.read", SiteFilter: true},
	"traffic.summary": {Name: "traffic.summary", Class: ClassState, Permission: "traffic.read"},
	"wireguard.peers": {Name: "wireguard.peers", Class: ClassEvent, Permission: "wireguard.read"},
	"me":              {Name: "me", Class: ClassEvent},
	"system":          {Name: "system", Class: ClassState},
}

// DashboardTopic devuelve el UUID de un topic dashboard.<id>.
func DashboardTopic(topic string) (uuid.UUID, bool) {
	rest, ok := strings.CutPrefix(topic, "dashboard.")
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(rest)
	return id, err == nil
}

// LookupTopic devuelve la definición de un topic (dashboard.<id> incluido).
func LookupTopic(topic string) (TopicDef, bool) {
	if t, ok := Topics[topic]; ok {
		return t, true
	}
	if _, ok := DashboardTopic(topic); ok {
		return TopicDef{Name: topic, Class: ClassEvent}, true
	}
	return TopicDef{}, false
}

// Subjects son las suscripciones NATS core del hub (api.md §4.8, I1).
var Subjects = []string{
	"horus.devices.>", "horus.detection.finding.>", "horus.detection.customer.>", "horus.flows.exporter.>",
	"horus.wireguard.peer.>", "horus.analytics.dashboard.>", "horus.analytics.playlist.>", "horus.alerts.notification.>",
	"horus.auth.session.>", "horus.auth.membership.>", "horus.auth.kiosk.>", "horus.auth.tenant.>",
	"horus.telemetry.flows.summary.>",
}

// TopicsForType devuelve los topics de un tipo de evento (subject sin la
// entidad). Los eventos de playlist van a todas las suscripciones dashboard.*.
func TopicsForType(typ, entity string) []string {
	parts := strings.Split(typ, ".")
	if len(parts) != 4 {
		return nil
	}
	domain, ent, ev := parts[1], parts[2], parts[3]
	switch {
	case domain == "devices" && (ent == "router" || ent == "site"):
		return []string{"routers"}
	case domain == "devices" && ent == "customer":
		return []string{"customers"}
	case domain == "detection" && ent == "finding":
		return []string{"security"}
	case domain == "detection" && ent == "customer" && ev == "security_state_changed":
		return []string{"security"}
	case domain == "flows" && ent == "exporter" && slices.Contains([]string{"state_changed", "silent", "recovered"}, ev):
		return []string{"exporters"}
	case domain == "wireguard" && ent == "peer":
		return []string{"wireguard.peers"}
	case domain == "analytics" && ent == "dashboard":
		return []string{"dashboard." + entity}
	case domain == "analytics" && ent == "playlist":
		return []string{"dashboard.*"}
	case domain == "alerts" && ent == "notification":
		return []string{"me"}
	case domain == "auth" && ent == "session" && ev == "revoked":
		return []string{"me"}
	case domain == "auth" && ent == "membership":
		return []string{"me"}
	}
	return nil
}

// Projection es la proyección pública del sobre (EventProjection de C6).
type Projection struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Time             string          `json:"time"`
	Subject          string          `json:"subject"`
	TenantID         string          `json:"tenant_id"`
	AggregateVersion *int            `json:"aggregate_version"`
	Actor            *ProjActor      `json:"actor"`
	Data             json.RawMessage `json:"data"`
}

// ProjActor es actor.type/actor.id.
type ProjActor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// StripPII devuelve data sin los campos indicados.
func StripPII(data json.RawMessage, fields []string) json.RawMessage {
	if len(fields) == 0 {
		return data
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return data
	}
	for _, f := range fields {
		delete(m, f)
	}
	out, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return out
}

// dataFields son campos de data usados para filtrar sin más consultas.
type dataFields struct {
	SiteID          *uuid.UUID `json:"site_id"`
	UserID          *uuid.UUID `json:"user_id"`
	RecipientUserID *uuid.UUID `json:"recipient_user_id"`
	SessionID       *uuid.UUID `json:"session_id"`
	ID              *uuid.UUID `json:"id"`
	Items           []struct {
		DashboardID uuid.UUID `json:"dashboard_id"`
	} `json:"items"`
}
