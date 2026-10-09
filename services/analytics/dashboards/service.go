package dashboards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	tw "github.com/hcdestroyer/horus-flow/services/analytics/api/trafficwidgets"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// Permisos de dashboards (C7).
const (
	PermRead   = "dashboards.read"
	PermManage = "dashboards.manage"
)

// Service implementa los casos de uso de dashboards, playlists y kiosco.
type Service struct {
	st      *store
	catalog *Catalog
	cursor  *pagination.Codec
	kiosks  func() (authapi.KioskChecker, bool)
	widgets func() (tw.Provider, bool)
	now     func() time.Time
	logger  *slog.Logger
}

func (s *Service) ts() time.Time { return s.now().UTC() }

func fmtTS(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// viewer es quien mira: usuario o kiosco, con su tenant.
type viewer struct {
	p      *authz.Principal
	t      pgdb.TenantID
	kiosk  *authapi.KioskStatus
	assign map[uuid.UUID]bool // dashboards asignados al kiosco
}

func (v *viewer) isKiosk() bool { return v.kiosk != nil }

func (s *Service) viewer(ctx context.Context) (*viewer, error) {
	p := authz.FromContext(ctx)
	tid, err := authz.TenantOf(ctx)
	if err != nil {
		return nil, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	v := &viewer{p: p, t: pgdb.TenantID(tid)}
	if p.Type != authz.TypeKiosk {
		return v, nil
	}
	kc, ok := s.kiosks()
	if !ok {
		return nil, apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable, "Kioscos no disponibles en este proceso.")
	}
	st, err := kc.CheckKiosk(ctx, tid, p.KioskID)
	if err != nil {
		return nil, fmt.Errorf("dashboards: kiosk: %w", err)
	}
	if !st.Active {
		return nil, &apperr.Error{Kind: apperr.KindUnauthorized, Code: problem.CodeSessionRevoked}
	}
	v.kiosk, v.assign = st, map[uuid.UUID]bool{}
	for _, id := range st.DashboardIDs {
		v.assign[id] = true
	}
	if st.PlaylistID != nil {
		if pl, err := s.st.getPlaylist(ctx, v.t, *st.PlaylistID); err == nil {
			for _, it := range pl.Items {
				v.assign[it.DashboardID] = true
			}
		}
	}
	return v, nil
}

func (v *viewer) userID() *uuid.UUID {
	if v.p == nil || v.p.UserID == uuid.Nil {
		return nil
	}
	id := v.p.UserID
	return &id
}

func (v *viewer) owns(d *Dashboard) bool {
	u := v.userID()
	return u != nil && d.OwnerID != nil && *d.OwnerID == *u
}

func (v *viewer) canRead(d *Dashboard) bool {
	if v.isKiosk() {
		return v.assign[d.ID]
	}
	if !v.p.Has(PermRead) {
		return false
	}
	switch d.Visibility {
	case VisSystem, VisTenant:
		return true
	case VisPrivate:
		return v.owns(d)
	}
	return false
}

// load devuelve el dashboard si quien mira puede leerlo (404 si no).
func (s *Service) load(ctx context.Context, id uuid.UUID) (*viewer, *Dashboard, error) {
	v, err := s.viewer(ctx)
	if err != nil {
		return nil, nil, err
	}
	d, err := s.st.getDashboard(ctx, v.t, id)
	if errors.Is(err, errNotFound) || (err == nil && !v.canRead(d)) {
		return nil, nil, apperr.NotFound(CodeDashboardNotFound)
	}
	return v, d, err
}

// loadWritable aplica las reglas de edición (sistema: solo lectura).
func (s *Service) loadWritable(ctx context.Context, id uuid.UUID, expect int) (*viewer, *Dashboard, error) {
	v, d, err := s.load(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if v.isKiosk() {
		return nil, nil, apperr.Forbidden(problem.CodeKioskForbidden, "")
	}
	if d.Visibility == VisSystem {
		return nil, nil, apperr.Conflict(CodeDashboardReadOnly, "Las plantillas de Horus son de solo lectura: duplíquela para editarla.")
	}
	if !v.owns(d) && (d.Visibility != VisTenant || !v.p.Has(PermManage)) {
		return nil, nil, apperr.Forbidden(problem.CodePermissionDenied, "Solo el dueño o quien tenga dashboards.manage.")
	}
	if d.Version != expect {
		return nil, nil, apperr.PreconditionFailed(d)
	}
	return v, d, nil
}

func dashData(d *Dashboard, changed []string) map[string]any {
	m := map[string]any{"id": d.ID, "version": d.Version, "name": d.Name, "visibility": d.Visibility, "owner_id": d.OwnerID,
		"widget_count": len(d.Widgets), "updated_at": fmtTS(d.UpdatedAt)}
	if changed != nil {
		m["changed_fields"] = changed
	}
	return m
}

func (s *Service) dashEvent(v *viewer, typ string, changed []string) func(*Dashboard) []outbox.Event {
	at := s.ts()
	return func(d *Dashboard) []outbox.Event {
		tid := v.t.UUID()
		if typ == "deleted" {
			d.Version++
		}
		data := dashData(d, changed)
		if typ == "deleted" {
			data["deleted_at"] = fmtTS(at)
		}
		return []outbox.Event{{Type: "horus.analytics.dashboard." + typ, Source: "horus/analytics", TenantID: &tid, AggregateType: "dashboard",
			AggregateID: d.ID, AggregateVersion: d.Version, Actor: outbox.ActorFrom(v.p), OccurredAt: at, Data: data}}
	}
}

func (s *Service) save(ctx context.Context, v *viewer, d *Dashboard, expect int, changed []string) error {
	err := s.st.updateDashboard(ctx, v.t, d, expect, s.dashEvent(v, "updated", changed))
	if errors.Is(err, errVersion) {
		cur, gerr := s.st.getDashboard(ctx, v.t, d.ID)
		if gerr != nil {
			return apperr.NotFound(CodeDashboardNotFound)
		}
		return apperr.PreconditionFailed(cur)
	}
	return err
}

// ---------------------------------------------------------------- lectura

// ListDashboards es GET /dashboards (sin widgets).
func (s *Service) ListDashboards(ctx context.Context, visibility string, limit int, cursor string) ([]Dashboard, pagination.Page, error) {
	v, err := s.viewer(ctx)
	if err != nil {
		return nil, pagination.Page{}, err
	}
	if v.isKiosk() || !v.p.Has(PermRead) {
		return nil, pagination.Page{}, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	var vis []string
	for _, x := range strings.Split(visibility, ",") {
		if x = strings.TrimSpace(x); x != "" {
			if !slices.Contains([]string{VisSystem, VisTenant, VisPrivate}, x) {
				return nil, pagination.Page{}, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "visibility: system, tenant o private")
			}
			vis = append(vis, x)
		}
	}
	owner := uuid.Nil
	if u := v.userID(); u != nil {
		owner = *u
	}
	var after *time.Time
	afterID := uuid.Nil
	fk := pagination.FilterKey(visibility)
	if cursor != "" {
		at, id, err := s.decodeCursor(cursor, fk, v.t)
		if err != nil {
			return nil, pagination.Page{}, err
		}
		after, afterID = &at, id
	}
	rows, err := s.st.listDashboards(ctx, v.t, owner, vis, after, afterID, limit+1)
	if err != nil {
		return nil, pagination.Page{}, err
	}
	pg := pagination.Page{Limit: limit}
	if len(rows) > limit {
		rows = rows[:limit]
		next := s.encodeCursor(rows[len(rows)-1].CreatedAt, rows[len(rows)-1].ID, fk, v.t)
		pg.NextCursor, pg.HasMore = &next, true
	}
	if rows == nil {
		rows = []Dashboard{}
	}
	return rows, pg, nil
}

func (s *Service) decodeCursor(cursor, fk string, t pgdb.TenantID) (time.Time, uuid.UUID, error) {
	cur, err := s.cursor.Decode(cursor, "created_at", fk, t.String())
	if err != nil || len(cur.Keys) != 2 {
		return time.Time{}, uuid.Nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
	}
	at, e1 := time.Parse(time.RFC3339Nano, cur.Keys[0])
	id, e2 := uuid.Parse(cur.Keys[1])
	if e1 != nil || e2 != nil {
		return time.Time{}, uuid.Nil, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
	}
	return at, id, nil
}

func (s *Service) encodeCursor(at time.Time, id uuid.UUID, fk string, t pgdb.TenantID) string {
	return s.cursor.Encode(pagination.Cursor{Keys: []string{at.UTC().Format(time.RFC3339Nano), id.String()}, Sort: "created_at",
		Filter: fk, Tenant: t.String()})
}

// GetDashboard es GET /dashboards/{id} (kiosco: solo asignados).
func (s *Service) GetDashboard(ctx context.Context, id uuid.UUID) (*Dashboard, error) {
	_, d, err := s.load(ctx, id)
	return d, err
}

// ---------------------------------------------------------------- escritura

// DashboardInput es el cuerpo de POST /dashboards.
type DashboardInput struct {
	Name           string     `json:"name"`
	Visibility     string     `json:"visibility"`
	Layout         *Layout    `json:"layout"`
	DefaultRange   string     `json:"default_range"`
	RefreshSeconds int        `json:"refresh_seconds"`
	Variables      *Variables `json:"variables"`
	Widgets        []Widget   `json:"widgets"`
}

// CreateDashboard es POST /dashboards (privado por defecto).
func (s *Service) CreateDashboard(ctx context.Context, in DashboardInput) (*Dashboard, error) {
	v, err := s.viewer(ctx)
	if err != nil {
		return nil, err
	}
	if v.isKiosk() || !v.p.Has(PermRead) {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	if in.Visibility == "" {
		in.Visibility = VisPrivate
	}
	if in.Visibility != VisPrivate && in.Visibility != VisTenant {
		return nil, apperr.Validation(apperr.Field("visibility", "INVALID_VALUE", "private o tenant."))
	}
	if in.Visibility == VisTenant && !v.p.Has(PermManage) {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "visibility=tenant exige dashboards.manage.")
	}
	if in.Layout == nil {
		return nil, apperr.Validation(apperr.Field("layout", "REQUIRED", "Obligatorio."))
	}
	now := s.ts()
	tid := v.t.UUID()
	d := &Dashboard{ID: uuid.Must(uuid.NewV7()), TenantID: &tid, OwnerID: v.userID(), Name: strings.TrimSpace(in.Name),
		Visibility: in.Visibility, Layout: *in.Layout, DefaultRange: in.DefaultRange, RefreshSeconds: in.RefreshSeconds,
		Widgets: in.Widgets, CreatedAt: now, UpdatedAt: now, Version: 1}
	if d.DefaultRange == "" {
		d.DefaultRange = "6h"
	}
	if d.RefreshSeconds == 0 {
		d.RefreshSeconds = 30
	}
	if in.Variables != nil {
		d.Variables = *in.Variables
	}
	if d.Widgets == nil {
		d.Widgets = []Widget{}
	}
	if err := validateDocument(s.catalog, d); err != nil {
		return nil, err
	}
	if err := s.st.insertDashboard(ctx, v.t, d, s.dashEvent(v, "created", nil)); err != nil {
		return nil, err
	}
	return d, nil
}

// UpdateDashboard es PATCH /dashboards/{id} (nombre, visibilidad, rango, refresco, variables).
func (s *Service) UpdateDashboard(ctx context.Context, id uuid.UUID, expect int, f map[string]json.RawMessage) (*Dashboard, error) {
	v, d, err := s.loadWritable(ctx, id, expect)
	if err != nil {
		return nil, err
	}
	var fe fieldErrs
	var changed []string
	for k, raw := range f {
		changed = append(changed, k)
		var e error
		switch k {
		case "name":
			e = json.Unmarshal(raw, &d.Name)
		case "visibility":
			var vis string
			if e = json.Unmarshal(raw, &vis); e == nil {
				if vis != VisPrivate && vis != VisTenant {
					fe.add("visibility", "INVALID_VALUE", "private o tenant.")
				} else if vis == VisTenant && !v.p.Has(PermManage) {
					return nil, apperr.Forbidden(problem.CodePermissionDenied, "visibility=tenant exige dashboards.manage.")
				}
				d.Visibility = vis
			}
		case "default_range":
			e = json.Unmarshal(raw, &d.DefaultRange)
		case "refresh_seconds":
			e = json.Unmarshal(raw, &d.RefreshSeconds)
		case "variables":
			d.Variables = Variables{}
			e = json.Unmarshal(raw, &d.Variables)
		default:
			fe.add(k, "UNKNOWN_FIELD", "Campo no permitido.")
		}
		if e != nil {
			fe.add(k, "INVALID_TYPE", "Tipo no válido.")
		}
	}
	if err := fe.err(); err != nil {
		return nil, err
	}
	if err := validateDocument(s.catalog, d); err != nil {
		return nil, err
	}
	slices.Sort(changed)
	return d, s.save(ctx, v, d, expect, changed)
}

// DeleteDashboard es DELETE /dashboards/{id}.
func (s *Service) DeleteDashboard(ctx context.Context, id uuid.UUID, expect int) error {
	v, d, err := s.loadWritable(ctx, id, expect)
	if err != nil {
		return err
	}
	err = s.st.deleteDashboard(ctx, v.t, d, expect, s.dashEvent(v, "deleted", nil))
	if errors.Is(err, errVersion) {
		return apperr.PreconditionFailed(d)
	}
	return err
}

// DuplicateDashboard es POST /dashboards/{id}/duplicate (copia privada).
func (s *Service) DuplicateDashboard(ctx context.Context, id uuid.UUID, name string) (*Dashboard, error) {
	v, src, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if v.isKiosk() {
		return nil, apperr.Forbidden(problem.CodeKioskForbidden, "")
	}
	now := s.ts()
	tid := v.t.UUID()
	d := *src
	d.ID, d.TenantID, d.OwnerID, d.Visibility, d.CreatedAt, d.UpdatedAt, d.Version = uuid.Must(uuid.NewV7()), &tid, v.userID(), VisPrivate, now, now, 1
	d.Name = strings.TrimSpace(name)
	if d.Name == "" {
		d.Name = src.Name + " (copia)"
		if len([]rune(d.Name)) > 120 {
			d.Name = string([]rune(d.Name)[:120])
		}
	}
	d.Widgets = slices.Clone(src.Widgets)
	if err := validateDocument(s.catalog, &d); err != nil {
		return nil, err
	}
	if err := s.st.insertDashboard(ctx, v.t, &d, s.dashEvent(v, "created", nil)); err != nil {
		return nil, err
	}
	return &d, nil
}

// LayoutReplace es el cuerpo de PUT /dashboards/{id}/layout.
type LayoutReplace struct {
	Layout    *Layout             `json:"layout"`
	Positions map[string]Position `json:"positions"`
}

// ReplaceLayout es PUT /dashboards/{id}/layout.
func (s *Service) ReplaceLayout(ctx context.Context, id uuid.UUID, expect int, in LayoutReplace) (*Dashboard, error) {
	v, d, err := s.loadWritable(ctx, id, expect)
	if err != nil {
		return nil, err
	}
	if in.Layout == nil {
		return nil, apperr.Validation(apperr.Field("layout", "REQUIRED", "Obligatorio."))
	}
	if len(in.Positions) != len(d.Widgets) {
		return nil, apperr.Validation(apperr.Field("positions", "INVALID_VALUE", "Debe cubrir exactamente todos los widgets."))
	}
	for i := range d.Widgets {
		p, ok := in.Positions[d.Widgets[i].ID]
		if !ok {
			return nil, apperr.Validation(apperr.Field("positions."+d.Widgets[i].ID, "REQUIRED", "Falta la posición del widget."))
		}
		d.Widgets[i].Position = p
	}
	d.Layout = *in.Layout
	if err := validateDocument(s.catalog, d); err != nil {
		return nil, err
	}
	return d, s.save(ctx, v, d, expect, []string{"layout", "widgets"})
}

// AddWidget es POST /dashboards/{id}/widgets.
func (s *Service) AddWidget(ctx context.Context, id uuid.UUID, expect int, w Widget) (*Dashboard, error) {
	v, d, err := s.loadWritable(ctx, id, expect)
	if err != nil {
		return nil, err
	}
	if x, _ := d.Widget(w.ID); x != nil {
		return nil, apperr.Validation(apperr.Field("id", "DUPLICATE", "Ya existe un widget con ese id."))
	}
	d.Widgets = append(d.Widgets, w)
	if err := validateDocument(s.catalog, d); err != nil {
		return nil, err
	}
	return d, s.save(ctx, v, d, expect, []string{"widgets"})
}

// UpdateWidget es PATCH /dashboards/{id}/widgets/{wid}.
func (s *Service) UpdateWidget(ctx context.Context, id uuid.UUID, wid string, expect int, f map[string]json.RawMessage) (*Dashboard, error) {
	v, d, err := s.loadWritable(ctx, id, expect)
	if err != nil {
		return nil, err
	}
	w, _ := d.Widget(wid)
	if w == nil {
		return nil, apperr.NotFound(problem.CodeNotFound)
	}
	var fe fieldErrs
	for k, raw := range f {
		var e error
		switch k {
		case "title":
			w.Title = nil
			e = json.Unmarshal(raw, &w.Title)
		case "position":
			e = json.Unmarshal(raw, &w.Position)
		case "config":
			w.Config = append(json.RawMessage(nil), raw...)
		case "refresh_seconds":
			w.RefreshSeconds = nil
			e = json.Unmarshal(raw, &w.RefreshSeconds)
		default:
			fe.add(k, "UNKNOWN_FIELD", "Campo no permitido.")
		}
		if e != nil {
			fe.add(k, "INVALID_TYPE", "Tipo no válido.")
		}
	}
	if err := fe.err(); err != nil {
		return nil, err
	}
	if err := validateDocument(s.catalog, d); err != nil {
		return nil, err
	}
	return d, s.save(ctx, v, d, expect, []string{"widgets"})
}

// DeleteWidget es DELETE /dashboards/{id}/widgets/{wid}.
func (s *Service) DeleteWidget(ctx context.Context, id uuid.UUID, wid string, expect int) (*Dashboard, error) {
	v, d, err := s.loadWritable(ctx, id, expect)
	if err != nil {
		return nil, err
	}
	_, i := d.Widget(wid)
	if i < 0 {
		return nil, apperr.NotFound(problem.CodeNotFound)
	}
	d.Widgets = slices.Delete(d.Widgets, i, i+1)
	return d, s.save(ctx, v, d, expect, []string{"widgets"})
}

// ---------------------------------------------------------------- playlists

// PlaylistInput es el cuerpo de POST/PATCH /playlists.
type PlaylistInput struct {
	Name       *string         `json:"name"`
	Items      *[]PlaylistItem `json:"items"`
	Transition *string         `json:"transition"`
}

func (s *Service) manager(ctx context.Context) (*viewer, error) {
	v, err := s.viewer(ctx)
	if err != nil {
		return nil, err
	}
	if v.isKiosk() || !v.p.Has(PermManage) {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	return v, nil
}

func (s *Service) playlistEvent(v *viewer, typ string) func(*Playlist) []outbox.Event {
	at := s.ts()
	return func(p *Playlist) []outbox.Event {
		tid := v.t.UUID()
		data := map[string]any{"id": p.ID, "version": p.Version, "name": p.Name, "items": p.Items, "transition": p.Transition,
			"updated_at": fmtTS(p.UpdatedAt)}
		if typ == "deleted" {
			p.Version++
			data = map[string]any{"id": p.ID, "version": p.Version, "deleted_at": fmtTS(at)}
		}
		return []outbox.Event{{Type: "horus.analytics.playlist." + typ, Source: "horus/analytics", TenantID: &tid, AggregateType: "playlist",
			AggregateID: p.ID, AggregateVersion: p.Version, Actor: outbox.ActorFrom(v.p), OccurredAt: at, Data: data}}
	}
}

func (s *Service) applyPlaylist(ctx context.Context, v *viewer, p *Playlist, in PlaylistInput) error {
	if in.Name != nil {
		p.Name = strings.TrimSpace(*in.Name)
	}
	if in.Items != nil {
		p.Items = *in.Items
		for i := range p.Items {
			if p.Items[i].DurationSeconds == 0 {
				p.Items[i].DurationSeconds = 30
			}
		}
	}
	if in.Transition != nil {
		p.Transition = *in.Transition
	}
	if err := validatePlaylist(p); err != nil {
		return err
	}
	ids := make([]uuid.UUID, 0, len(p.Items))
	for _, it := range p.Items {
		ids = append(ids, it.DashboardID)
	}
	ok, err := s.st.visibleDashboards(ctx, v.t, ids)
	if err != nil {
		return err
	}
	for i, it := range p.Items {
		if !ok[it.DashboardID] {
			return apperr.Validation(apperr.Field(fmt.Sprintf("items[%d].dashboard_id", i), "NOT_FOUND",
				"Dashboard inexistente o privado (una playlist solo admite plantillas y dashboards del ISP)."))
		}
	}
	return nil
}

// CreatePlaylist es POST /playlists (emite playlist.updated).
func (s *Service) CreatePlaylist(ctx context.Context, in PlaylistInput) (*Playlist, error) {
	v, err := s.manager(ctx)
	if err != nil {
		return nil, err
	}
	now := s.ts()
	p := &Playlist{ID: uuid.Must(uuid.NewV7()), TenantID: v.t.UUID(), Transition: "fade", CreatedAt: now, UpdatedAt: now, Version: 1}
	if err := s.applyPlaylist(ctx, v, p, in); err != nil {
		return nil, err
	}
	if err := s.st.savePlaylist(ctx, v.t, p, 0, s.playlistEvent(v, "updated")); err != nil {
		return nil, err
	}
	return p, nil
}

// ListPlaylists es GET /playlists.
func (s *Service) ListPlaylists(ctx context.Context, limit int, cursor string) ([]Playlist, pagination.Page, error) {
	v, err := s.viewer(ctx)
	if err != nil {
		return nil, pagination.Page{}, err
	}
	if v.isKiosk() || !v.p.Has(PermRead) {
		return nil, pagination.Page{}, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	var after *time.Time
	afterID := uuid.Nil
	if cursor != "" {
		at, id, err := s.decodeCursor(cursor, "playlists", v.t)
		if err != nil {
			return nil, pagination.Page{}, err
		}
		after, afterID = &at, id
	}
	rows, err := s.st.listPlaylists(ctx, v.t, after, afterID, limit+1)
	if err != nil {
		return nil, pagination.Page{}, err
	}
	pg := pagination.Page{Limit: limit}
	if len(rows) > limit {
		rows = rows[:limit]
		next := s.encodeCursor(rows[len(rows)-1].CreatedAt, rows[len(rows)-1].ID, "playlists", v.t)
		pg.NextCursor, pg.HasMore = &next, true
	}
	if rows == nil {
		rows = []Playlist{}
	}
	return rows, pg, nil
}

func (s *Service) loadPlaylist(ctx context.Context, id uuid.UUID, expect int) (*viewer, *Playlist, error) {
	v, err := s.manager(ctx)
	if err != nil {
		return nil, nil, err
	}
	p, err := s.st.getPlaylist(ctx, v.t, id)
	if errors.Is(err, errNotFound) {
		return nil, nil, apperr.NotFound(problem.CodeNotFound)
	}
	if err != nil {
		return nil, nil, err
	}
	if p.Version != expect {
		return nil, nil, apperr.PreconditionFailed(p)
	}
	return v, p, nil
}

// UpdatePlaylist es PATCH /playlists/{id} (los kioscos la aplican solos por el evento).
func (s *Service) UpdatePlaylist(ctx context.Context, id uuid.UUID, expect int, in PlaylistInput) (*Playlist, error) {
	v, p, err := s.loadPlaylist(ctx, id, expect)
	if err != nil {
		return nil, err
	}
	if err := s.applyPlaylist(ctx, v, p, in); err != nil {
		return nil, err
	}
	err = s.st.savePlaylist(ctx, v.t, p, expect, s.playlistEvent(v, "updated"))
	if errors.Is(err, errVersion) {
		return nil, apperr.PreconditionFailed(p)
	}
	return p, err
}

// DeletePlaylist es DELETE /playlists/{id}.
func (s *Service) DeletePlaylist(ctx context.Context, id uuid.UUID, expect int) error {
	v, p, err := s.loadPlaylist(ctx, id, expect)
	if err != nil {
		return err
	}
	err = s.st.deletePlaylist(ctx, v.t, p, expect, s.playlistEvent(v, "deleted"))
	if errors.Is(err, errVersion) {
		return apperr.PreconditionFailed(p)
	}
	return err
}

// ---------------------------------------------------------------- kiosco

// KioskConfig es la respuesta de GET /kiosk/config.
type KioskConfig struct {
	KioskID               uuid.UUID      `json:"kiosk_id"`
	TenantID              uuid.UUID      `json:"tenant_id"`
	PlaylistID            *uuid.UUID     `json:"playlist_id"`
	Items                 []PlaylistItem `json:"items"`
	Transition            string         `json:"transition"`
	ShowPersonalData      bool           `json:"show_personal_data"`
	CriticalFindingBanner bool           `json:"critical_finding_banner"`
	FrontendMinVersion    *string        `json:"frontend_min_version"`
	Topics                []string       `json:"-"`
}

// KioskConfig devuelve la playlist o los dashboards del kiosco que llama.
func (s *Service) KioskConfig(ctx context.Context) (*KioskConfig, error) {
	v, err := s.viewer(ctx)
	if err != nil {
		return nil, err
	}
	if !v.isKiosk() {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "Ruta exclusiva de kioscos.")
	}
	k := v.kiosk
	out := &KioskConfig{KioskID: k.ID, TenantID: k.TenantID, PlaylistID: k.PlaylistID, Transition: "fade",
		ShowPersonalData: k.ShowPersonalData, CriticalFindingBanner: k.CriticalFindingBanner, Items: []PlaylistItem{}}
	if k.PlaylistID != nil {
		if p, err := s.st.getPlaylist(ctx, v.t, *k.PlaylistID); err == nil {
			out.Items, out.Transition = p.Items, p.Transition
		}
	}
	if len(out.Items) == 0 {
		for _, id := range k.DashboardIDs {
			out.Items = append(out.Items, PlaylistItem{DashboardID: id, DurationSeconds: 30})
		}
	}
	return out, nil
}

// KioskTopics devuelve los topics WebSocket que un kiosco puede usar: los de
// los widgets de sus dashboards, dashboard.<id> de cada uno y system
// (I1-13 criterio 4). Lo consume el gateway en proceso.
func (s *Service) KioskTopics(ctx context.Context, tenantID, kioskID uuid.UUID) ([]string, error) {
	kc, ok := s.kiosks()
	if !ok {
		return nil, errors.New("dashboards: kiosks not local")
	}
	st, err := kc.CheckKiosk(ctx, tenantID, kioskID)
	if err != nil || !st.Active {
		return nil, err
	}
	t := pgdb.TenantID(tenantID)
	ids := slices.Clone(st.DashboardIDs)
	if st.PlaylistID != nil {
		if p, err := s.st.getPlaylist(ctx, t, *st.PlaylistID); err == nil {
			for _, it := range p.Items {
				ids = append(ids, it.DashboardID)
			}
		}
	}
	topics := []string{"system"}
	var types []string
	for _, id := range ids {
		d, err := s.st.getDashboard(ctx, t, id)
		if err != nil {
			continue
		}
		topics = append(topics, "dashboard."+d.ID.String())
		for _, typ := range d.WidgetTypes() {
			if wt, ok := s.catalog.Get(typ); ok && wt.KioskAllowed {
				types = append(types, typ)
			}
		}
	}
	for _, tp := range s.catalog.Topics(types) {
		if !slices.Contains(topics, tp) {
			topics = append(topics, tp)
		}
	}
	return topics, nil
}
