package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Permisos de la huella TLS del router.
const (
	PermTLSPinRead    = "devices.read"
	PermTLSPinCapture = "devices.update"
	// PermTLSPinConfirm: fijar (o volver a fijar) la huella es una decisión
	// de confianza sobre la credencial del router (re-autenticación reciente).
	PermTLSPinConfirm = PermCredentialsWrite
)

// Acciones de auditoría de la huella TLS.
const (
	AuditTLSObserved  = "devices.router.tls_fingerprint_observed"
	AuditTLSChanged   = "devices.router.tls_fingerprint_changed"
	AuditTLSConfirmed = "devices.router.tls_fingerprint_confirmed"
	AuditTLSRepinned  = "devices.router.tls_fingerprint_repinned"
)

// TLSProber lee la huella del certificado del router por el túnel (solo el
// saludo TLS: sin peticiones ni credenciales).
type TLSProber interface {
	ProbeFingerprint(ctx context.Context, tunnelIP string) (string, error)
}

// tlsPinStore es lo que necesita TLSPins del repositorio.
type tlsPinStore interface {
	GetRouter(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Router, error)
	GetCredential(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID, kind string) (*domain.Credential, error)
	ObserveTLSFingerprint(ctx context.Context, t pgdb.TenantID, id uuid.UUID, observed, result string, at time.Time) (*domain.Credential, error)
	ConfirmTLSFingerprint(ctx context.Context, t pgdb.TenantID, id uuid.UUID, fp, by string, at time.Time) (*domain.Credential, error)
	Emit(ctx context.Context, evs []outbox.Event) error
}

// TLSPinView es RouterTlsFingerprint del contrato.
type TLSPinView struct {
	RouterID       uuid.UUID
	Status         string
	PinnedSHA256   *string
	ObservedSHA256 *string
	ObservedAt     *time.Time
	ConfirmedAt    *time.Time
	ConfirmedBy    *string
	LastResult     *string
	LastUsedAt     *time.Time
}

func tlsPinView(routerID uuid.UUID, c *domain.Credential) *TLSPinView {
	v := &TLSPinView{RouterID: routerID, Status: domain.TLSPinStatus(c)}
	if c == nil {
		return v
	}
	v.PinnedSHA256, v.ConfirmedAt, v.ConfirmedBy, v.LastResult, v.LastUsedAt = c.TLSFingerprintSHA256, c.TLSConfirmedAt, c.TLSConfirmedBy,
		c.LastResult, c.LastUsedAt
	if c.TLSPending != nil {
		v.ObservedSHA256, v.ObservedAt = c.TLSPending, c.TLSPendingAt
	}
	return v
}

// TLSPins gestiona la huella TLS del router: captura en el alta (TOFU, solo
// queda pendiente), confirmación explícita de un administrador y detección de
// cambios (nunca se acepta sola una huella distinta de la fijada).
type TLSPins struct {
	store  tlsPinStore
	prober TLSProber
	audit  func() (authapi.AuditRecorder, bool)
	now    func() time.Time
	logger *slog.Logger
}

