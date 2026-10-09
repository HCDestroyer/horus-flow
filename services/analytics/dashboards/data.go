package dashboards

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	tw "github.com/hcdestroyer/horus-flow/services/analytics/api/trafficwidgets"
)

// WidgetTimeout es el tiempo máximo por widget (api.md §2.11).
const WidgetTimeout = 10 * time.Second

const permCustomersRead = "customers.read"

// CanRead implementa dashboardsapi.Access (gateway: topic dashboard.<id>).
func (s *Service) CanRead(ctx context.Context, tenantID, userID uuid.UUID, perms map[string][]string, dashboardID uuid.UUID) (bool, error) {
	v := &viewer{p: &authz.Principal{Type: authz.TypeUser, UserID: userID, Perms: perms, TenantID: tenantID}, t: pgdb.TenantID(tenantID)}
	d, err := s.st.getDashboard(ctx, v.t, dashboardID)
	if errors.Is(err, errNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return v.canRead(d), nil
}

func (s *Service) checkType(v *viewer, typ string) (*WidgetType, error) {
	t, ok := s.catalog.Get(typ)
	if !ok {
		return nil, &apperr.Error{Kind: apperr.KindInvalid, Code: CodeWidgetTypeUnknown}
	}
	allowed := v.p.Has(t.RequiredPermission)
	if v.isKiosk() {
		allowed = t.KioskAllowed
	}
	for _, extra := range t.AdditionalPermissions {
		if !v.isKiosk() && !v.p.Has(extra) {
			allowed = false
		}
	}
	if !allowed {
		return nil, apperr.Forbidden(CodeWidgetTypeNotAllowed, "Sin el permiso del tipo de widget.")
	}
	return t, nil
}

func (s *Service) request(v *viewer, t *WidgetType, config json.RawMessage, qv url.Values, vars Variables) (tw.Request, error) {
	req := tw.Request{TenantID: v.t.UUID(), Type: t.Type, Config: config, Range: qv.Get("range")}
	if req.Range == "" {
		req.Range = vars.Range
	}
	if req.Range != "" && !slices.Contains(Ranges, req.Range) {
		return req, apperr.Validation(apperr.Field("range", "INVALID_VALUE", "15m, 1h, 6h, 24h, 7d, 30d o 90d."))
	}
	for _, k := range []string{"from", "to"} {
		if raw := qv.Get(k); raw != "" {
			ts, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return req, apperr.Validation(apperr.Field(k, "INVALID_FORMAT", "Fecha RFC 3339."))
			}
			if k == "from" {
				req.From = &ts
			} else {
				req.To = &ts
			}
		}
	}
	site := vars.SiteID
	if raw := qv.Get("site_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return req, apperr.Validation(apperr.Field("site_id", "INVALID_FORMAT", "UUID no válido."))
		}
		site = &id
	}
	if site != nil {
		req.SiteIDs = []uuid.UUID{*site}
	}
	if v.isKiosk() {
		req.ShowPersonalData = v.kiosk.ShowPersonalData
	} else {
		req.ShowPersonalData = v.p.Has(permCustomersRead)
		if !v.p.TenantWide(t.RequiredPermission) {
			req.AllowedSites = v.p.SiteScopes(t.RequiredPermission)
			if req.AllowedSites == nil {
				req.AllowedSites = []uuid.UUID{}
			}
		}
	}
	return req, nil
}

func (s *Service) resolve(ctx context.Context, req tw.Request, t *WidgetType) (*tw.WidgetData, error) {
	if t.Type == "noc_header" {
		now := s.ts()
		return &tw.WidgetData{Data: tw.Data{Kind: "state", Values: map[string]any{"now": fmtTS(now)}},
			Meta: tw.Meta{WidgetType: t.Type, DataEndpointKind: "state", GeneratedAt: now}}, nil
	}
	p, ok := s.widgets()
	if !ok || !slices.Contains(p.Types(), t.Type) {
		return nil, apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable,
			"Los datos de este tipo de widget aún no se resuelven en este proceso.")
	}
	ctx, cancel := context.WithTimeout(ctx, WidgetTimeout)
	defer cancel()
	out, err := p.Resolve(ctx, req)
	switch {
	case err == nil:
		return out, nil
	case errors.Is(err, context.DeadlineExceeded):
		return nil, &apperr.Error{Kind: apperr.KindUnavailable, Code: "TIMEOUT", Detail: "El widget tardó más de 10 s."}
	case errors.Is(err, tw.ErrUnavailable):
		return nil, apperr.New(apperr.KindUnavailable, "ANALYTICS_UNAVAILABLE", "La analítica no está disponible.")
	case errors.Is(err, tw.ErrInvalidConfig):
		return nil, &apperr.Error{Kind: apperr.KindInvalid, Code: CodeWidgetConfigInvalid, Detail: err.Error()}
	case errors.Is(err, tw.ErrUnsupportedType):
		return nil, apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable, "Tipo de widget sin proveedor de datos.")
	}
	return nil, err
}

// WidgetData es GET /dashboards/{id}/widgets/{wid}/data: con los permisos y el
// alcance de quien mira (usuario o kiosco), nunca del autor.
func (s *Service) WidgetData(ctx context.Context, id uuid.UUID, wid string, qv url.Values) (*tw.WidgetData, int, error) {
	v, d, err := s.load(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	w, _ := d.Widget(wid)
	if w == nil {
		return nil, 0, apperr.NotFound(problem.CodeNotFound)
	}
	t, err := s.checkType(v, w.Type)
	if err != nil {
		return nil, 0, err
	}
	req, err := s.request(v, t, w.Config, qv, d.Variables)
	if err != nil {
		return nil, 0, err
	}
	refresh := d.RefreshSeconds
	if w.RefreshSeconds != nil {
		refresh = *w.RefreshSeconds
	}
	out, err := s.resolve(ctx, req, t)
	return out, refresh, err
}

// PreviewInput es el cuerpo de POST /widget-data/preview.
type PreviewInput struct {
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config"`
	Range  string          `json:"range"`
}

// Preview es POST /widget-data/preview (editor; no disponible para kioscos).
func (s *Service) Preview(ctx context.Context, in PreviewInput) (*tw.WidgetData, error) {
	v, err := s.viewer(ctx)
	if err != nil {
		return nil, err
	}
	if v.isKiosk() {
		return nil, apperr.Forbidden(problem.CodeKioskForbidden, "")
	}
	t, err := s.checkType(v, in.Type)
	if err != nil {
		return nil, err
	}
	w := Widget{ID: "preview", Type: in.Type, Config: in.Config, Position: Position{W: 1, H: 1}}
	var fe fieldErrs
	if err := validateWidget(s.catalog, "widget", &w, &fe); err != nil {
		return nil, err
	}
	req, err := s.request(v, t, w.Config, url.Values{"range": {in.Range}}, Variables{})
	if err != nil {
		return nil, err
	}
	return s.resolve(ctx, req, t)
}
