package main

import (
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/platformevents"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// PermPlatformEvents es el permiso de GET /platform/events (estado
// detallado de la instalación, rol de plataforma).
const PermPlatformEvents = "platform.status.read"

type platformAPIConfig struct {
	CursorKey  observability.Secret `env:"HORUS_PLATFORM_CURSOR_KEY"`
	PublicKeys string               `env:"HORUS_JWT_PUBLIC_KEYS"`
	Issuer     string               `env:"HORUS_AUTH_ISSUER" envDefault:"horus-auth"`
}

// mountAPI monta GET /api/v1/platform/events (contrato v0, platform.yaml)
// si el proceso sirve la API de plataforma (rol gateway) y tiene registro en
// PostgreSQL. El borde del gateway ya exige token de plataforma y permiso;
// el guard lo revalida (defensa en profundidad).
func (p *platform) mountAPI(api *httpx.Mux, services *module.Services) {
	if p.store == nil || !slices.Contains(p.roles, "gateway") {
		return
	}
	cfg, err := config.Load[platformAPIConfig](p.environ)
	if err != nil {
		p.logger.Warn("platform events API disabled: config", slog.Any("error", err))
		return
	}
	verifier, ok := module.Lookup[*authz.Verifier](services, authapi.ServiceVerifier)
	if !ok && cfg.PublicKeys != "" {
		keys, err := authz.ParsePublicKeysPEM([]byte(cfg.PublicKeys))
		if err == nil {
			verifier, ok = authz.NewVerifier(keys, cfg.Issuer, nil), true
		}
	}
	if !ok {
		p.logger.Warn("platform events API disabled: no token verifier")
		return
	}
	h := &eventsHandler{store: p.store, cursor: pagination.NewCodec([]byte(cfg.CursorKey.Reveal())), logger: p.logger}
	api.ForService("gateway").Handle("GET /api/v1/platform/events", authz.NewGuard(verifier).Platform(PermPlatformEvents, h.list))
}

type eventsHandler struct {
	store  *platformevents.Store
	cursor *pagination.Codec
	logger *slog.Logger
}

func (h *eventsHandler) list(w http.ResponseWriter, r *http.Request) {
	qv := r.URL.Query()
	req, err := pagination.ParseRequest(qv)
	if err != nil {
		problem.Std(w, r, http.StatusBadRequest, problem.CodeInvalidFilter, problem.WithDetail("limit: 1..200"))
		return
	}
	q := platformevents.Query{Limit: req.Limit + 1, Process: qv.Get("process"), Role: qv.Get("role"), Severity: qv.Get("severity")}
	if k := qv.Get("kind"); k != "" {
		for _, kind := range strings.Split(k, ",") {
			if !slices.Contains(platformevents.Kinds, kind) {
				problem.Std(w, r, http.StatusBadRequest, problem.CodeInvalidFilter, problem.WithDetail("kind: "+kind))
				return
			}
			q.Kinds = append(q.Kinds, kind)
		}
	}
	switch q.Severity {
	case "", platformevents.SeverityInfo, platformevents.SeverityWarn, platformevents.SeverityError:
	default:
		problem.Std(w, r, http.StatusBadRequest, problem.CodeInvalidFilter, problem.WithDetail("severity: info, warn o error"))
		return
	}
	for name, dst := range map[string]**time.Time{"since": &q.Since, "until": &q.Until} {
		if v := qv.Get(name); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				problem.Std(w, r, http.StatusBadRequest, problem.CodeInvalidFilter, problem.WithDetail(name+": RFC 3339"))
				return
			}
			*dst = &t
		}
	}
	fk := pagination.FilterKey(qv.Get("kind"), q.Severity, q.Process, q.Role, qv.Get("since"), qv.Get("until"))
	if req.Cursor != "" {
		cur, err := h.cursor.Decode(req.Cursor, "-occurred_at", fk, "platform")
		if err != nil || len(cur.Keys) != 2 {
			problem.Std(w, r, http.StatusBadRequest, problem.CodeInvalidCursor)
			return
		}
		at, e1 := time.Parse(time.RFC3339Nano, cur.Keys[0])
		id, e2 := uuid.Parse(cur.Keys[1])
		if e1 != nil || e2 != nil {
			problem.Std(w, r, http.StatusBadRequest, problem.CodeInvalidCursor)
			return
		}
		q.Before, q.BeforeID = &at, id
	}
	rows, err := h.store.List(r.Context(), q)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "platform events: list", slog.Any("error", err))
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
		return
	}
	pg := pagination.Page{Limit: req.Limit}
	if len(rows) > req.Limit {
		rows = rows[:req.Limit]
		last := rows[len(rows)-1]
		next := h.cursor.Encode(pagination.Cursor{Keys: []string{last.OccurredAt.UTC().Format(time.RFC3339Nano), last.ID.String()},
			Sort: "-occurred_at", Filter: fk, Tenant: "platform"})
		pg.NextCursor, pg.HasMore = &next, true
	}
	if rows == nil {
		rows = []platformevents.Event{}
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": rows, "page": pg})
}
