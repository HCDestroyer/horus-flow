package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// Permisos (permissions.yaml, C7). Los reportes cubren todo el ISP: se
// exige el permiso con alcance de tenant completo (no por nodo).
const (
	PermRead     = "reports.read"
	PermExport   = "reports.export"
	PermSettings = "settings.manage"
	// PermCustomers decide la variante: sin customers.read con alcance de
	// todo el ISP, los archivos se sirven con los datos personales enmascarados.
	PermCustomers = "customers.read"
)

// Variantes de archivo (auditoría y cabecera X-Horus-Report-Variant).
const (
	VariantFull   = "full"
	VariantMasked = "masked"
)

// MaskedFor indica si p debe recibir la variante enmascarada: le falta
// customers.read con alcance de todo el ISP (el reporte cubre a todos los
// clientes; un permiso limitado a nodos no basta). Vale igual para usuarios
// y API tokens (sus permisos ya son la intersección token ∩ usuario).
func MaskedFor(p *authz.Principal) bool { return !p.TenantWide(PermCustomers) }

// MaskedCtx es MaskedFor del principal de la petición.
func MaskedCtx(ctx context.Context) bool { return MaskedFor(authz.FromContext(ctx)) }

// Códigos de error propios (ErrorCode de common.yaml).
const (
	CodeReportNotFound     = "REPORT_NOT_FOUND"
	CodeReportNotReady     = "REPORT_NOT_READY"
	CodeReportExpired      = "REPORT_EXPIRED"
	CodeDefinitionNotFound = "REPORT_DEFINITION_NOT_FOUND"
)

// RunsPerHour es el máximo de reportes a petición por usuario y hora (api.md §1.10).
const RunsPerHour = 10

// Service implementa la API de reportes.
type Service struct {
	St        *Store
	Files     Files
	Cursor    *pagination.Codec
	Now       func() time.Time
	Logger    *slog.Logger
	Audit     func() (authapi.AuditRecorder, bool)
	Directory func() (authapi.TenantDirectory, bool)
	Retention time.Duration
	// Wake despierta al worker tras encolar (no bloquea).
	Wake func()
	// EmailAvailable: hay SMTP de la instalación para el aviso a destinatarios.
	EmailAvailable bool
}

func (s *Service) now() time.Time { return s.Now().UTC() }

func notFound() error {
	return &apperr.Error{Kind: apperr.KindNotFound, Code: CodeReportNotFound, Title: "Reporte no encontrado"}
}

