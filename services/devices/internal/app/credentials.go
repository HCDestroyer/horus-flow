package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// PermCredentialsWrite es x-permission de PUT /routers/{id}/credentials/{kind}.
const PermCredentialsWrite = "devices.credentials.write"

// AuditCredentialsWrite es la acción auditada (docs/events.md §8.11).
const AuditCredentialsWrite = "devices.credentials.write"

// Tipos de credencial en la URL (enum `kind` de devices.yaml) y su valor en
// devices.credential.kind.
var credentialPathKinds = map[string]string{
	"snmp":         domain.CredentialSNMPv3,
	"routeros_api": domain.CredentialRouterOSAPI,
}

// CredentialKinds son los valores admitidos de `kind` en la URL.
var CredentialKinds = []string{"routeros_api", "snmp"}

// Límites de los campos de la credencial (los mínimos son los del contrato).
const (
	maxCredUser     = 64
	maxCredSecret   = 256
	minSNMPSecret   = 8
	minAPIPassword  = 16
	fingerprintSize = 32 // SHA-256
)

var (
	snmpCredFields = []string{"username", "auth_protocol", "auth_password", "priv_protocol", "priv_password"}
	apiCredFields  = []string{"username", "password", "tls_fingerprint_sha256"}
	snmpAuthProtos = []string{"sha1", "sha256"}
	snmpPrivProtos = []string{"aes128"}
)

// CredentialStore es el repositorio de PUT /routers/{id}/credentials/{kind}.
type CredentialStore interface {
	GetRouter(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Router, error)
	GetCredential(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID, kind string) (*domain.Credential, error)
	PutCredentials(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID, creds []domain.Credential) error
	Emit(ctx context.Context, evs []outbox.Event) error
}

// Credentials sustituye credenciales de router write-only: las que Horus usa
// para LEER del router (SNMPv3, usuario de solo lectura de RouterOS). Horus
// nunca escribe en el router; aquí solo se guarda, cifrado con envelope
// (CredentialAAD), lo que el operador ya configuró en él. El secreto no se
// devuelve, no se registra en logs ni entra en la auditoría.
type Credentials struct {
	store  CredentialStore
	sealer *envelope.Sealer
	audit  func() (authapi.AuditRecorder, bool)
	now    func() time.Time
	logger *slog.Logger
}

