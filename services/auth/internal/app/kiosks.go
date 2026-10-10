package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// Códigos de error de kioscos.
const (
	CodeKioskNotFound           = "NOT_FOUND"
	CodeKioskEnrollmentInvalid  = "KIOSK_ENROLLMENT_CODE_INVALID"
	PermKiosksManage            = "kiosks.manage"
	kioskStatusCacheTTL         = 2 * time.Second
	errKioskVersion             = "kiosk version changed"
	kioskReasonCredentialReused = "credential_reuse"
)

// Errores del repositorio de kioscos.
var (
	ErrKioskVersion = errors.New(errKioskVersion)
	// ErrKioskCodeUsed: código ya canjeado, caducado o invalidado.
	ErrKioskCodeUsed = errors.New("kiosk code invalid")
)

// KioskEvents construye los eventos de un kiosco cambiado.
type KioskEvents func(k *domain.Kiosk) []outbox.Event

// CodeRedemption es el resultado de buscar un código de enrolamiento.
type CodeRedemption struct {
	CodeID   uuid.UUID
	Kiosk    *domain.Kiosk
	Used     bool
	Expired  bool
	Failures int
}

// KioskStore es el repositorio de kioscos.
type KioskStore interface {
	ListKiosks(ctx context.Context, t pgdb.TenantID, afterCreated *time.Time, afterID uuid.UUID, limit int) ([]domain.Kiosk, error)
	GetKiosk(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Kiosk, error)
	CreateKiosk(ctx context.Context, t pgdb.TenantID, k *domain.Kiosk, ev KioskEvents) error
	UpdateKiosk(ctx context.Context, t pgdb.TenantID, k *domain.Kiosk, expect int, ev KioskEvents) error
	// PutKioskCode invalida los códigos pendientes del kiosco y guarda uno nuevo.
	PutKioskCode(ctx context.Context, t pgdb.TenantID, kioskID, codeID uuid.UUID, hash []byte, expires time.Time) error
	// KioskByCode busca un código por su hash (sin tenant: canje público).
	KioskByCode(ctx context.Context, hash []byte) (*CodeRedemption, error)
	// KioskCodeFailure suma un fallo al código y lo invalida al llegar a max.
	KioskCodeFailure(ctx context.Context, codeID uuid.UUID, max int) error
	// EnrollKiosk consume el código y fija la credencial (falla si ya se usó).
	EnrollKiosk(ctx context.Context, codeID uuid.UUID, k *domain.Kiosk, credHash []byte, ev KioskEvents) error
	// KioskByCredential busca el kiosco de una credencial vigente (rotated
	// nil) o ya rotada (con los datos de su rotación).
	KioskByCredential(ctx context.Context, hash []byte) (k *domain.Kiosk, rotated *RotatedCredential, err error)
	// RotateKioskCredential sustituye la credencial (CAS sobre la anterior)
	// y registra la rotación con el arranque del proceso que la hace.
	RotateKioskCredential(ctx context.Context, k *domain.Kiosk, oldHash, newHash []byte, ip string, now time.Time, bootID uuid.UUID) (bool, error)
}

// RotatedCredential describe la rotación de una credencial de kiosco ya
// usada: su sucesora, qué arranque del proceso la rotó y cuándo.
type RotatedCredential struct {
	SuccessorHash []byte
	RotatedBy     *uuid.UUID
	RotatedAt     time.Time
}

// KioskRotationGrace es el plazo durante el que una rotación interrumpida
// por un reinicio puede reanudarse (D23).
const KioskRotationGrace = 30 * time.Minute

// resumable indica si presentar una credencial ya rotada es la respuesta
// perdida de una rotación que hizo otro arranque del proceso (murió tras
// confirmarla en PostgreSQL y antes de entregar la cookie nueva): la
// sucesora sigue siendo la vigente (nunca se usó) y no ha vencido la gracia.
// Dentro del mismo arranque, o si la sucesora ya se usó, es reutilización.
func (s *Kiosks) resumable(k *domain.Kiosk, r *RotatedCredential, now time.Time) bool {
	return r != nil && r.RotatedBy != nil && *r.RotatedBy != s.bootID && len(r.SuccessorHash) > 0 &&
		bytes.Equal(r.SuccessorHash, k.CredentialHash) && now.Sub(r.RotatedAt) < KioskRotationGrace
}

