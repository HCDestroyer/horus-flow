package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// ImportHandler sirve la importación de prefijos desde el MikroTik (I1-28).
type ImportHandler struct {
	im     *app.Importer
	guard  *authz.Guard
	logger *slog.Logger
}

// NewImport crea el handler.
func NewImport(im *app.Importer, guard *authz.Guard, logger *slog.Logger) *ImportHandler {
	return &ImportHandler{im: im, guard: guard, logger: logger}
}

// Mount registra las rutas.
func (h *ImportHandler) Mount(r *httpx.ServiceMux) {
	r.Handle("POST /api/v1/routers/{router_id}/prefix-import-preview", h.guard.Tenant("sites.update", h.preview))
	r.Handle("POST /api/v1/sites/{site_id}/client-prefixes/batch", h.guard.Tenant("sites.update", h.batch))
}

func importItemJSON(it domain.ImportItem) map[string]any {
	return map[string]any{
		"prefix": it.Prefix.String(), "origin": it.Origin, "origin_name": it.OriginName, "suggested_role": it.SuggestedRole,
		"suggested_assignment_mode": it.SuggestedAssignmentMode, "delegated_prefix_length": it.DelegatedPrefixLength,
		"suggested_ipv6_client_len": it.SuggestedIPv6ClientLen, "ipv6_pool_usage": it.IPv6PoolUsage, "diff": it.Diff,
		"existing_client_prefix_id": it.ExistingID,
	}
}

func (h *ImportHandler) preview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "router_id", domain.CodeRouterNotFound)
	if !ok {
		return
	}
	var in struct {
		Accept *string `json:"accept_new_tls_fingerprint"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, jsonapi.MaxBody))
	if err != nil {
		problem.Std(w, r, http.StatusRequestEntityTooLarge, problem.CodePayloadTooLarge)
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		r.Body = io.NopCloser(bytes.NewReader(body))
		if !jsonapi.Decode(w, r, &in, false) {
			return
		}
	}
	pv, err := h.im.PreviewImport(r.Context(), id, in.Accept)
	if err != nil {
		apperr.WriteHTTP(w, r, h.logger, err)
		return
	}
	items := make([]map[string]any, 0, len(pv.Items))
	for _, it := range pv.Items {
		items = append(items, importItemJSON(it))
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"router_id": pv.RouterID, "read_at": ts(pv.ReadAt), "routeros_version": pv.Version,
		"tls_fingerprint_sha256": pv.TLSFingerprint, "items": items})
}

func (h *ImportHandler) batch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "site_id", domain.CodeSiteNotFound)
	if !ok {
		return
	}
	var in struct {
		Items json.RawMessage `json:"items"`
	}
	if !jsonapi.Decode(w, r, &in, false) {
		return
	}
	items, err := app.DecodeItems(in.Items)
	if err != nil {
		apperr.WriteHTTP(w, r, h.logger, err)
		return
	}
	created, err := h.im.BatchCreatePrefixes(r.Context(), id, items)
	if err != nil {
		apperr.WriteHTTP(w, r, h.logger, err)
		return
	}
	out := make([]map[string]any, 0, len(created))
	for _, p := range created {
		out = append(out, prefixJSON(p))
	}
	jsonapi.Write(w, http.StatusCreated, map[string]any{"data": out})
}
