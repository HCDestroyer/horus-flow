// Package api es el contrato público en proceso del módulo auth (ADR-0025
// §1): lo que otros módulos (gateway, devices…) pueden pedir a auth cuando
// está en el mismo proceso, registrado en module.Services. Refleja
// `horus.auth.v1.SessionService` (packages/protobuf/horus/auth/v1/session.proto);
// cuando auth esté en otro proceso, el cliente gRPC implementa las mismas
// interfaces.
package api

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Nombres en module.Services.
const (
	// ServiceSessions → SessionChecker.
	ServiceSessions = "auth.SessionService"
	// ServiceVerifier → *authz.Verifier con las claves públicas de auth.
	ServiceVerifier = "auth.Verifier"
	// ServiceAudit → AuditRecorder.
	ServiceAudit = "auth.Audit"
)

// CheckSessionRequest es la sesión (y tenant) a comprobar.
type CheckSessionRequest struct {
	SID         uuid.UUID
	UserID      uuid.UUID
	TenantID    uuid.UUID // uuid.Nil = sin tenant
	ViaPlatform bool
}

// CheckSessionResponse es el resultado.
type CheckSessionResponse struct {
	Active           bool
	MembershipActive bool
	TenantActive     bool
	RevokedReason    string
}

// SessionChecker comprueba revocación de sesiones y membresías (gateway).
type SessionChecker interface {
	CheckSession(ctx context.Context, req CheckSessionRequest) (CheckSessionResponse, error)
}

// AuditEntry es un registro de auditoría (docs/security.md §7.2).
type AuditEntry struct {
	TenantID     uuid.UUID // uuid.Nil = acción de plataforma
	OccurredAt   time.Time
	ActorType    string // user | service | system | kiosk
	ActorID      string
	ViaPlatform  bool
	Action       string // p. ej. platform.tenant_data.accessed
	ResourceType string
	ResourceID   string
	Scope        string
	Outcome      string // success | denied | failure
	IP           string
	UserAgent    string
	Changes      map[string]any
	RequestID    string
	TraceID      string
}

// AuditRecorder escribe en la auditoría append-only (cadena de hashes).
type AuditRecorder interface {
	Record(ctx context.Context, e AuditEntry) error
}