// NewCredentials crea el servicio.
func NewCredentials(store CredentialStore, sealer *envelope.Sealer, audit func() (authapi.AuditRecorder, bool),
	now func() time.Time, logger *slog.Logger,
) *Credentials {
	if now == nil {
		now = time.Now
	}
	if audit == nil {
		audit = func() (authapi.AuditRecorder, bool) { return nil, false }
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Credentials{store: store, sealer: sealer, audit: audit, now: now, logger: logger}
}

// CredentialStatus es el esquema CredentialStatus (sin secretos).
type CredentialStatus struct {
	Kind                 string
	Configured           bool
	UpdatedAt            *time.Time
	LastUsedAt           *time.Time
	LastResult           *string
	TLSFingerprintSHA256 *string
}

// Statuses devuelve el estado de las dos credenciales del router (snmp y
// routeros_api) para GET /routers/{id}: si están configuradas, último uso y
// resultado y la huella TLS fijada. Nunca el secreto. Requiere devices.read en
// el sitio del router; fuera de alcance → 404 como el resto de rutas.
func (c *Credentials) Statuses(ctx context.Context, routerID uuid.UUID) ([]CredentialStatus, error) {
	p, t, err := scope(ctx, "devices.read")
	if err != nil {
		return nil, err
	}
	r, err := c.store.GetRouter(ctx, t, routerID)
	if err != nil || !siteAllowed(p, "devices.read", r.SiteID) {
		return nil, apperr.NotFound(domain.CodeRouterNotFound)
	}
	out := make([]CredentialStatus, 0, len(CredentialKinds))
	for _, kind := range CredentialKinds {
		st := CredentialStatus{Kind: kind}
		cred, err := c.store.GetCredential(ctx, t, routerID, credentialPathKinds[kind])
		switch {
		case errors.Is(err, domain.ErrNotFound):
		case err != nil:
			return nil, err
		default:
			at := cred.UpdatedAt
			st.Configured, st.UpdatedAt, st.LastUsedAt, st.LastResult = true, &at, cred.LastUsedAt, cred.LastResult
			st.TLSFingerprintSHA256 = cred.TLSFingerprintSHA256
		}
		out = append(out, st)
	}
	return out, nil
}

// PutCredential valida, cifra y sustituye la credencial `kind` del router.
// Router de otro ISP, inexistente o fuera del alcance → 404 sin distinguir.
func (c *Credentials) PutCredential(ctx context.Context, routerID uuid.UUID, kind string, fields Fields) (*CredentialStatus, error) {
	p, t, err := scope(ctx, PermCredentialsWrite)
	if err != nil {
		return nil, err
	}
	dbKind, ok := credentialPathKinds[kind]
	if !ok {
		return nil, apperr.Validation(apperr.Field("kind", "INVALID_VALUE", "Valores permitidos: "+strings.Join(CredentialKinds, ", ")+"."))
	}
	r, err := c.store.GetRouter(ctx, t, routerID)
	if err != nil || !siteAllowed(p, PermCredentialsWrite, r.SiteID) {
		return nil, apperr.NotFound(domain.CodeRouterNotFound)
	}
	cred, secret, fpChange, err := c.parse(ctx, t, routerID, dbKind, fields)
	if err != nil {
		return nil, err
	}
	ct, dek, err := c.sealer.Seal(secret, CredentialAAD(t.UUID(), routerID, dbKind))
	clear(secret)
	if err != nil {
		return nil, fmt.Errorf("devices: seal credential: %w", err)
	}
	now := c.now().UTC()
	cred.ID, cred.RouterID, cred.Kind = uuid.Must(uuid.NewV7()), routerID, dbKind
	cred.Ciphertext, cred.DEKWrapped, cred.KEKID, cred.UpdatedAt, cred.Version = ct, dek, c.sealer.KEKID(), now, 1
	if err := c.store.PutCredentials(ctx, t, routerID, []domain.Credential{cred}); err != nil {
		return nil, mapStoreErr(err, domain.CodeRouterNotFound)
	}
	changes := map[string]any{"credential_kind": dbKind, "fields": credentialFieldNames(fields)}
	if fpChange != "" {
		changes["tls_fingerprint_sha256"] = fpChange
	}
	recordAudit(ctx, c.audit, c.store, c.logger, now, p, t, AuditCredentialsWrite, "router", routerID.String(),
		"site:"+r.SiteID.String(), changes)
	return &CredentialStatus{Kind: kind, Configured: true, UpdatedAt: &now, TLSFingerprintSHA256: cred.TLSFingerprintSHA256}, nil
}

// credentialFieldNames son los campos enviados (para la auditoría: nombres,
// nunca valores).
func credentialFieldNames(fields Fields) []string {
	out := make([]string, 0, len(fields))
	for k := range fields {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// parse valida el cuerpo según kind y devuelve la credencial (sin cifrar
// todavía), el secreto a cifrar y, si cambia la huella TLS fijada, cómo
// ("set" | "cleared" | "kept").
func (c *Credentials) parse(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID, kind string, fields Fields) (domain.Credential, []byte, string, error) {
	var f fieldErrs
	var cred domain.Credential
	switch kind {
	case domain.CredentialSNMPv3:
		unknownFields(&f, fields, snmpCredFields...)
		cred.Username = requiredText(&f, fields, "username", 1, maxCredUser)
		auth := requiredEnum(&f, fields, "auth_protocol", snmpAuthProtos)
		authPw := requiredText(&f, fields, "auth_password", minSNMPSecret, maxCredSecret)
		priv := requiredEnum(&f, fields, "priv_protocol", snmpPrivProtos)
		privPw := requiredText(&f, fields, "priv_password", minSNMPSecret, maxCredSecret)
		if err := f.err(); err != nil {
			return cred, nil, "", err
		}
		secret, _ := json.Marshal(snmpSecret{AuthProtocol: auth, AuthPassword: authPw, PrivProtocol: priv, PrivPassword: privPw})
		return cred, secret, "", nil
	default: // routeros_api
		unknownFields(&f, fields, apiCredFields...)
		cred.Username = requiredText(&f, fields, "username", 1, maxCredUser)
		pw := requiredText(&f, fields, "password", minAPIPassword, maxCredSecret)
		fp, fpSent := tlsFingerprint(&f, fields)
		if err := f.err(); err != nil {
			return cred, nil, "", err
		}
		change := ""
		switch {
		case fpSent && fp != nil:
			cred.TLSFingerprintSHA256, change = fp, "set"
		case fpSent:
			change = "cleared" // null explícito: se vuelve a fijar en el primer contacto (TOFU)
		default:
			// Sin el campo se conserva la huella fijada: rotar la contraseña no
			// cambia la identidad del router.
			prev, err := c.store.GetCredential(ctx, t, routerID, kind)
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return cred, nil, "", err
			}
			if prev != nil && prev.TLSFingerprintSHA256 != nil {
				fp := *prev.TLSFingerprintSHA256
				cred.TLSFingerprintSHA256, change = &fp, "kept"
			}
		}
		return cred, []byte(pw), change, nil
	}
}

func requiredText(f *fieldErrs, fields Fields, key string, minLen, maxLen int) string {
	raw, ok := fields[key]
	if !ok || isNull(raw) {
		f.add(key, "REQUIRED", key+" es obligatorio.")
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		f.add(key, "INVALID_TYPE", "Debe ser texto.")
		return ""
	}
	n := utf8.RuneCountInString(s)
	switch {
	case n < minLen:
		f.add(key, "TOO_SHORT", fmt.Sprintf("Mínimo %d caracteres.", minLen))
	case n > maxLen:
		f.add(key, "TOO_LONG", fmt.Sprintf("Máximo %d caracteres.", maxLen))
	case strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		f.add(key, "INVALID_VALUE", "Sin caracteres de control.")
	default:
		return s
	}
	return ""
}

func requiredEnum(f *fieldErrs, fields Fields, key string, allowed []string) string {
	if _, ok := fields[key]; !ok {
		f.add(key, "REQUIRED", key+" es obligatorio.")
		return ""
	}
	v, _ := f.enum(fields, key, allowed)
	return v
}

// tlsFingerprint lee tls_fingerprint_sha256 (hex de 64 dígitos, con o sin
// ':') y la normaliza al formato del lector RouterOS (AA:BB:…).
func tlsFingerprint(f *fieldErrs, fields Fields) (*string, bool) {
	raw, ok := fields["tls_fingerprint_sha256"]
	if !ok {
		return nil, false
	}
	if isNull(raw) {
		return nil, true
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		f.add("tls_fingerprint_sha256", "INVALID_TYPE", "Debe ser texto o null.")
		return nil, true
	}
	b, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(s), ":", ""))
	if err != nil || len(b) != fingerprintSize {
		f.add("tls_fingerprint_sha256", "INVALID_FORMAT", "SHA-256 en hexadecimal (64 dígitos, con o sin ':').")
		return nil, true
	}
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02X", x)
	}
	out := strings.Join(parts, ":")
	return &out, true
}