// principal devuelve el principal, su tenant y comprueba perm con alcance de tenant completo.
func principal(ctx context.Context, perm string) (*authz.Principal, pgdb.TenantID, error) {
	p := authz.FromContext(ctx)
	tid, err := authz.TenantOf(ctx)
	if err != nil {
		return nil, pgdb.TenantID{}, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	if p.Type == authz.TypeKiosk || !p.Has(perm) {
		return nil, pgdb.TenantID{}, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	if !p.TenantWide(perm) {
		return nil, pgdb.TenantID{}, apperr.Forbidden(problem.CodePermissionDenied,
			"Los reportes cubren todo el ISP: el permiso "+perm+" debe tener alcance de todo el ISP, no de un nodo.")
	}
	return p, pgdb.TenantID(tid), nil
}

// ListRuns es GET /reports.
func (s *Service) ListRuns(ctx context.Context, kind string, limit int, cursor string) ([]Run, pagination.Page, error) {
	_, t, err := principal(ctx, PermRead)
	if err != nil {
		return nil, pagination.Page{}, err
	}
	if kind != "" && !slices.Contains(Kinds, kind) {
		return nil, pagination.Page{}, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "kind: "+strings.Join(Kinds, ", "))
	}
	fk := pagination.FilterKey(kind)
	var before *time.Time
	beforeID := uuid.Nil
	if cursor != "" {
		cur, err := s.Cursor.Decode(cursor, "-created_at", fk, t.String())
		if err != nil || len(cur.Keys) != 2 {
			return nil, pagination.Page{}, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		at, e1 := time.Parse(time.RFC3339Nano, cur.Keys[0])
		id, e2 := uuid.Parse(cur.Keys[1])
		if e1 != nil || e2 != nil {
			return nil, pagination.Page{}, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		before, beforeID = &at, id
	}
	rows, err := s.St.ListRuns(ctx, t, kind, before, beforeID, limit)
	if err != nil {
		return nil, pagination.Page{}, err
	}
	pg := pagination.Page{Limit: limit}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next := s.Cursor.Encode(pagination.Cursor{Keys: []string{last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID.String()},
			Sort: "-created_at", Filter: fk, Tenant: t.String()})
		pg.NextCursor, pg.HasMore = &next, true
	}
	if rows == nil {
		rows = []Run{}
	}
	return rows, pg, nil
}

// GetRun es GET /reports/{id}.
func (s *Service) GetRun(ctx context.Context, id uuid.UUID) (*Run, error) {
	_, t, err := principal(ctx, PermRead)
	if err != nil {
		return nil, err
	}
	r, err := s.St.GetRun(ctx, t, id)
	if errors.Is(err, errNotFound) {
		return nil, notFound()
	}
	return r, err
}

// CreateInput es el cuerpo de POST /reports.
type CreateInput struct {
	Kind    string     `json:"kind"`
	From    *time.Time `json:"from"`
	To      *time.Time `json:"to"`
	Formats []string   `json:"formats"`
}

// CreateRun es POST /reports: encola la generación a petición (sin rango:
// la última semana ISO completa en la zona del ISP). Idempotente por
// (usuario, Idempotency-Key); 10 por usuario y hora.
func (s *Service) CreateRun(ctx context.Context, in CreateInput, idem uuid.UUID) (*Run, bool, error) {
	p, t, err := principal(ctx, PermExport)
	if err != nil {
		return nil, false, err
	}
	var fields []problem.FieldError
	if in.Kind == "" {
		in.Kind = KindSecurityWeekly
	}
	if !slices.Contains(Kinds, in.Kind) {
		fields = append(fields, apperr.Field("kind", "INVALID_VALUE", "Tipo de reporte no admitido: "+strings.Join(Kinds, ", ")+"."))
	}
	formats, ok := normFormats(in.Formats)
	if !ok {
		fields = append(fields, apperr.Field("formats", "INVALID_VALUE", "Formatos admitidos: pdf, csv."))
	}
	if (in.From == nil) != (in.To == nil) {
		fields = append(fields, apperr.Field("from", "REQUIRED", "from y to van juntos (o ninguno: última semana completa)."))
	}
	if len(fields) > 0 {
		return nil, false, apperr.Validation(fields...)
	}
	def, err := s.ensureDefinition(ctx, t, in.Kind)
	if err != nil {
		return nil, false, err
	}
	loc := LoadLocation(def.Timezone)
	now := s.now()
	period := LastWeek(now, loc)
	if in.From != nil {
		period, err = CustomRange(*in.From, *in.To, now, loc)
		if err != nil {
			return nil, false, apperr.Validation(apperr.Field("to", "INVALID_RANGE", strings.TrimPrefix(err.Error(), "reporting: ")+"."))
		}
	}
	var createdBy *uuid.UUID
	if p.UserID != uuid.Nil {
		u := p.UserID
		createdBy = &u
		n, oldest, err := s.St.CountRecentByUser(ctx, t, u, now.Add(-time.Hour))
		if err != nil {
			return nil, false, err
		}
		if n >= RunsPerHour {
			retry := int(oldest.Add(time.Hour).Sub(now).Seconds()) + 1
			if retry < 1 {
				retry = 1
			}
			return nil, false, &apperr.Error{Kind: apperr.KindTooManyRequests, Code: problem.CodeRateLimited, Title: "Demasiados reportes",
				Detail: fmt.Sprintf("Máximo %d reportes por hora y usuario.", RunsPerHour), RetryAfter: retry}
		}
	}
	run := &Run{ID: uuid.Must(uuid.NewV7()), TenantID: t.UUID(), Kind: in.Kind, Status: StatusQueued, PeriodFrom: period.From,
		PeriodTo: period.To, Timezone: loc.String(), Formats: formats, CreatedBy: createdBy, CreatedAt: now}
	var key *uuid.UUID
	if idem != uuid.Nil && createdBy != nil {
		key = &idem
	}
	out, replayed, err := s.St.InsertOnDemand(ctx, run, key)
	if err != nil {
		return nil, false, err
	}
	if !replayed && s.Wake != nil {
		s.Wake()
	}
	return out, replayed, nil
}

// Download es GET /reports/{id}/download: abre el archivo del formato en la
// variante que corresponde al principal (MaskedFor) (otro tenant o
// inexistente → 404; no generado → 409; caducado → 409) y audita la
// descarga con la variante servida.
func (s *Service) Download(ctx context.Context, id uuid.UUID, format, ip, userAgent string) (*Run, File, *os.File, error) {
	p, t, err := principal(ctx, PermRead)
	if err != nil {
		return nil, File{}, nil, err
	}
	if format == "" {
		format = FormatPDF
	}
	if !slices.Contains(Formats, format) {
		return nil, File{}, nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "format: pdf o csv")
	}
	r, err := s.St.GetRun(ctx, t, id)
	if errors.Is(err, errNotFound) {
		return nil, File{}, nil, notFound()
	}
	if err != nil {
		return nil, File{}, nil, err
	}
	if r.PurgedAt != nil {
		return nil, File{}, nil, &apperr.Error{Kind: apperr.KindConflict, Code: CodeReportExpired, Title: "Reporte caducado",
			Detail: "Los archivos se borraron al cumplirse la retención. Genere el reporte de nuevo."}
	}
	masked := MaskedFor(p)
	f, ok := r.File(format, masked)
	if r.Status != StatusCompleted || !ok {
		detail := "El reporte aún no está listo."
		switch {
		case r.Status == StatusCompleted && masked && slices.Contains(r.Formats, format):
			// Generado antes de existir la variante enmascarada: nunca se
			// sirve la completa a quien no tiene customers.read.
			detail = "Este reporte no tiene versión con los datos personales enmascarados (se generó antes de existir). Genérelo de nuevo."
		case r.Status == StatusCompleted || r.Status == StatusFailed:
			detail = "El reporte no tiene archivo en formato " + format + "."
		}
		return nil, File{}, nil, &apperr.Error{Kind: apperr.KindConflict, Code: CodeReportNotReady, Title: "Reporte no disponible", Detail: detail}
	}
	fh, err := s.Files.Open(f.ObjectKey)
	if err != nil {
		return nil, File{}, nil, fmt.Errorf("reporting: download %s: %w", r.ID, err)
	}
	variant := VariantFull
	if masked {
		variant = VariantMasked
	}
	s.audit(ctx, p, t, "reports.download", r.ID, ip, userAgent, map[string]any{"format": format, "variant": variant, "kind": r.Kind,
		"period_from": r.PeriodFrom.UTC().Format(outbox.TimeFormat), "period_to": r.PeriodTo.UTC().Format(outbox.TimeFormat), "sha256": f.SHA256})
	return r, f, fh, nil
}

// audit registra una acción: con el AuditRecorder de auth si es local; si
// no, como horus.analytics.audit.recorded por el outbox (lo persiste auth).
func (s *Service) audit(ctx context.Context, p *authz.Principal, t pgdb.TenantID, action string, id uuid.UUID, ip, ua string, changes map[string]any) {
	at := s.now()
	if s.Audit != nil {
		if rec, ok := s.Audit(); ok {
			if err := rec.Record(ctx, authapi.AuditEntry{TenantID: t.UUID(), OccurredAt: at, ActorType: p.Type, ActorID: p.Subject,
				ViaPlatform: p.ViaPlatform, Action: action, ResourceType: "report", ResourceID: id.String(), Scope: "tenant",
				Outcome: "success", IP: ip, UserAgent: ua, Changes: changes}); err != nil {
				s.Logger.ErrorContext(ctx, "reporting: audit failed", slog.String("action", action), slog.Any("error", err))
			}
			return
		}
	}
	eid := uuid.Must(uuid.NewV7())
	tid := t.UUID()
	actor := outbox.ActorFrom(p)
	var ipv, uav any
	if ip != "" {
		ipv = ip
	}
	if ua != "" {
		uav = ua
	}
	ev := outbox.Event{ID: eid, Type: "horus.analytics.audit.recorded", Source: "horus/analytics", TenantID: &tid, AggregateType: "audit",
		AggregateID: eid, AggregateVersion: 1, Actor: actor, OccurredAt: at, Data: map[string]any{
			"id": eid.String(), "tenant_id": tid.String(), "occurred_at": at.Format(outbox.TimeFormat), "source_service": "analytics",
			"actor": actor, "ip": ipv, "user_agent": uav, "action": action, "resource_type": "report", "resource_id": id.String(),
			"scope": "tenant", "outcome": "success", "reason": nil, "changes": changes, "request_id": nil, "trace_id": nil,
		}}
	if err := s.St.Emit(ctx, t, []outbox.Event{ev}); err != nil {
		s.Logger.ErrorContext(ctx, "reporting: audit outbox failed", slog.String("action", action), slog.Any("error", err))
	}
}

// tenantInfo busca nombre, slug y zona del ISP en el directorio de auth (si es local).
func (s *Service) tenantInfo(ctx context.Context, t uuid.UUID) (authapi.TenantSummary, bool) {
	if s.Directory == nil {
		return authapi.TenantSummary{}, false
	}
	dir, ok := s.Directory()
	if !ok {
		return authapi.TenantSummary{}, false
	}
	list, err := dir.TenantSummaries(ctx)
	if err != nil {
		return authapi.TenantSummary{}, false
	}
	for _, x := range list {
		if x.ID == t {
			return x, true
		}
	}
	return authapi.TenantSummary{}, false
}

// ensureDefinition crea, si falta, la definición por defecto del tipo (zona del ISP del directorio).
func (s *Service) ensureDefinition(ctx context.Context, t pgdb.TenantID, kind string) (*Definition, error) {
	info, _ := s.tenantInfo(ctx, t.UUID())
	return s.St.EnsureDefinition(ctx, t, kind, info.Timezone, info.Name, info.Slug, s.now())
}

// ListDefinitions es GET /report-definitions (crea las que falten con los valores por defecto).
func (s *Service) ListDefinitions(ctx context.Context) ([]Definition, error) {
	_, t, err := principal(ctx, PermRead)
	if err != nil {
		return nil, err
	}
	for _, k := range Kinds {
		if _, err := s.ensureDefinition(ctx, t, k); err != nil {
			return nil, err
		}
	}
	return s.St.ListDefinitions(ctx, t)
}

// UpdateDefinition es PATCH /report-definitions/{id} (JSON Merge Patch con If-Match).
func (s *Service) UpdateDefinition(ctx context.Context, id uuid.UUID, ifMatch int, patch map[string]json.RawMessage) (*Definition, error) {
	p, t, err := principal(ctx, PermSettings)
	if err != nil {
		return nil, err
	}
	d, err := s.St.GetDefinition(ctx, t, id)
	if errors.Is(err, errNotFound) {
		return nil, &apperr.Error{Kind: apperr.KindNotFound, Code: CodeDefinitionNotFound, Title: "Programación de reporte no encontrada"}
	}
	if err != nil {
		return nil, err
	}
	if ifMatch != d.Version {
		return nil, apperr.PreconditionFailed(d)
	}
	before := *d
	var fields []problem.FieldError
	bad := func(f, code, msg string) { fields = append(fields, apperr.Field(f, code, msg)) }
	intIn := func(raw json.RawMessage, lo, hi int) (int, bool) {
		var v int
		if json.Unmarshal(raw, &v) != nil || v < lo || v > hi {
			return 0, false
		}
		return v, true
	}
	for k, raw := range patch {
		switch k {
		case "enabled":
			if json.Unmarshal(raw, &d.Enabled) != nil {
				bad(k, "INVALID_VALUE", "Booleano.")
			}
		case "weekday":
			v, ok := intIn(raw, 1, 7)
			if !ok {
				bad(k, "INVALID_VALUE", "Día ISO: 1 (lunes) a 7 (domingo).")
			}
			d.Weekday = v
		case "hour":
			v, ok := intIn(raw, 0, 23)
			if !ok {
				bad(k, "INVALID_VALUE", "Hora de 0 a 23.")
			}
			d.Hour = v
		case "minute":
			v, ok := intIn(raw, 0, 59)
			if !ok {
				bad(k, "INVALID_VALUE", "Minuto de 0 a 59.")
			}
			d.Minute = v
		case "timezone":
			var v string
			if json.Unmarshal(raw, &v) != nil || !ValidTimezone(v) {
				bad(k, "INVALID_TIMEZONE", "Zona horaria IANA no válida.")
			}
			d.Timezone = v
		case "formats":
			var v []string
			f, ok := []string(nil), false
			if json.Unmarshal(raw, &v) == nil && len(v) > 0 {
				f, ok = normFormats(v)
			}
			if !ok {
				bad(k, "INVALID_VALUE", "Uno o más de: pdf, csv.")
			}
			d.Formats = f
		case "recipients":
			var v []string
			if json.Unmarshal(raw, &v) != nil || len(v) > 20 {
				bad(k, "INVALID_VALUE", "Lista de hasta 20 correos.")
				continue
			}
			out := []string{}
			for i, e := range v {
				e = strings.TrimSpace(e)
				a, err := mail.ParseAddress(e)
				if err != nil || a.Address != e || len(e) > 254 {
					bad(fmt.Sprintf("recipients.%d", i), "INVALID_EMAIL", "Correo no válido.")
					continue
				}
				if !slices.Contains(out, strings.ToLower(e)) {
					out = append(out, strings.ToLower(e))
				}
			}
			d.Recipients = out
		case "id", "tenant_id", "kind", "version", "created_at", "updated_at", "next_run_at", "tenant_name":
			bad(k, "READ_ONLY", "Campo de solo lectura.")
		default:
			bad(k, "UNKNOWN_FIELD", "Campo desconocido.")
		}
	}
	if len(fields) > 0 {
		return nil, apperr.Validation(fields...)
	}
	if err := s.St.UpdateDefinition(ctx, t, d, ifMatch, s.now()); err != nil {
		if errors.Is(err, errVersion) {
			cur, _ := s.St.GetDefinition(ctx, t, id)
			return nil, apperr.PreconditionFailed(cur)
		}
		return nil, err
	}
	s.audit(ctx, p, t, "reports.schedule.update", d.ID, "", "", definitionChanges(&before, d))
	return d, nil
}

// definitionChanges devuelve {campo: [antes, después]} de lo que cambió (sin correos: solo el número).
func definitionChanges(a, b *Definition) map[string]any {
	out := map[string]any{}
	if a.Enabled != b.Enabled {
		out["enabled"] = []any{a.Enabled, b.Enabled}
	}
	if a.Weekday != b.Weekday || a.Hour != b.Hour || a.Minute != b.Minute {
		out["schedule"] = []any{fmt.Sprintf("%d %02d:%02d", a.Weekday, a.Hour, a.Minute), fmt.Sprintf("%d %02d:%02d", b.Weekday, b.Hour, b.Minute)}
	}
	if a.Timezone != b.Timezone {
		out["timezone"] = []any{a.Timezone, b.Timezone}
	}
	if !slices.Equal(a.Formats, b.Formats) {
		out["formats"] = []any{a.Formats, b.Formats}
	}
	if !slices.Equal(a.Recipients, b.Recipients) {
		out["recipients_count"] = []any{len(a.Recipients), len(b.Recipients)}
	}
	return out
}
