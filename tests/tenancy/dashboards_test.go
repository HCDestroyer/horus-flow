//go:build integration

package tenancy

import (
	"context"
	"testing"
)

const nocTemplate = "0192f000-0000-7000-8000-00000000d001"

// I1-15: plantillas sembradas, catálogo, validación 422, solo lectura de
// plantillas, playlists asignadas a kioscos (GET /kiosk/config y evento) y
// acceso del kiosco solo a sus dashboards.
func TestDashboardsPlaylistsAndKiosk(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	pt := a.superadmin().platformToken()
	A := a.newISP(pt, "isp-a")

	// Criterio 2: catálogo con permisos y marcas de kiosco/datos personales.
	wt := a.must(a.do(req{Method: "GET", Path: "/api/v1/widget-types", Token: A.token}), 200)
	types := wt.Body["data"].([]any)
	if len(types) < 15 {
		t.Fatalf("widget-types = %d", len(types))
	}
	for _, x := range types {
		m := x.(map[string]any)
		if m["type"] == "top_customers" && (m["contains_personal_data"] != true || m["required_permission"] != "customers.read") {
			t.Fatalf("top_customers = %v", m)
		}
	}

	// Criterio 1: plantillas sembradas.
	list := a.must(a.do(req{Method: "GET", Path: "/api/v1/dashboards?visibility=system", Token: A.token}), 200)
	if len(list.Body["data"].([]any)) != 2 {
		t.Fatalf("plantillas = %s", list.Raw)
	}
	tpl := a.must(a.do(req{Method: "GET", Path: "/api/v1/dashboards/" + nocTemplate, Token: A.token}), 200)
	if tpl.str("template_key") != "noc_isp" || tpl.Body["tenant_id"] != nil {
		t.Fatalf("plantilla = %s", tpl.Raw)
	}
	if r := a.do(req{Method: "PATCH", Path: "/api/v1/dashboards/" + nocTemplate, Token: A.token, Header: map[string]string{"If-Match": `"1"`},
		Body: map[string]any{"name": "mío"}}); r.Status != 409 || r.code() != "DASHBOARD_READ_ONLY" {
		t.Fatalf("editar plantilla: %d %s", r.Status, r.Raw)
	}
	dup := a.must(a.post(A.token, "/api/v1/dashboards/"+nocTemplate+"/duplicate", map[string]any{}), 201)
	if dup.str("visibility") != "private" || dup.str("name") != "NOC del ISP (copia)" {
		t.Fatalf("duplicado = %s", dup.Raw)
	}

	// Criterio 3: tipo desconocido o config inválida → 422.
	dupID := dup.str("id")
	bad := a.do(req{Method: "POST", Path: "/api/v1/dashboards/" + dupID + "/widgets", Token: A.token, Header: map[string]string{"If-Match": `"1"`},
		Body: map[string]any{"id": "w-new", "type": "no_existe", "title": nil, "position": map[string]any{"x": 0, "y": 20, "w": 2, "h": 2}, "config": map[string]any{}}})
	if bad.Status != 422 || bad.code() != "WIDGET_TYPE_UNKNOWN" {
		t.Fatalf("tipo desconocido: %d %s", bad.Status, bad.Raw)
	}
	bad = a.do(req{Method: "PATCH", Path: "/api/v1/dashboards/" + dupID + "/widgets/w-top-customers", Token: A.token,
		Header: map[string]string{"If-Match": `"1"`}, Body: map[string]any{"config": map[string]any{"n": 999}}})
	if bad.Status != 422 || bad.code() != "WIDGET_CONFIG_INVALID" {
		t.Fatalf("config inválida: %d %s", bad.Status, bad.Raw)
	}
	ok := a.must(a.do(req{Method: "PATCH", Path: "/api/v1/dashboards/" + dupID + "/widgets/w-top-customers", Token: A.token,
		Header: map[string]string{"If-Match": `"1"`}, Body: map[string]any{"config": map[string]any{"n": 5}}}), 200)
	if ok.Header.Get("ETag") != `"2"` {
		t.Fatalf("versión del documento = %s", ok.Header.Get("ETag"))
	}

	// Un dashboard privado no lo ve otro usuario del ISP.
	other := a.userToken(A, "noc@isp-a.test", "noc")
	if r := a.do(req{Method: "GET", Path: "/api/v1/dashboards/" + dupID, Token: other}); r.Status != 404 {
		t.Fatalf("privado visto por otro: %d", r.Status)
	}
	if r := a.post(other, "/api/v1/playlists", map[string]any{"name": "x", "items": []any{}}); r.Status != 403 {
		t.Fatalf("playlist sin dashboards.manage: %d", r.Status)
	}

	// Criterio 4: playlist → kiosco → /kiosk/config; el cambio emite el evento.
	pl := a.must(a.post(A.token, "/api/v1/playlists", map[string]any{"name": "NOC + Seguridad", "items": []any{
		map[string]any{"dashboard_id": nocTemplate, "duration_seconds": 30},
		map[string]any{"dashboard_id": "0192f000-0000-7000-8000-00000000d002", "duration_seconds": 45},
	}}), 201)
	if r := a.post(A.token, "/api/v1/playlists", map[string]any{"name": "x", "items": []any{map[string]any{"dashboard_id": dupID, "duration_seconds": 30}}}); r.Status != 422 {
		t.Fatalf("playlist con dashboard privado: %d", r.Status)
	}
	_, _, jwt := a.enrolledKiosk(A, map[string]any{"name": "TV NOC", "playlist_id": pl.str("id")})
	cfg := a.must(a.do(req{Method: "GET", Path: "/api/v1/kiosk/config", Token: jwt}), 200)
	items := cfg.Body["items"].([]any)
	if len(items) != 2 || items[1].(map[string]any)["duration_seconds"].(float64) != 45 || cfg.Body["show_personal_data"] != false {
		t.Fatalf("kiosk/config = %s", cfg.Raw)
	}
	if r := a.do(req{Method: "GET", Path: "/api/v1/kiosk/config", Token: A.token}); r.Status != 403 {
		t.Fatalf("kiosk/config con usuario: %d", r.Status)
	}
	a.must(a.do(req{Method: "PATCH", Path: "/api/v1/playlists/" + pl.str("id"), Token: A.token, Header: map[string]string{"If-Match": `"1"`},
		Body: map[string]any{"items": []any{map[string]any{"dashboard_id": nocTemplate, "duration_seconds": 20}}}}), 200)
	var n int
	if err := a.admin.QueryRow(context.Background(), `SELECT count(*) FROM analytics.outbox WHERE payload->>'type' = 'horus.analytics.playlist.updated'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("playlist.updated = %d %v", n, err)
	}
	cfg = a.must(a.do(req{Method: "GET", Path: "/api/v1/kiosk/config", Token: jwt}), 200)
	if len(cfg.Body["items"].([]any)) != 1 {
		t.Fatalf("kiosk/config tras el cambio = %s", cfg.Raw)
	}

	// El kiosco solo ve sus dashboards y widgets kiosk_allowed.
	a.must(a.do(req{Method: "GET", Path: "/api/v1/dashboards/" + nocTemplate, Token: jwt}), 200)
	if r := a.do(req{Method: "GET", Path: "/api/v1/dashboards/0192f000-0000-7000-8000-00000000d002", Token: jwt}); r.Status != 404 {
		t.Fatalf("dashboard no asignado: %d", r.Status)
	}
	data := a.must(a.do(req{Method: "GET", Path: "/api/v1/dashboards/" + nocTemplate + "/widgets/w-header/data", Token: jwt}), 200)
	if data.Body["meta"].(map[string]any)["widget_type"] != "noc_header" || data.Header.Get("ETag") == "" {
		t.Fatalf("datos de widget = %s", data.Raw)
	}
	if r := a.do(req{Method: "GET", Path: "/api/v1/dashboards", Token: jwt}); r.Status != 403 || r.code() != "KIOSK_FORBIDDEN" {
		t.Fatalf("kiosco en listado: %d %s", r.Status, r.Raw)
	}
	if r := a.post(jwt, "/api/v1/widget-data/preview", map[string]any{"type": "noc_header", "config": map[string]any{}}); r.Status != 403 {
		t.Fatalf("kiosco en preview: %d", r.Status)
	}
	// Un usuario sin el permiso del tipo → 403 WIDGET_TYPE_NOT_ALLOWED solo en ese widget.
	if r := a.do(req{Method: "GET", Path: "/api/v1/dashboards/" + nocTemplate + "/widgets/w-top-customers/data", Token: other}); r.Status != 403 || r.code() != "WIDGET_TYPE_NOT_ALLOWED" {
		t.Fatalf("widget sin permiso: %d %s", r.Status, r.Raw)
	}
}