// Kiosks implementa los casos de uso de kioscos (I1-14).
type Kiosks struct {
	store   KioskStore
	signer  *authz.Signer
	audit   api.AuditRecorder
	cursor  *pagination.Codec
	baseURL string
	now     func() time.Time
	logger  *slog.Logger

	mu    sync.Mutex
	cache map[uuid.UUID]cachedKiosk
	// bootID identifica este arranque del proceso en las rotaciones.
	bootID uuid.UUID
}

type cachedKiosk struct {
	st  *api.KioskStatus
	exp time.Time
}

// NewKiosks crea el servicio.
func NewKiosks(store KioskStore, signer *authz.Signer, audit api.AuditRecorder, cursor *pagination.Codec, baseURL string,
	now func() time.Time, logger *slog.Logger,
) *Kiosks {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if cursor == nil {
		cursor = pagination.NewCodec(nil)
	}
	return &Kiosks{store: store, signer: signer, audit: audit, cursor: cursor, baseURL: strings.TrimRight(baseURL, "/"),
		now: now, logger: logger, cache: map[uuid.UUID]cachedKiosk{}, bootID: uuid.Must(uuid.NewV7())}
}

func (s *Kiosks) ts() time.Time { return s.now().UTC() }

func kioskScope(ctx context.Context) (*authz.Principal, pgdb.TenantID, error) {
	p := authz.FromContext(ctx)
	tid, err := authz.TenantOf(ctx)
	if err != nil || p.Type == authz.TypeKiosk {
		return nil, pgdb.TenantID{}, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	if !p.Has(PermKiosksManage) {
		return nil, pgdb.TenantID{}, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	return p, pgdb.TenantID(tid), nil
}

func kioskData(k *domain.Kiosk, reason *string, at time.Time) map[string]any {
	ids := k.DashboardIDs
	if ids == nil {
		ids = []uuid.UUID{}
	}
	return map[string]any{"id": k.ID, "version": k.Version, "name": k.Name, "status": k.Status, "reason": reason,
		"playlist_id": k.PlaylistID, "dashboard_ids": ids, "show_personal_data": k.ShowPersonalData,
		"changed_at": at.UTC().Format("2006-01-02T15:04:05.000Z")}
}

func kioskEvent(actor outbox.Actor, k *domain.Kiosk, typ string, reason *string, at time.Time) outbox.Event {
	tid := k.TenantID
	return outbox.Event{Type: "horus.auth.kiosk." + typ, Source: "horus/auth", TenantID: &tid, AggregateType: "kiosk",
		AggregateID: k.ID, AggregateVersion: k.Version, Actor: actor, OccurredAt: at, Data: kioskData(k, reason, at)}
}

func (s *Kiosks) record(ctx context.Context, tenant uuid.UUID, actorType, actorID, action string, kioskID uuid.UUID, outcome, ip string, changes map[string]any) {
	if s.audit == nil {
		return
	}
	if changes == nil {
		changes = map[string]any{}
	}
	if err := s.audit.Record(ctx, api.AuditEntry{TenantID: tenant, OccurredAt: s.ts(), ActorType: actorType, ActorID: actorID,
		Action: action, ResourceType: "kiosk", ResourceID: kioskID.String(), Scope: "tenant", Outcome: outcome, IP: ip, Changes: changes}); err != nil {
		s.logger.ErrorContext(ctx, "kiosk audit failed", slog.String("action", action), slog.Any("error", err))
	}
}

func (s *Kiosks) recordUser(ctx context.Context, p *authz.Principal, t pgdb.TenantID, action string, k *domain.Kiosk, changes map[string]any) {
	s.record(ctx, t.UUID(), p.Type, p.Subject, action, k.ID, "success", "", changes)
}

// ---------------------------------------------------------------- CRUD

// KioskFields es el cuerpo de alta o JSON Merge Patch.
type KioskFields map[string]json.RawMessage

func (s *Kiosks) apply(k *domain.Kiosk, f KioskFields, create bool) error {
	var errs []problem.FieldError
	bad := func(field, code, msg string) { errs = append(errs, apperr.Field(field, code, msg)) }
	for key := range f {
		if !slices.Contains([]string{"name", "playlist_id", "dashboard_ids", "allowed_cidrs", "show_personal_data",
			"show_personal_data_reason", "critical_finding_banner", "expires_at"}, key) {
			bad(key, "UNKNOWN_FIELD", "Campo no permitido.")
		}
	}
	if raw, ok := f["name"]; ok {
		var v string
		if json.Unmarshal(raw, &v) != nil || strings.TrimSpace(v) == "" || len([]rune(v)) > 80 {
			bad("name", "INVALID_VALUE", "Texto de 1 a 80 caracteres.")
		} else {
			k.Name = strings.TrimSpace(v)
		}
	} else if create {
		bad("name", "REQUIRED", "Obligatorio.")
	}
	if raw, ok := f["playlist_id"]; ok {
		var v *uuid.UUID
		if json.Unmarshal(raw, &v) != nil {
			bad("playlist_id", "INVALID_VALUE", "UUID o null.")
		} else {
			k.PlaylistID = v
		}
	}
	if raw, ok := f["dashboard_ids"]; ok {
		var v []uuid.UUID
		if json.Unmarshal(raw, &v) != nil || len(v) > 20 {
			bad("dashboard_ids", "INVALID_VALUE", "Lista de hasta 20 UUID.")
		} else {
			k.DashboardIDs = v
		}
	}
	if raw, ok := f["allowed_cidrs"]; ok {
		var v []string
		if json.Unmarshal(raw, &v) != nil || len(v) > 32 {
			bad("allowed_cidrs", "INVALID_VALUE", "Lista de hasta 32 CIDR.")
		} else {
			k.AllowedCIDRs = nil
			for _, c := range v {
				p, err := netip.ParsePrefix(strings.TrimSpace(c))
				if err != nil {
					bad("allowed_cidrs", "INVALID_FORMAT", "CIDR no válido: "+c)
					break
				}
				k.AllowedCIDRs = append(k.AllowedCIDRs, p.Masked())
			}
		}
	}
	for _, b := range []struct {
		key string
		dst *bool
	}{{"show_personal_data", &k.ShowPersonalData}, {"critical_finding_banner", &k.CriticalFindingBanner}} {
		if raw, ok := f[b.key]; ok {
			if json.Unmarshal(raw, b.dst) != nil {
				bad(b.key, "INVALID_TYPE", "Booleano.")
			}
		}
	}
	if raw, ok := f["show_personal_data_reason"]; ok {
		var v *string
		if json.Unmarshal(raw, &v) != nil || (v != nil && len([]rune(*v)) > 300) {
			bad("show_personal_data_reason", "INVALID_VALUE", "Texto de hasta 300 caracteres o null.")
		} else {
			k.ShowPersonalDataReason = v
		}
	}
	if raw, ok := f["expires_at"]; ok {
		var v time.Time
		if json.Unmarshal(raw, &v) != nil || !v.After(s.ts()) {
			bad("expires_at", "INVALID_VALUE", "Fecha futura RFC 3339.")
		} else {
			k.ExpiresAt = v.UTC()
		}
	}
	if k.ShowPersonalData && (k.ShowPersonalDataReason == nil || strings.TrimSpace(*k.ShowPersonalDataReason) == "") {
		bad("show_personal_data_reason", "REQUIRED", "Obligatorio si show_personal_data = true.")
	}
	if !k.ShowPersonalData {
		k.ShowPersonalDataReason = nil
	}
	if len(errs) > 0 {
		slices.SortFunc(errs, func(a, b problem.FieldError) int { return strings.Compare(a.Field, b.Field) })
		return apperr.Validation(errs...)
	}
	return nil
}

// Create es POST /kiosks.
func (s *Kiosks) Create(ctx context.Context, f KioskFields) (*domain.Kiosk, error) {
	p, t, err := kioskScope(ctx)
	if err != nil {
		return nil, err
	}
	now := s.ts()
	k := &domain.Kiosk{ID: uuid.Must(uuid.NewV7()), TenantID: t.UUID(), Status: domain.KioskPending, DashboardIDs: []uuid.UUID{},
		ExpiresAt: now.Add(domain.KioskDefaultExpiry), Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.apply(k, f, true); err != nil {
		return nil, err
	}
	if err := s.store.CreateKiosk(ctx, t, k, nil); err != nil {
		return nil, err
	}
	s.recordUser(ctx, p, t, "kiosks.create", k, map[string]any{"show_personal_data": k.ShowPersonalData, "allowed_cidrs": len(k.AllowedCIDRs)})
	return k, nil
}

// List es GET /kiosks.
func (s *Kiosks) List(ctx context.Context, limit int, cursor string) ([]domain.Kiosk, pagination.Page, error) {
	_, t, err := kioskScope(ctx)
	if err != nil {
		return nil, pagination.Page{}, err
	}
	var afterAt *time.Time
	afterID := uuid.Nil
	if cursor != "" {
		cur, err := s.cursor.Decode(cursor, "created_at", "", t.String())
		if err != nil || len(cur.Keys) != 2 {
			return nil, pagination.Page{}, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		at, e1 := time.Parse(time.RFC3339Nano, cur.Keys[0])
		id, e2 := uuid.Parse(cur.Keys[1])
		if e1 != nil || e2 != nil {
			return nil, pagination.Page{}, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		afterAt, afterID = &at, id
	}
	rows, err := s.store.ListKiosks(ctx, t, afterAt, afterID, limit+1)
	if err != nil {
		return nil, pagination.Page{}, err
	}
	pg := pagination.Page{Limit: limit}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next := s.cursor.Encode(pagination.Cursor{Keys: []string{last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID.String()},
			Sort: "created_at", Tenant: t.String()})
		pg.NextCursor, pg.HasMore = &next, true
	}
	now := s.ts()
	for i := range rows {
		rows[i].Status = rows[i].EffectiveStatus(now)
	}
	if rows == nil {
		rows = []domain.Kiosk{}
	}
	return rows, pg, nil
}

func (s *Kiosks) get(ctx context.Context, id uuid.UUID) (*authz.Principal, pgdb.TenantID, *domain.Kiosk, error) {
	p, t, err := kioskScope(ctx)
	if err != nil {
		return nil, t, nil, err
	}
	k, err := s.store.GetKiosk(ctx, t, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, t, nil, apperr.NotFound(CodeKioskNotFound)
	}
	if err != nil {
		return nil, t, nil, err
	}
	return p, t, k, nil
}

// Get es GET /kiosks/{id}.
func (s *Kiosks) Get(ctx context.Context, id uuid.UUID) (*domain.Kiosk, error) {
	_, _, k, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	k.Status = k.EffectiveStatus(s.ts())
	return k, nil
}

// Update es PATCH /kiosks/{id}.
func (s *Kiosks) Update(ctx context.Context, id uuid.UUID, expect int, f KioskFields) (*domain.Kiosk, error) {
	p, t, k, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if k.Version != expect {
		return nil, apperr.PreconditionFailed(k)
	}
	if err := s.apply(k, f, false); err != nil {
		return nil, err
	}
	if err := s.store.UpdateKiosk(ctx, t, k, expect, nil); err != nil {
		if errors.Is(err, ErrKioskVersion) {
			cur, _ := s.store.GetKiosk(ctx, t, id)
			return nil, apperr.PreconditionFailed(cur)
		}
		return nil, err
	}
	s.invalidate(k.ID)
	changed := make([]string, 0, len(f))
	for key := range f {
		changed = append(changed, key)
	}
	slices.Sort(changed)
	s.recordUser(ctx, p, t, "kiosks.update", k, map[string]any{"fields": changed, "show_personal_data": k.ShowPersonalData})
	k.Status = k.EffectiveStatus(s.ts())
	return k, nil
}

// Revoke es POST /kiosks/{id}/revoke: invalida la credencial y emite
// horus.auth.kiosk.revoked (el gateway cierra su WebSocket con 4409).
func (s *Kiosks) Revoke(ctx context.Context, id uuid.UUID, reason string) (*domain.Kiosk, error) {
	p, t, k, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if k.Status == domain.KioskRevoked {
		return k, nil
	}
	if reason == "" {
		reason = "manual"
	}
	if err := s.revoke(ctx, t, k, outbox.ActorFrom(p), reason); err != nil {
		return nil, err
	}
	s.recordUser(ctx, p, t, "kiosks.revoke", k, map[string]any{"reason": reason})
	return k, nil
}

func (s *Kiosks) revoke(ctx context.Context, t pgdb.TenantID, k *domain.Kiosk, actor outbox.Actor, reason string) error {
	expect := k.Version
	k.Status, k.CredentialHash, k.CredentialFamily = domain.KioskRevoked, nil, nil
	at := s.ts()
	err := s.store.UpdateKiosk(ctx, t, k, expect, func(k *domain.Kiosk) []outbox.Event {
		return []outbox.Event{kioskEvent(actor, k, "revoked", &reason, at)}
	})
	s.invalidate(k.ID)
	return err
}

// EnrollmentCode es POST /kiosks/{id}/enrollment-codes: código de un uso (10 min).
func (s *Kiosks) EnrollmentCode(ctx context.Context, id uuid.UUID) (code, qrURL string, expires time.Time, err error) {
	p, t, k, err := s.get(ctx, id)
	if err != nil {
		return "", "", time.Time{}, err
	}
	switch k.EffectiveStatus(s.ts()) {
	case domain.KioskRevoked, domain.KioskExpired:
		return "", "", time.Time{}, apperr.Conflict(problem.CodeConflict, "El kiosco está revocado o caducado: cree uno nuevo.")
	}
	code = domain.NewKioskCode()
	expires = s.ts().Add(domain.KioskCodeTTL)
	if err := s.store.PutKioskCode(ctx, t, k.ID, uuid.Must(uuid.NewV7()), domain.HashSecret(code), expires); err != nil {
		return "", "", time.Time{}, err
	}
	s.recordUser(ctx, p, t, "kiosks.enrollment_code.created", k, nil)
	return code, s.baseURL + "/kiosk/enroll#code=" + code, expires, nil
}

// ---------------------------------------------------------------- dispositivo

var errEnroll = &apperr.Error{Kind: apperr.KindInvalid, Code: CodeKioskEnrollmentInvalid,
	Detail: "Código no válido, caducado o ya usado. Pida uno nuevo al administrador."}

var errKioskCred = &apperr.Error{Kind: apperr.KindUnauthorized, Code: problem.CodeUnauthenticated,
	Detail: "Credencial de kiosco no válida: vuelva a enrolar la pantalla."}

// Enroll es POST /kiosk/enroll: canjea el código y devuelve la credencial de
// dispositivo (cookie). Un segundo canje falla; 10 fallos invalidan el código.
func (s *Kiosks) Enroll(ctx context.Context, code, ip string) (string, *domain.Kiosk, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if !domain.ValidKioskCode(code) {
		return "", nil, errEnroll
	}
	red, err := s.store.KioskByCode(ctx, domain.HashSecret(code))
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil, errEnroll
	}
	if err != nil {
		return "", nil, err
	}
	k := red.Kiosk
	now := s.ts()
	if red.Used || red.Expired || red.Failures >= domain.KioskCodeMaxFails {
		s.record(ctx, k.TenantID, "kiosk", k.ID.String(), "kiosks.enroll", k.ID, "denied", ip, map[string]any{"reason": "code_used_or_expired"})
		return "", nil, errEnroll
	}
	if st := k.EffectiveStatus(now); st == domain.KioskRevoked || st == domain.KioskExpired {
		return "", nil, errEnroll
	}
	if !k.AllowsIP(ip) {
		_ = s.store.KioskCodeFailure(ctx, red.CodeID, domain.KioskCodeMaxFails)
		s.record(ctx, k.TenantID, "kiosk", k.ID.String(), "kiosks.enroll", k.ID, "denied", ip, map[string]any{"reason": "cidr"})
		return "", nil, apperr.Forbidden(problem.CodeKioskForbidden, "La pantalla no está en una red permitida para este kiosco.")
	}
	cred := domain.NewKioskCredential()
	family := uuid.Must(uuid.NewV7())
	k.Status, k.CredentialFamily, k.LastSeenAt = domain.KioskActive, &family, &now
	if a, err := netip.ParseAddr(ip); err == nil {
		k.LastIP = &a
	}
	actor := outbox.Actor{Type: "kiosk", ID: k.ID.String()}
	if err := s.store.EnrollKiosk(ctx, red.CodeID, k, domain.HashSecret(cred), func(k *domain.Kiosk) []outbox.Event {
		return []outbox.Event{kioskEvent(actor, k, "enrolled", nil, now)}
	}); err != nil {
		if errors.Is(err, ErrKioskCodeUsed) {
			return "", nil, errEnroll
		}
		return "", nil, err
	}
	s.invalidate(k.ID)
	s.record(ctx, k.TenantID, "kiosk", k.ID.String(), "kiosks.enroll", k.ID, "success", ip, nil)
	return cred, k, nil
}

// KioskToken es el resultado de POST /kiosk/token.
type KioskToken struct {
	Access     string
	ExpiresAt  time.Time
	TenantID   uuid.UUID
	Credential string // cookie rotada
	CookieExp  time.Time
}

// Token es POST /kiosk/token: rota la credencial y emite un JWT de kiosco de
// 10 min sin permisos de escritura. Reutilizar una credencial ya rotada
// revoca el kiosco y avisa al administrador.
func (s *Kiosks) Token(ctx context.Context, cred, ip string) (*KioskToken, error) {
	if cred == "" || len(cred) > 128 {
		return nil, errKioskCred
	}
	hash := domain.HashSecret(cred)
	k, rotated, err := s.store.KioskByCredential(ctx, hash)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, errKioskCred
	}
	if err != nil {
		return nil, err
	}
	t := pgdb.TenantID(k.TenantID)
	if s.resumable(k, rotated, s.ts()) {
		s.logger.InfoContext(ctx, "kiosk credential: resuming a rotation interrupted by a restart", slog.String("kiosk_id", k.ID.String()))
		s.record(ctx, k.TenantID, "kiosk", k.ID.String(), "kiosks.credential.rotation_resumed", k.ID, "success", ip, nil)
		hash, rotated = rotated.SuccessorHash, nil
	}
	if rotated != nil {
		if k.Status != domain.KioskRevoked {
			s.logger.WarnContext(ctx, "kiosk credential reuse: revoking", slog.String("kiosk_id", k.ID.String()))
			if err := s.revoke(ctx, t, k, outbox.Actor{Type: "system", ID: "system:auth"}, kioskReasonCredentialReused); err != nil {
				return nil, err
			}
			s.record(ctx, k.TenantID, "kiosk", k.ID.String(), "kiosks.credential.reused", k.ID, "denied", ip, map[string]any{"revoked": true})
		}
		return nil, errKioskCred
	}
	now := s.ts()
	if k.EffectiveStatus(now) != domain.KioskActive {
		return nil, errKioskCred
	}
	if !k.AllowsIP(ip) {
		s.record(ctx, k.TenantID, "kiosk", k.ID.String(), "kiosks.token", k.ID, "denied", ip, map[string]any{"reason": "cidr"})
		return nil, apperr.Forbidden(problem.CodeKioskForbidden, "")
	}
	next := domain.NewKioskCredential()
	ok, err := s.store.RotateKioskCredential(ctx, k, hash, domain.HashSecret(next), ip, now, s.bootID)
	if err != nil {
		return nil, err
	}
	if !ok { // carrera con otro canje de la misma credencial
		return nil, errKioskCred
	}
	access, exp, err := s.signer.Sign(authz.Claims{
		Typ: authz.TypeKiosk, Scope: authz.ScopeKiosk, TID: k.TenantID.String(), KioskID: k.ID.String(),
		RegisteredClaims: jwtSubject("kiosk:" + k.ID.String()),
	})
	if err != nil {
		return nil, err
	}
	return &KioskToken{Access: access, ExpiresAt: exp, TenantID: k.TenantID, Credential: next, CookieExp: k.ExpiresAt}, nil
}

// ---------------------------------------------------------------- api.KioskChecker

func (s *Kiosks) invalidate(id uuid.UUID) {
	s.mu.Lock()
	delete(s.cache, id)
	s.mu.Unlock()
}

// CheckKiosk implementa api.KioskChecker (caché en proceso de 2 s).
func (s *Kiosks) CheckKiosk(ctx context.Context, tenantID, kioskID uuid.UUID) (*api.KioskStatus, error) {
	now := s.ts()
	s.mu.Lock()
	if c, ok := s.cache[kioskID]; ok && now.Before(c.exp) && c.st.TenantID == tenantID {
		s.mu.Unlock()
		return c.st, nil
	}
	s.mu.Unlock()
	k, err := s.store.GetKiosk(ctx, pgdb.TenantID(tenantID), kioskID)
	if errors.Is(err, domain.ErrNotFound) {
		st := &api.KioskStatus{ID: kioskID, TenantID: tenantID, Status: domain.KioskRevoked}
		return st, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth: check kiosk: %w", err)
	}
	status := k.EffectiveStatus(now)
	st := &api.KioskStatus{ID: k.ID, TenantID: k.TenantID, Name: k.Name, Active: status == domain.KioskActive, Status: status,
		AllowedCIDRs: k.AllowedCIDRs, DashboardIDs: k.DashboardIDs, PlaylistID: k.PlaylistID, ShowPersonalData: k.ShowPersonalData,
		CriticalFindingBanner: k.CriticalFindingBanner}
	s.mu.Lock()
	s.cache[kioskID] = cachedKiosk{st: st, exp: now.Add(kioskStatusCacheTTL)}
	s.mu.Unlock()
	return st, nil
}

var _ api.KioskChecker = (*Kiosks)(nil)

func jwtSubject(sub string) jwt.RegisteredClaims { return jwt.RegisteredClaims{Subject: sub} }