// recordAudit registra una acción del módulo: con el AuditRecorder de auth si
// es local; si no, como horus.devices.audit.recorded por el outbox.
func recordAudit(ctx context.Context, audit func() (authapi.AuditRecorder, bool), store interface {
	Emit(ctx context.Context, evs []outbox.Event) error
}, logger *slog.Logger, at time.Time, p *authz.Principal, t pgdb.TenantID, action, rtype, rid, auditScope string, changes map[string]any,
) {
	e := authapi.AuditEntry{TenantID: t.UUID(), OccurredAt: at, ActorType: p.Type, ActorID: p.Subject, ViaPlatform: p.ViaPlatform,
		Action: action, ResourceType: rtype, ResourceID: rid, Scope: auditScope, Outcome: "success", Changes: changes}
	if rec, ok := audit(); ok {
		if err := rec.Record(ctx, e); err != nil {
			logger.ErrorContext(ctx, "audit failed", slog.String("action", action), slog.Any("error", err))
		}
		return
	}
	id := uuid.Must(uuid.NewV7())
	tid := t.UUID()
	actor := outbox.ActorFrom(p)
	ev := outbox.Event{ID: id, Type: "horus.devices.audit.recorded", Source: "horus/devices", TenantID: &tid, AggregateType: "audit",
		AggregateID: id, AggregateVersion: 1, Actor: actor, OccurredAt: at, Data: map[string]any{
			"id": id.String(), "tenant_id": tid.String(), "occurred_at": at.Format(outbox.TimeFormat), "source_service": "devices",
			"actor": actor, "ip": nil, "user_agent": nil, "action": action, "resource_type": rtype, "resource_id": rid, "scope": auditScope,
			"outcome": "success", "reason": nil, "changes": changes, "request_id": nil, "trace_id": nil,
		}}
	if err := store.Emit(ctx, []outbox.Event{ev}); err != nil {
		logger.ErrorContext(ctx, "audit outbox failed", slog.String("action", action), slog.Any("error", err))
	}
}
