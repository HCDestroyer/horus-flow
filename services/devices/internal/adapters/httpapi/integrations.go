package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Integrations sirve las rutas de integraciones (ADR-0030; devices.yaml
// /integrations/*, /customers/{id}/external-identity*).
type Integrations struct {
	svc    *app.Integrations
	guard  *authz.Guard
	logger *slog.Logger
}

// NewIntegrations crea el handler.
func NewIntegrations(svc *app.Integrations, guard *authz.Guard, logger *slog.Logger) *Integrations {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Integrations{svc: svc, guard: guard, logger: logger}
}

// Mount registra las rutas. Las IPs de clientes van en el cuerpo.
func (h *Integrations) Mount(r *httpx.ServiceMux) {
	r.Handle("POST /api/v1/integrations/ip-lookup", h.guard.Tenant(app.PermCustomersRead, h.ipLookup))
	r.Handle("POST /api/v1/integrations/consumption", h.guard.Tenant(app.PermTrafficCustomerRead, h.consumption))
	r.Handle("POST /api/v1/integrations/security-states", h.guard.Tenant(app.PermFindingsRead, h.securityStates))
	r.Handle("GET /api/v1/integrations/argosisp", h.guard.Tenant(app.PermSettingsRead, h.getConnector))
	r.Handle("PUT /api/v1/integrations/argosisp", h.guard.Tenant(app.PermSettingsManage, h.putConnector))
	r.Handle("POST /api/v1/integrations/argosisp/webhook-secret", h.guard.Tenant(app.PermSettingsManage, h.rotateSecret))
	r.Handle("POST /api/v1/integrations/argosisp/connection-test", h.guard.Tenant(app.PermSettingsManage, h.test))
	r.HandleFunc("POST /api/v1/integrations/argosisp/webhook/{connector_id}", h.webhook)
	r.Handle("GET /api/v1/customers/{customer_id}/external-identity", h.guard.Tenant(app.PermCustomersRead, h.identity))
	r.Handle("POST /api/v1/customers/{customer_id}/external-identity/refresh", h.guard.Tenant(app.PermCustomersUpdate, h.refresh))
}

func (h *Integrations) fail(w http.ResponseWriter, r *http.Request, err error) {
	apperr.WriteHTTP(w, r, h.logger, err)
}

// ExternalRefJSON es ExternalRef del contrato.
func ExternalRefJSON(i *domain.ExternalIdentity) any {
	if i == nil {
		return nil
	}
	return map[string]any{"system": i.System, "customer_id": i.ExternalCustomerID, "service_id": i.ExternalServiceID, "code": i.Code,
		"display_name": i.DisplayName, "service_state": i.ServiceState, "plan": i.Plan, "company_id": i.ExternalCompanyID,
		"company_name": i.CompanyName, "synced_at": ts(i.SyncedAt), "stale": i.Stale}
}

func periodJSON(i *domain.ExternalIdentity) map[string]any {
	return map[string]any{"id": i.ID, "system": i.System, "external_customer_id": i.ExternalCustomerID,
		"external_service_id": i.ExternalServiceID, "display_name": i.DisplayName, "code": i.Code, "service_state": i.ServiceState,
		"plan": i.Plan, "external_company_id": i.ExternalCompanyID, "company_name": i.CompanyName, "company_nit": i.CompanyNIT,
		"valid_from": ts(i.ValidFrom), "valid_to": tsPtr(i.ValidTo), "synced_at": ts(i.SyncedAt), "stale": i.Stale}
}

func identityJSON(v *app.IdentityView) map[string]any {
	hist := make([]map[string]any, 0, len(v.History))
	for i := range v.History {
		hist = append(hist, periodJSON(&v.History[i]))
	}
	return map[string]any{"customer_id": v.CustomerID, "connector_enabled": v.ConnectorEnabled, "current": ExternalRefJSON(v.Current),
		"history": hist, "last_checked_at": tsPtr(v.LastCheckedAt), "last_check_status": v.LastCheckStatus}
}

func (h *Integrations) connectorJSON(v *app.ConnectorView) map[string]any {
	c := v.Connector
	if c == nil {
		return map[string]any{"id": nil, "configured": false, "enabled": false, "base_url": nil, "has_token": false, "webhook_url": nil,
			"has_webhook_secret": false, "last_sync_at": nil, "last_sync_status": nil, "last_error": nil, "resolver": nil,
			"identities": 0, "stale_identities": 0, "version": 0}
	}
	return map[string]any{"id": c.ID, "configured": true, "enabled": c.Enabled, "base_url": c.BaseURL, "has_token": c.HasToken(),
		"webhook_url": h.svc.WebhookURL(c), "has_webhook_secret": c.HasSecret(), "last_sync_at": tsPtr(c.LastSyncAt),
		"last_sync_status": c.LastSyncStatus, "last_error": c.LastError, "resolver": c.Resolver, "identities": v.Identities,
		"stale_identities": v.Stale, "version": c.Version}
}

func (h *Integrations) getConnector(w http.ResponseWriter, r *http.Request) {
	v, err := h.svc.GetConnector(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if v.Connector != nil {
		w.Header().Set("ETag", jsonapi.ETag(v.Connector.Version))
	}
	jsonapi.Write(w, http.StatusOK, h.connectorJSON(v))
}

func (h *Integrations) putConnector(w http.ResponseWriter, r *http.Request) {
	var in app.ConnectorInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	v, err := h.svc.PutConnector(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, h.connectorJSON(v))
}

func (h *Integrations) rotateSecret(w http.ResponseWriter, r *http.Request) {
	u, sec, err := h.svc.RotateWebhookSecret(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusOK, map[string]any{"webhook_url": u, "webhook_secret": sec})
}

func (h *Integrations) test(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.TestConnector(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"ok": res.OK, "tested_at": ts(res.TestedAt), "latency_ms": res.LatencyMS,
		"resolver": res.Resolver, "error_code": res.ErrorCode, "error": res.Error})
}

// maxWebhookBody es el tope del cuerpo de un webhook (contrato: 1 MiB).
const maxWebhookBody = 1 << 20

func (h *Integrations) webhook(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("connector_id"))
	if err != nil {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		problem.Std(w, r, http.StatusRequestEntityTooLarge, problem.CodePayloadTooLarge)
		return
	}
	switch err := h.svc.ReceiveWebhook(r.Context(), id, r.Header, body); {
	case err == nil:
		w.WriteHeader(http.StatusAccepted)
	case errors.Is(err, app.ErrWebhookUnknown):
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
	case errors.Is(err, app.ErrWebhookRejected):
		problem.Std(w, r, http.StatusUnauthorized, problem.CodeUnauthenticated, problem.WithDetail("Firma del webhook no válida o fuera de plazo."))
	default:
		h.fail(w, r, err)
	}
}

