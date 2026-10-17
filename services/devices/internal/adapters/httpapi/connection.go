package httpapi

import (
	"net/http"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/clientip"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// reauthMaxAge es la antigüedad máxima de auth_time en las rutas 🔒 que
// emiten credenciales (el gateway ya lo aplica; aquí, defensa en
// profundidad).
const reauthMaxAge = 5 * time.Minute

// maxRedeemBody acota el cuerpo del canje público.
const maxRedeemBody = 64 << 10

func freshAuth(w http.ResponseWriter, r *http.Request) bool {
	if authz.FromContext(r.Context()).AuthAge(time.Now()) > reauthMaxAge {
		problem.Std(w, r, http.StatusForbidden, problem.CodeReauthRequired)
		return false
	}
	return true
}

func (h *Integrations) createLink(w http.ResponseWriter, r *http.Request) {
	if !freshAuth(w, r) {
		return
	}
	l, err := h.svc.CreateConnectionLink(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusCreated, map[string]any{"url": l.URL, "expires_at": ts(l.ExpiresAt),
		"ttl_seconds": int(domain.ConnectionLinkTTL / time.Second)})
}

func (h *Integrations) redeemLink(w http.ResponseWriter, r *http.Request) {
	if !freshAuth(w, r) {
		return
	}
	var body struct {
		Link string `json:"link"`
	}
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	v, err := h.svc.RedeemLink(r.Context(), body.Link)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, h.connectorJSON(v))
}

// redeemCode es el canje público del enlace (ArgosISP, servidor a servidor).
func (h *Integrations) redeemCode(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRedeemBody)
	var in app.RedeemCodeInput
	if !jsonapi.Decode(w, r, &in, false) {
		return
	}
	body, replay, err := h.svc.RedeemConnectionCode(r.Context(), in, clientip.String(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if replay {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *Integrations) issueTicket(w http.ResponseWriter, r *http.Request) {
	var in app.SSOTicketInput
	if !jsonapi.Decode(w, r, &in, false) {
		return
	}
	res, err := h.svc.IssueSSOTicket(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusCreated, map[string]any{"url": res.URL, "expira_en": ts(res.ExpiresAt)})
}

func (h *Integrations) resolveTarget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type       string `json:"type"`
		ExternalID string `json:"external_id"`
	}
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	res, err := h.svc.ResolveSSOTarget(r.Context(), body.Type, body.ExternalID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"kind": res.Kind, "id": res.ID})
}

func (h *Integrations) openArgos(w http.ResponseWriter, r *http.Request) {
	var in app.OpenArgosInput
	if !jsonapi.Decode(w, r, &in, false) {
		return
	}
	res, err := h.svc.OpenArgos(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var exp any
	if !res.ExpiresAt.IsZero() {
		exp = ts(res.ExpiresAt)
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusOK, map[string]any{"url": res.URL, "expires_at": exp})
}
