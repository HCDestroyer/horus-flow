package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// Kiosks sirve las rutas de kioscos (I1-14; auth.yaml /kiosks*, /kiosk/*).
type Kiosks struct {
	h   *Handler
	svc *app.Kiosks
}

// NewKiosks crea el handler (comparte guard, orígenes y logger con h).
func NewKiosks(h *Handler, svc *app.Kiosks) *Kiosks { return &Kiosks{h: h, svc: svc} }

// Mount registra las rutas.
func (k *Kiosks) Mount(r *httpx.ServiceMux) {
	g := k.h.guard
	r.Handle("GET /api/v1/kiosks", g.Tenant(app.PermKiosksManage, k.list))
	r.Handle("POST /api/v1/kiosks", g.Tenant(app.PermKiosksManage, k.create))
	r.Handle("GET /api/v1/kiosks/{kiosk_id}", g.Tenant(app.PermKiosksManage, k.get))
	r.Handle("PATCH /api/v1/kiosks/{kiosk_id}", g.Tenant(app.PermKiosksManage, k.update))
	r.Handle("POST /api/v1/kiosks/{kiosk_id}/enrollment-codes", g.Tenant(app.PermKiosksManage, k.code))
	r.Handle("POST /api/v1/kiosks/{kiosk_id}/revoke", g.Tenant(app.PermKiosksManage, k.revoke))
	r.HandleFunc("POST /api/v1/kiosk/enroll", k.enroll)
	r.HandleFunc("POST /api/v1/kiosk/token", k.token)
}

func (k *Kiosks) fail(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := apperr.As(err); ok && e.Current != nil {
		if c, ok := e.Current.(*domain.Kiosk); ok {
			e.Current = KioskJSON(c)
		}
	}
	k.h.fail(w, r, err)
}

// KioskJSON es Kiosk del contrato (sin credenciales).
func KioskJSON(x *domain.Kiosk) map[string]any {
	cidrs := make([]string, 0, len(x.AllowedCIDRs))
	for _, c := range x.AllowedCIDRs {
		cidrs = append(cidrs, c.String())
	}
	var seen, ip any
	if x.LastSeenAt != nil {
		seen = ts(*x.LastSeenAt)
	}
	if x.LastIP != nil {
		ip = x.LastIP.String()
	}
	return map[string]any{
		"id": x.ID, "tenant_id": x.TenantID, "version": x.Version, "created_at": ts(x.CreatedAt), "updated_at": ts(x.UpdatedAt),
		"name": x.Name, "status": x.Status, "playlist_id": x.PlaylistID, "dashboard_ids": x.DashboardIDs, "allowed_cidrs": cidrs,
		"cidr_risk": len(cidrs) == 0, "show_personal_data": x.ShowPersonalData, "critical_finding_banner": x.CriticalFindingBanner,
		"expires_at": ts(x.ExpiresAt), "last_seen_at": seen, "last_ip": ip, "frontend_version": nil,
	}
}

func writeKiosk(w http.ResponseWriter, status int, x *domain.Kiosk) {
	w.Header().Set("ETag", jsonapi.ETag(x.Version))
	jsonapi.Write(w, status, KioskJSON(x))
}

func (k *Kiosks) list(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed, problem.WithDetail("limit debe estar entre 1 y 200"))
			return
		}
		limit = n
	}
	rows, page, err := k.svc.List(r.Context(), limit, r.URL.Query().Get("cursor"))
	if err != nil {
		k.fail(w, r, err)
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for i := range rows {
		data = append(data, KioskJSON(&rows[i]))
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data, "page": page})
}

func (k *Kiosks) create(w http.ResponseWriter, r *http.Request) {
	var f app.KioskFields
	if !jsonapi.Decode(w, r, &f, false) {
		return
	}
	x, err := k.svc.Create(r.Context(), f)
	if err != nil {
		k.fail(w, r, err)
		return
	}
	jsonapi.SetLocation(w, k.h.baseURL, "/api/v1/kiosks/"+x.ID.String())
	writeKiosk(w, http.StatusCreated, x)
}

func (k *Kiosks) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "kiosk_id", app.CodeKioskNotFound)
	if !ok {
		return
	}
	x, err := k.svc.Get(r.Context(), id)
	if err != nil {
		k.fail(w, r, err)
		return
	}
	writeKiosk(w, http.StatusOK, x)
}

func (k *Kiosks) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "kiosk_id", app.CodeKioskNotFound)
	if !ok {
		return
	}
	ver, err := jsonapi.IfMatch(r)
	if err != nil {
		k.fail(w, r, err)
		return
	}
	var f app.KioskFields
	if !jsonapi.Decode(w, r, &f, false) {
		return
	}
	x, err := k.svc.Update(r.Context(), id, ver, f)
	if err != nil {
		k.fail(w, r, err)
		return
	}
	writeKiosk(w, http.StatusOK, x)
}

func (k *Kiosks) code(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "kiosk_id", app.CodeKioskNotFound)
	if !ok {
		return
	}
	if !k.h.requireReauth(w, r) {
		return
	}
	code, qr, exp, err := k.svc.EnrollmentCode(r.Context(), id)
	if err != nil {
		k.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusCreated, map[string]any{"code": code, "qr_url": qr, "expires_at": ts(exp)})
}

func (k *Kiosks) revoke(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "kiosk_id", app.CodeKioskNotFound)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 && !jsonapi.Decode(w, r, &body, false) {
		return
	}
	if len(body.Reason) > 200 {
		body.Reason = body.Reason[:200]
	}
	x, err := k.svc.Revoke(r.Context(), id, body.Reason)
	if err != nil {
		k.fail(w, r, err)
		return
	}
	writeKiosk(w, http.StatusOK, x)
}

func setKioskCookie(w http.ResponseWriter, value string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: domain.KioskCookie, Value: value, Path: domain.KioskCookiePath, Expires: exp,
		MaxAge: int(time.Until(exp).Seconds()), Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

func clearKioskCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: domain.KioskCookie, Value: "", Path: domain.KioskCookiePath, MaxAge: -1, Secure: true,
		HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

func (k *Kiosks) enroll(w http.ResponseWriter, r *http.Request) {
	if !k.h.origins.CheckCookieRequest(w, r) {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	cred, x, err := k.svc.Enroll(r.Context(), body.Code, clientIP(r))
	if err != nil {
		k.fail(w, r, err)
		return
	}
	setKioskCookie(w, cred, x.ExpiresAt)
	w.WriteHeader(http.StatusNoContent)
}

func (k *Kiosks) token(w http.ResponseWriter, r *http.Request) {
	if !k.h.origins.CheckCookieRequest(w, r) {
		return
	}
	cred := ""
	if c, err := r.Cookie(domain.KioskCookie); err == nil {
		cred = c.Value
	}
	t, err := k.svc.Token(r.Context(), cred, clientIP(r))
	if err != nil {
		if e, ok := apperr.As(err); ok && e.Kind == apperr.KindUnauthorized {
			clearKioskCookie(w)
		}
		k.fail(w, r, err)
		return
	}
	setKioskCookie(w, t.Credential, t.CookieExp)
	tid := t.TenantID
	jsonapi.Write(w, http.StatusOK, tokenResponse{AccessToken: t.Access, TokenType: "Bearer", ExpiresAt: ts(t.ExpiresAt),
		Scope: authz.ScopeKiosk, TenantID: &tid})
}