func (h *Integrations) customerID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("customer_id"))
	if err != nil {
		problem.Std(w, r, http.StatusNotFound, domain.CodeCustomerNotFound)
		return uuid.Nil, false
	}
	return id, true
}

func (h *Integrations) identity(w http.ResponseWriter, r *http.Request) {
	id, ok := h.customerID(w, r)
	if !ok {
		return
	}
	v, err := h.svc.ExternalIdentity(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, identityJSON(v))
}

func (h *Integrations) refresh(w http.ResponseWriter, r *http.Request) {
	id, ok := h.customerID(w, r)
	if !ok {
		return
	}
	v, err := h.svc.RefreshIdentity(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, identityJSON(v))
}

func (h *Integrations) ipLookup(w http.ResponseWriter, r *http.Request) {
	var in app.IPLookupInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	res, err := h.svc.IPLookup(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	c := &res.Customer
	out := map[string]any{
		"customer": map[string]any{"id": c.ID, "address": c.AddressString(), "alias": c.Alias, "alias_source": c.AliasSource, "kind": c.Kind,
			"kind_source": c.KindSource, "status": c.Status, "first_seen": ts(c.FirstSeen), "last_seen": ts(c.LastSeen),
			"site": map[string]any{"id": c.SiteID, "name": res.SiteName}},
		"external": ExternalRefJSON(res.External), "consumption": nil, "health": nil, "security": nil,
		"meta": map[string]any{"at": ts(res.At), "omitted": res.Omitted},
	}
	if s := res.Consumption; s != nil {
		top := make([]map[string]any, 0, len(s.TopServices7d))
		for _, x := range s.TopServices7d {
			top = append(top, map[string]any{"name": x.Name, "bytes": x.Bytes})
		}
		pair := func(up, down uint64) map[string]any { return map[string]any{"up_bytes": up, "down_bytes": down} }
		out["consumption"] = map[string]any{"last_24h": pair(s.Last24h.UpBytes, s.Last24h.DownBytes),
			"last_7d": pair(s.Last7d.UpBytes, s.Last7d.DownBytes), "last_30d": pair(s.Last30d.UpBytes, s.Last30d.DownBytes),
			"peak_down_bps_24h": s.PeakDownBps24h, "top_services_7d": top}
	}
	if hl := res.Health; hl != nil {
		var router any
		if rt := hl.Router; rt != nil {
			router = map[string]any{"id": rt.ID, "name": rt.Name, "status": rt.Status, "since": tsPtr(rt.Since)}
		}
		out["health"] = map[string]any{"router": router, "exporter": map[string]any{"status": hl.ExporterStatus, "last_flow_at": tsPtr(hl.LastFlowAt)},
			"last_seen_minutes": hl.LastSeenMinutes}
	}
	if sec := res.Security; sec != nil {
		fs := make([]map[string]any, 0, len(sec.Findings))
		for _, f := range sec.Findings {
			fs = append(fs, map[string]any{"id": f.ID, "kind": f.Kind, "severity": f.Severity, "state": f.State, "opened_at": ts(f.OpenedAt),
				"summary": f.Summary})
		}
		out["security"] = map[string]any{"state": sec.State, "open_findings": sec.OpenFindings, "findings": fs}
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusOK, out)
}

func (h *Integrations) consumption(w http.ResponseWriter, r *http.Request) {
	var in app.ConsumptionInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	res, err := h.svc.Consumption(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(res.Items))
	for _, it := range res.Items {
		series := make([]map[string]any, 0, len(it.Series))
		for _, d := range it.Series {
			series = append(series, map[string]any{"day": d.Day.Format(time.DateOnly), "up_bytes": d.UpBytes, "down_bytes": d.DownBytes})
		}
		row := map[string]any{"address": it.Address, "external_service_id": it.ExternalServiceID, "up_bytes": it.UpBytes,
			"down_bytes": it.DownBytes, "series": series}
		if it.Addresses != nil {
			row["addresses"] = it.Addresses
		}
		if it.Error != nil {
			row["error"] = *it.Error
		}
		items = append(items, row)
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusOK, map[string]any{"items": items,
		"meta": map[string]any{"from": ts(res.From), "to": ts(res.To), "granularity": res.Granularity}})
}

func (h *Integrations) securityStates(w http.ResponseWriter, r *http.Request) {
	var in app.SecurityStatesInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	res, err := h.svc.SecurityStates(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(res.Items))
	for _, it := range res.Items {
		row := map[string]any{"address": it.Address, "external_service_id": it.ExternalServiceID}
		if it.Error != nil {
			row["error"] = *it.Error
		} else {
			row["state"], row["open_findings"], row["max_severity"], row["since"] = it.State, it.OpenFindings, it.MaxSeverity, tsPtr(it.Since)
		}
		items = append(items, row)
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusOK, map[string]any{"items": items, "meta": map[string]any{"at": ts(res.At)}})
}