// NewTLSPins crea el servicio.
func NewTLSPins(store tlsPinStore, prober TLSProber, audit func() (authapi.AuditRecorder, bool), now func() time.Time,
	logger *slog.Logger,
) *TLSPins {
	if now == nil {
		now = time.Now
	}
	if audit == nil {
		audit = func() (authapi.AuditRecorder, bool) { return nil, false }
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &TLSPins{store: store, prober: prober, audit: audit, now: now, logger: logger}
}

// systemPrincipal es el actor de las observaciones en segundo plano.
var systemPrincipal = &authz.Principal{Type: authz.TypeService, Subject: "svc:devices"}

func (s *TLSPins) routerCred(ctx context.Context, perm string, routerID uuid.UUID) (*authz.Principal, pgdb.TenantID, *domain.Router,
	*domain.Credential, error,
) {
	p, t, err := scope(ctx, perm)
	if err != nil {
		return nil, t, nil, nil, err
	}
	r, err := s.store.GetRouter(ctx, t, routerID)
	if err != nil || !siteAllowed(p, perm, r.SiteID) {
		return nil, t, nil, nil, apperr.NotFound(domain.CodeRouterNotFound)
	}
	c, err := s.store.GetCredential(ctx, t, routerID, domain.CredentialRouterOSAPI)
	if errors.Is(err, domain.ErrNotFound) {
		return p, t, r, nil, nil
	}
	if err != nil {
		return nil, t, nil, nil, err
	}
	return p, t, r, c, nil
}

// routerOSOnly: la API REST de RouterOS (huella TLS, importación de
// prefijos, netwatch) solo existe en MikroTik.
func routerOSOnly(r *domain.Router) error {
	if r.Vendor != "" && r.Vendor != domain.VendorMikroTik {
		return apperr.New(apperr.KindInvalid, domain.CodeVendorNotSupported,
			"Solo los routers MikroTik tienen API REST de RouterOS (Huawei VRP se lee solo por SNMP).")
	}
	return nil
}

// Get es GET /routers/{id}/tls-fingerprint.
func (s *TLSPins) Get(ctx context.Context, routerID uuid.UUID) (*TLSPinView, error) {
	_, _, _, c, err := s.routerCred(ctx, PermTLSPinRead, routerID)
	if err != nil {
		return nil, err
	}
	return tlsPinView(routerID, c), nil
}

// Capture es POST /routers/{id}/tls-fingerprint/capture: lee la huella del
// certificado por el túnel (solo el saludo TLS) y la deja pendiente de
// confirmación si no coincide con la fijada.
func (s *TLSPins) Capture(ctx context.Context, routerID uuid.UUID) (*TLSPinView, error) {
	p, t, r, c, err := s.routerCred(ctx, PermTLSPinCapture, routerID)
	if err != nil {
		return nil, err
	}
	if err := routerOSOnly(r); err != nil {
		return nil, err
	}
	if r.TunnelAddress == nil || *r.TunnelAddress == "" {
		return nil, apperr.New(apperr.KindBadGateway, domain.CodeRouterUnreachable, "El router aún no tiene túnel con Horus: pegue el script de alta.")
	}
	if c == nil {
		return nil, apperr.Conflict(problem.CodeConflict, "No hay credencial de la API de RouterOS: genere el script de alta.")
	}
	observed, err := s.prober.ProbeFingerprint(ctx, *r.TunnelAddress)
	if err != nil {
		return nil, apperr.New(apperr.KindBadGateway, domain.CodeRouterUnreachable, "El router no responde por el túnel.")
	}
	c, err = s.Observe(ctx, p, t, r, c, observed)
	if err != nil {
		return nil, err
	}
	return tlsPinView(routerID, c), nil
}

// Observe registra una huella observada (captura interactiva, importación o
// lectura en segundo plano): coincide con la fijada → nada pendiente;
// distinta → pendiente de confirmación, last_result tls_fingerprint_changed y
// auditoría (solo la primera vez que se ve esa huella).
func (s *TLSPins) Observe(ctx context.Context, p *authz.Principal, t pgdb.TenantID, r *domain.Router, c *domain.Credential,
	observed string,
) (*domain.Credential, error) {
	fp, ok := domain.NormalizeFingerprint(observed)
	if !ok {
		return nil, fmt.Errorf("devices: invalid observed fingerprint")
	}
	if p == nil {
		p = systemPrincipal
	}
	pinned := ""
	if c.TLSFingerprintSHA256 != nil {
		pinned = strings.ToUpper(*c.TLSFingerprintSHA256)
	}
	result := ""
	if pinned != "" && pinned != fp {
		result = "tls_fingerprint_changed"
	}
	already := c.TLSPending != nil && strings.EqualFold(*c.TLSPending, fp)
	now := s.now().UTC()
	next, err := s.store.ObserveTLSFingerprint(ctx, t, c.ID, fp, result, now)
	if err != nil {
		return nil, mapStoreErr(err, domain.CodeRouterNotFound)
	}
	switch {
	case already || pinned == fp:
	case pinned == "":
		s.record(ctx, now, p, t, AuditTLSObserved, r, map[string]any{"tls_fingerprint_sha256": fp})
	default:
		s.logger.WarnContext(ctx, "devices: router TLS fingerprint changed; reads stopped until an admin re-pins it",
			slog.String("router_id", r.ID.String()))
		s.record(ctx, now, p, t, AuditTLSChanged, r, map[string]any{"pinned": pinned, "observed": fp})
	}
	return next, nil
}

// Confirm es POST /routers/{id}/tls-fingerprint/confirm: el administrador
// confirma la huella pendiente que ve en la consola (la primera o una nueva
// tras un cambio). Debe coincidir exactamente con la pendiente.
func (s *TLSPins) Confirm(ctx context.Context, routerID uuid.UUID, raw string) (*TLSPinView, error) {
	p, t, r, c, err := s.routerCred(ctx, PermTLSPinConfirm, routerID)
	if err != nil {
		return nil, err
	}
	fp, ok := domain.NormalizeFingerprint(raw)
	if !ok {
		return nil, apperr.Validation(apperr.Field("fingerprint_sha256", "INVALID_FORMAT", "SHA-256 en hexadecimal (64 dígitos, con o sin ':')."))
	}
	if c == nil || c.TLSPending == nil {
		return nil, apperr.Conflict(domain.CodeTLSFingerprintMismatch, "No hay ninguna huella pendiente de confirmar: léala de nuevo.")
	}
	if !strings.EqualFold(*c.TLSPending, fp) {
		return nil, apperr.Conflict(domain.CodeTLSFingerprintMismatch, "La huella no coincide con la que presenta el router: léala de nuevo.")
	}
	previous := c.TLSFingerprintSHA256
	now := s.now().UTC()
	next, err := s.store.ConfirmTLSFingerprint(ctx, t, c.ID, fp, p.Subject, now)
	if errors.Is(err, domain.ErrTLSPendingChanged) {
		return nil, apperr.Conflict(domain.CodeTLSFingerprintMismatch, "La huella pendiente cambió mientras confirmaba: léala de nuevo.")
	}
	if err != nil {
		return nil, mapStoreErr(err, domain.CodeRouterNotFound)
	}
	if previous == nil || *previous == "" {
		s.record(ctx, now, p, t, AuditTLSConfirmed, r, map[string]any{"tls_fingerprint_sha256": fp})
	} else {
		s.record(ctx, now, p, t, AuditTLSRepinned, r, map[string]any{"previous": *previous, "tls_fingerprint_sha256": fp})
	}
	return tlsPinView(routerID, next), nil
}

func (s *TLSPins) record(ctx context.Context, at time.Time, p *authz.Principal, t pgdb.TenantID, action string, r *domain.Router,
	changes map[string]any,
) {
	recordAudit(ctx, s.audit, s.store, s.logger, at, p, t, action, "router", r.ID.String(), "site:"+r.SiteID.String(), changes)
}
