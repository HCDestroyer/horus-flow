package notify

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/alerts/internal/librenms"
)

func (h *Handler) mountLibreNMS(r *httpx.ServiceMux) {
	r.Handle("GET /api/v1/notification-channels/{channel_id}/librenms", h.guard.Tenant(PermRead, h.librenmsGet))
	r.Handle("POST /api/v1/notification-channels/{channel_id}/librenms/sync", h.guard.Tenant(PermManage, h.librenmsSync))
	r.Handle("POST /api/v1/notification-channels/{channel_id}/librenms/test", h.guard.Tenant(PermManage, h.librenmsTest))
	r.Handle("DELETE /api/v1/notification-channels/{channel_id}/librenms/devices/{router_id}", h.guard.Tenant(PermManage, h.librenmsDeleteDevice))
	r.Handle("GET /api/v1/librenms/routers", h.guard.Tenant(PermRead, h.librenmsRouters))
	r.Handle("PUT /api/v1/librenms/routers/{router_id}", h.guard.Tenant(PermManage, h.librenmsSetRouter))
	r.Handle("DELETE /api/v1/librenms/routers/{router_id}", h.guard.Tenant(PermManage, h.librenmsClearRouter))
	r.Handle("PUT /api/v1/librenms/routers/{router_id}/snmp-credentials", h.guard.Tenant(PermManage, h.librenmsRouterCredentials))
}

func objectJSON(o librenms.Object) map[string]any {
	var host any
	if o.Hostname != "" {
		host = o.Hostname
	}
	return map[string]any{"kind": o.Kind, "key": o.Key, "name": o.Name, "remote_id": int64(o.RemoteID), "hostname": host,
		"created_by_horus": o.CreatedByHorus, "active": o.Active}
}

func strPtr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func integrationJSON(v *IntegrationView) map[string]any {
	objs := make([]map[string]any, 0, len(v.Objects))
	for _, o := range v.Objects {
		objs = append(objs, objectJSON(o))
	}
	var test any
	if v.LastTest != nil {
		test = v.LastTest
	}
	return map[string]any{
		"channel_id": v.ChannelID, "status": v.Status, "horus_hostname": v.HorusHostname, "horus_hostname_default": strPtr(v.DefaultHost),
		"cpu_threshold": v.CPU, "memory_threshold": v.Memory, "tz_offset_minutes": v.TZOffset, "remote_version": v.RemoteVersion,
		"last_sync_at": tsPtr(v.LastSyncAt), "last_error_code": v.LastErrorCode, "last_error": v.LastError,
		"warnings": nonNil(v.Warnings), "objects": objs, "last_sync_steps": nonNilSteps(v.Steps), "last_test": test,
	}
}

func routerJSON(v *RouterView) map[string]any {
	out := map[string]any{"router_id": v.Router.ID, "router_name": v.Router.Name, "site_id": v.Router.SiteID,
		"onboarding_state": v.Router.OnboardingState, "mgmt_ip": nil, "mode": librenms.ModePing, "has_snmp_credentials": false,
		"horus_snmp_reuse_consent": false, "updated_at": nil}
	if c := v.Config; c != nil {
		out["mgmt_ip"], out["mode"], out["has_snmp_credentials"] = c.MgmtIP, c.Mode, c.HasSecret()
		out["horus_snmp_reuse_consent"] = c.ConsentAt != nil
		out["updated_at"] = ts(c.UpdatedAt)
	}
	return out
}

func routerPathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("router_id"))
	if err != nil {
		problem.Std(w, r, http.StatusNotFound, codeRouterNotFound)
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) librenmsGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	v, err := h.svc.LibreNMSIntegration(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, integrationJSON(v))
}

func (h *Handler) librenmsSync(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in SyncInput
	if r.ContentLength != 0 && !jsonapi.Decode(w, r, &in, true) {
		return
	}
	out, err := h.svc.LibreNMSSync(r.Context(), id, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	steps := out.Steps
	if steps == nil {
		steps = []librenms.Step{}
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"ok": out.OK, "error_code": out.ErrorCode, "error": out.Error, "steps": steps,
		"warnings": nonNil(out.Warnings), "integration": integrationJSON(out.View)})
}

func (h *Handler) librenmsTest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rep, err := h.svc.LibreNMSTest(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, rep)
}

func (h *Handler) librenmsDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rid, ok := routerPathID(w, r)
	if !ok {
		return
	}
	if err := h.svc.LibreNMSDeleteRouterDevice(r.Context(), id, rid); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) librenmsRouters(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.LibreNMSRouters(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for i := range rows {
		data = append(data, routerJSON(&rows[i]))
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data})
}

func (h *Handler) librenmsSetRouter(w http.ResponseWriter, r *http.Request) {
	rid, ok := routerPathID(w, r)
	if !ok {
		return
	}
	var in RouterInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	v, err := h.svc.SetLibreNMSRouter(r.Context(), rid, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, routerJSON(v))
}

func (h *Handler) librenmsClearRouter(w http.ResponseWriter, r *http.Request) {
	rid, ok := routerPathID(w, r)
	if !ok {
		return
	}
	if err := h.svc.ClearLibreNMSRouter(r.Context(), rid); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) librenmsRouterCredentials(w http.ResponseWriter, r *http.Request) {
	rid, ok := routerPathID(w, r)
	if !ok {
		return
	}
	if authz.FromContext(r.Context()).AuthAge(time.Now()) > ReauthMaxAge {
		problem.Std(w, r, http.StatusForbidden, problem.CodeReauthRequired)
		return
	}
	var in RouterSecret
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	if err := h.svc.PutLibreNMSRouterCredentials(r.Context(), rid, in); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
