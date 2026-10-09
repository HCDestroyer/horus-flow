package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Customers sirve las rutas de clientes (I1-06; devices.yaml /customers*).
type Customers struct {
	svc    *app.Customers
	guard  *authz.Guard
	logger *slog.Logger
}

// NewCustomers crea el handler.
func NewCustomers(svc *app.Customers, guard *authz.Guard, logger *slog.Logger) *Customers {
	return &Customers{svc: svc, guard: guard, logger: logger}
}

// Mount registra las rutas. La IP nunca va en la URL: la búsqueda es POST.
func (h *Customers) Mount(r *httpx.ServiceMux) {
	r.Handle("GET /api/v1/customers", h.guard.Tenant(app.PermCustomersRead, h.list))
	r.Handle("POST /api/v1/customers/lookup", h.guard.Tenant(app.PermCustomersRead, h.lookup))
	r.Handle("GET /api/v1/customers/stats", h.guard.Wrap(authz.Requirement{Scope: authz.ScopeTenant, AllowKiosk: true}, http.HandlerFunc(h.stats)))
	r.Handle("GET /api/v1/customers/{customer_id}", h.guard.Tenant(app.PermCustomersRead, h.get))
	r.Handle("PATCH /api/v1/customers/{customer_id}", h.guard.Tenant(app.PermCustomersUpdate, h.update))
	r.Handle("POST /api/v1/customers/{customer_id}/set-kind", h.guard.Tenant(app.PermCustomersKindWrite, h.setKind))
	r.Handle("POST /api/v1/customers/{customer_id}/unlock-kind", h.guard.Tenant(app.PermCustomersKindWrite, h.unlockKind))
	r.Handle("POST /api/v1/customers/{customer_id}/reset", h.guard.Tenant(app.PermCustomersKindWrite, h.reset))
	r.Handle("GET /api/v1/customers/{customer_id}/kind-history", h.guard.Tenant(app.PermCustomersRead, h.history))
}

func (h *Customers) fail(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := apperr.As(err); ok && e.Current != nil {
		if c, ok := e.Current.(*domain.Customer); ok {
			e.Current = CustomerDetailJSON(c)
		}
	}
	apperr.WriteHTTP(w, r, h.logger, err)
}

// CustomerJSON es Customer del contrato.
func CustomerJSON(c *domain.Customer) map[string]any {
	var kca any
	if c.KindChangedAt != nil {
		kca = ts(*c.KindChangedAt)
	}
	var reset any
	if c.ResetAt != nil {
		reset = ts(*c.ResetAt)
	}
	return map[string]any{
		"id": c.ID, "tenant_id": c.TenantID, "version": c.Version, "address": c.AddressString(), "realm_id": c.RealmID,
		"site_id": c.SiteID, "client_prefix_id": c.ClientPrefixID, "status": c.Status, "inactive_reason": c.InactiveReason,
		"kind": c.Kind, "kind_source": c.KindSource, "kind_locked": c.KindLocked, "kind_confidence": c.KindConfidence,
		"kind_changed_at": kca, "commercial_use_suspected": c.CommercialUseSuspected, "security_state": c.SecurityState,
		"open_findings": c.OpenFindings, "alias": c.Alias, "alias_source": c.AliasSource, "first_seen": ts(c.FirstSeen),
		"last_seen": ts(c.LastSeen), "reset_at": reset, "traffic_24h": nil,
	}
}

// CustomerDetailJSON añade notas y la sugerencia del scoring.
func CustomerDetailJSON(c *domain.Customer) map[string]any {
	m := CustomerJSON(c)
	m["notes"] = c.Notes
	m["suggested_kind"] = nil
	m["suggested_confidence"] = nil
	m["suggested_reasons"] = []any{}
	return m
}

func kindChangeJSON(k *domain.KindChange) map[string]any {
	reasons := json.RawMessage(k.Reasons)
	if len(reasons) == 0 {
		reasons = json.RawMessage("[]")
	}
	codes := k.ReasonCodes
	if codes == nil {
		codes = []string{}
	}
	return map[string]any{
		"id": k.ID, "changed_at": ts(k.ChangedAt), "from_kind": k.FromKind, "to_kind": k.ToKind, "source": k.Source,
		"reasons": reasons, "reason_codes": codes, "confidence": k.Confidence, "model_ref": k.ModelRef, "actor_id": k.ActorID,
		"manual_reason": k.ManualReason,
	}
}

func (h *Customers) list(w http.ResponseWriter, r *http.Request) {
	page, err := h.svc.List(r.Context(), r.URL.Query())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": listJSON(page.Data, CustomerJSON), "page": page.Page})
}

func (h *Customers) lookup(w http.ResponseWriter, r *http.Request) {
	var in app.LookupInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	page, err := h.svc.Lookup(r.Context(), r.URL.Query(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": listJSON(page.Data, CustomerJSON), "page": page.Page})
}

func (h *Customers) stats(w http.ResponseWriter, r *http.Request) {
	st, at, err := h.svc.Stats(r.Context(), r.URL.Query())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	sites := make([]map[string]any, 0, len(st.BySite))
	for _, s := range st.BySite {
		sites = append(sites, map[string]any{"site_id": s.SiteID, "active": s.Active, "with_open_findings": s.WithOpenFindings})
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{
		"total": st.Total, "active": st.Active, "new_today": st.NewToday, "by_kind": st.ByKind, "by_kind_source": st.ByKindSource,
		"by_status": st.ByStatus, "by_security_state": st.BySecurityState, "by_site": sites, "generated_at": ts(at),
	})
}

func (h *Customers) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "customer_id", domain.CodeCustomerNotFound)
	if !ok {
		return
	}
	c, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeItem(w, http.StatusOK, c.Version, CustomerDetailJSON(c))
}

func (h *Customers) ifMatch(w http.ResponseWriter, r *http.Request) (int, bool) {
	v, err := jsonapi.IfMatch(r)
	if err != nil {
		h.fail(w, r, err)
		return 0, false
	}
	return v, true
}

func (h *Customers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "customer_id", domain.CodeCustomerNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	var f app.Fields
	if !jsonapi.Decode(w, r, &f, false) {
		return
	}
	c, err := h.svc.Update(r.Context(), id, ver, f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeItem(w, http.StatusOK, c.Version, CustomerDetailJSON(c))
}

func (h *Customers) setKind(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "customer_id", domain.CodeCustomerNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	var in app.SetKindInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	c, err := h.svc.SetKind(r.Context(), id, ver, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeItem(w, http.StatusOK, c.Version, CustomerDetailJSON(c))
}

func (h *Customers) unlockKind(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "customer_id", domain.CodeCustomerNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	c, err := h.svc.UnlockKind(r.Context(), id, ver)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeItem(w, http.StatusOK, c.Version, CustomerDetailJSON(c))
}

func (h *Customers) reset(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "customer_id", domain.CodeCustomerNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !jsonapi.Decode(w, r, &in, false) {
		return
	}
	c, err := h.svc.Reset(r.Context(), id, ver, in.Reason)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeItem(w, http.StatusOK, c.Version, CustomerDetailJSON(c))
}

func (h *Customers) history(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "customer_id", domain.CodeCustomerNotFound)
	if !ok {
		return
	}
	page, err := h.svc.KindHistory(r.Context(), id, r.URL.Query())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": listJSON(page.Data, kindChangeJSON), "page": page.Page})
}
