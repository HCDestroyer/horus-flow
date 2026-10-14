//go:build integration

package tenancy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeLibre es un LibreNMS mínimo (API v0) para las pruebas de tenancy: lo
// justo para aprovisionar (dispositivos, grupos, reglas, plantillas,
// eventlog) y recibir el syslog de Horus. El falso completo, que valida la
// forma de cada petición, está en services/alerts/internal/librenms/librenmsfake.
type fakeLibre struct {
	mu        sync.Mutex
	token     string
	next      int
	devices   map[int]string // id → hostname
	groups    map[int]string
	members   map[int][]int
	rules     map[int]string
	templates map[int]string
	syslog    []string // "deviceID msg"
	events    []string
	requests  []string
}

func newFakeLibre(t *testing.T, token string) (*fakeLibre, *httptest.Server) {
	f := &fakeLibre{token: token, next: 10, devices: map[int]string{}, groups: map[int]string{}, members: map[int][]int{},
		rules: map[int]string{}, templates: map[int]string{}}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeLibre) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeLibre) syslogLines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.syslog...)
}

func (f *fakeLibre) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	out := func(code int, v map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	if r.Header.Get("X-Auth-Token") != f.token {
		out(401, map[string]any{"status": "error", "message": "Unauthenticated."})
		return
	}
	var b map[string]any
	_ = json.Unmarshal(raw, &b)
	p := strings.TrimPrefix(r.URL.Path, "/api/v0")
	seg := strings.Split(strings.Trim(p, "/"), "/")
	now := time.Now().UTC().Format(time.DateTime)
	switch {
	case p == "/system":
		out(200, map[string]any{"status": "ok", "system": []any{map[string]any{"local_ver": "25.10.0"}}})
	case p == "/devices" && r.Method == "GET":
		list := []any{}
		for id, h := range f.devices {
			list = append(list, map[string]any{"device_id": strconv.Itoa(id), "hostname": h})
		}
		out(200, map[string]any{"status": "ok", "devices": list})
	case p == "/devices" && r.Method == "POST":
		f.next++
		h, _ := b["hostname"].(string)
		f.devices[f.next] = h
		out(200, map[string]any{"status": "ok", "devices": []any{map[string]any{"device_id": strconv.Itoa(f.next), "hostname": h}}})
	case len(seg) == 3 && seg[0] == "devices" && seg[2] == "eventlog":
		f.events = append(f.events, seg[1]+" "+b["text"].(string))
		out(200, map[string]any{"status": "ok"})
	case len(seg) == 2 && seg[0] == "devices" && r.Method == "DELETE":
		id, _ := strconv.Atoi(seg[1])
		delete(f.devices, id)
		for g, ms := range f.members { // LibreNMS saca el dispositivo de sus grupos al borrarlo
			keep := []int{}
			for _, x := range ms {
				if x != id {
					keep = append(keep, x)
				}
			}
			f.members[g] = keep
		}
		out(200, map[string]any{"status": "ok"})
	case p == "/devicegroups" && r.Method == "GET":
		if len(f.groups) == 0 {
			out(404, map[string]any{"status": "error", "message": "No device groups found"})
			return
		}
		list := []any{}
		for id, n := range f.groups {
			list = append(list, map[string]any{"id": id, "name": n, "type": "static"})
		}
		out(200, map[string]any{"status": "ok", "groups": list})
	case p == "/devicegroups" && r.Method == "POST":
		f.next++
		f.groups[f.next] = b["name"].(string)
		out(201, map[string]any{"status": "ok", "id": f.next})
	case len(seg) == 3 && seg[0] == "devicegroups":
		id, _ := strconv.Atoi(seg[1])
		for _, d := range b["devices"].([]any) {
			n := int(d.(float64))
			if r.Method == "POST" {
				f.members[id] = append(f.members[id], n)
			} else {
				keep := f.members[id][:0]
				for _, x := range f.members[id] {
					if x != n {
						keep = append(keep, x)
					}
				}
				f.members[id] = keep
			}
		}
		out(200, map[string]any{"status": "ok"})
	case p == "/rules" && r.Method == "GET":
		list := []any{}
		for id, n := range f.rules {
			list = append(list, map[string]any{"id": strconv.Itoa(id), "name": n})
		}
		out(200, map[string]any{"status": "ok", "rules": list})
	case p == "/rules":
		if _, edit := b["rule_id"]; !edit {
			f.next++
			f.rules[f.next] = b["name"].(string)
		}
		out(200, map[string]any{"status": "ok"})
	case p == "/alert_templates" && r.Method == "GET":
		list := []any{}
		for id, n := range f.templates {
			list = append(list, map[string]any{"id": id, "name": n})
		}
		out(200, map[string]any{"status": "ok", "alert_templates": list})
	case p == "/alert_templates":
		id := 0
		if v, edit := b["template_id"]; edit {
			id = int(v.(float64))
		} else {
			f.next++
			id = f.next
		}
		f.templates[id] = b["name"].(string)
		out(200, map[string]any{"status": "ok", "id": id})
	case p == "/syslogsink":
		host, _ := b["host"].(string)
		for id, h := range f.devices {
			if h == host {
				f.syslog = append(f.syslog, strconv.Itoa(id)+" "+b["msg"].(string))
			}
		}
		out(200, map[string]any{"status": "ok", "message": "Syslog received: 1"})
	case len(seg) == 3 && seg[0] == "logs":
		logs := []any{}
		src := f.syslog
		if seg[1] == "eventlog" {
			src = f.events
		}
		for i := len(src) - 1; i >= 0; i-- {
			if strings.HasPrefix(src[i], seg[2]+" ") {
				msg := strings.TrimPrefix(src[i], seg[2]+" ")
				logs = append(logs, map[string]any{"msg": msg, "message": msg, "timestamp": now, "datetime": now})
			}
		}
		out(200, map[string]any{"status": "ok", "logs": logs})
	default:
		out(404, map[string]any{"status": "error", "message": "This API route doesn't exist."})
	}
}

func (f *fakeLibre) groupMembers(name string) []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, n := range f.groups {
		if n == name {
			return append([]int(nil), f.members[id]...)
		}
	}
	return nil
}

// D17: Horus aprovisiona LibreNMS y le delega el alertado. Integración por
// canal y configuración de routers aisladas por ISP (RLS y API), envío de la
// prueba de extremo a extremo y credenciales SNMP write-only.
func TestLibreNMSIntegration(t *testing.T) {
	t.Parallel()
	lib, srv := newFakeLibre(t, "token-librenms-isp-a-123")
	a := startAppEnv(t, "HORUS_PUBLIC_BASE_URL=https://horus.isp-a.test")
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-a"), a.newISP(pt, "isp-b")

	site := a.must(a.post(A.token, "/api/v1/sites", map[string]any{"name": "Nodo Centro"}), 201).str("id")
	rt := a.must(a.post(A.token, "/api/v1/routers", map[string]any{"site_id": site, "name": "rt-centro-01"}), 201).str("id")

	ln := a.must(a.post(A.token, "/api/v1/notification-channels", map[string]any{"name": "LibreNMS", "kind": "librenms",
		"config": map[string]any{"base_url": srv.URL, "username": "horus"}, "subscription": map[string]any{"event_types": []string{"finding_opened"}}}), 201)
	lnID := ln.str("id")
	tg := a.must(a.post(A.token, "/api/v1/notification-channels", map[string]any{"name": "TG", "kind": "telegram",
		"config": map[string]any{"chat_id": "-100123"}, "subscription": map[string]any{"event_types": []string{"finding_opened"}}}), 201)

	// Estado inicial y canal de otro tipo.
	st := a.must(a.do(req{Method: "GET", Path: "/api/v1/notification-channels/" + lnID + "/librenms", Token: A.token}), 200)
	if st.str("status") != "not_provisioned" || st.str("horus_hostname_default") != "horus.isp-a.test" || st.Body["cpu_threshold"] != float64(85) {
		t.Fatalf("estado inicial = %s", st.Raw)
	}
	if r := a.do(req{Method: "GET", Path: "/api/v1/notification-channels/" + tg.str("id") + "/librenms", Token: A.token}); r.Status != 422 ||
		r.code() != "LIBRENMS_CHANNEL_REQUIRED" {
		t.Fatalf("canal telegram: %d %s", r.Status, r.Raw)
	}
	// Sin aprovisionar: la prueba de extremo a extremo responde 409.
	if r := a.post(A.token, "/api/v1/notification-channels/"+lnID+"/librenms/test", nil); r.Status != 409 || r.code() != "LIBRENMS_NOT_PROVISIONED" {
		t.Fatalf("test sin aprovisionar: %d %s", r.Status, r.Raw)
	}
	// Sin credenciales: la sincronización informa missing_credentials.
	if r := a.must(a.post(A.token, "/api/v1/notification-channels/"+lnID+"/librenms/sync", nil), 200); r.Body["ok"] != false ||
		r.str("error_code") != "missing_credentials" {
		t.Fatalf("sync sin credenciales: %s", r.Raw)
	}
	a.must(a.do(req{Method: "PUT", Path: "/api/v1/notification-channels/" + lnID + "/credentials", Token: A.token,
		Body: map[string]any{"api_token": "token-librenms-isp-a-123"}}), 204)

	// Router: IP de gestión + SNMP v2c propio para LibreNMS (write-only).
	routers := a.must(a.do(req{Method: "GET", Path: "/api/v1/librenms/routers", Token: A.token}), 200)
	if rows := routers.Body["data"].([]any); len(rows) != 1 || rows[0].(map[string]any)["mgmt_ip"] != nil ||
		rows[0].(map[string]any)["mode"] != "ping" {
		t.Fatalf("routers = %s", routers.Raw)
	}
	if r := a.do(req{Method: "PUT", Path: "/api/v1/librenms/routers/" + rt, Token: A.token,
		Body: map[string]any{"mgmt_ip": "198.51.100.10", "mode": "horus_snmp_v3"}}); r.Status != 422 {
		t.Fatalf("SNMPv3 de Horus sin consentimiento: %d %s", r.Status, r.Raw)
	}
	a.must(a.do(req{Method: "PUT", Path: "/api/v1/librenms/routers/" + rt, Token: A.token,
		Body: map[string]any{"mgmt_ip": "198.51.100.10", "mode": "snmp_v2c"}}), 200)
	a.must(a.do(req{Method: "PUT", Path: "/api/v1/librenms/routers/" + rt + "/snmp-credentials", Token: A.token,
		Body: map[string]any{"community": "comunidad-secreta"}}), 204)
	got := a.must(a.do(req{Method: "GET", Path: "/api/v1/librenms/routers", Token: A.token}), 200)
	if strings.Contains(string(got.Raw), "comunidad-secreta") || !strings.Contains(string(got.Raw), `"has_snmp_credentials":true`) {
		t.Fatalf("credenciales expuestas o sin marcar: %s", got.Raw)
	}
	if a.auditCount("alerts.librenms.router.credentials.updated") != 1 || a.auditCount("alerts.librenms.router.updated") != 1 {
		t.Fatal("configuración del router no auditada")
	}

	// ISP B: no ve ni toca nada de A.
	if r := a.do(req{Method: "GET", Path: "/api/v1/notification-channels/" + lnID + "/librenms", Token: B.token}); r.Status != 404 {
		t.Fatalf("integración de A vista por B: %d", r.Status)
	}
	if r := a.do(req{Method: "PUT", Path: "/api/v1/librenms/routers/" + rt, Token: B.token,
		Body: map[string]any{"mgmt_ip": "198.51.100.99", "mode": "ping"}}); r.Status != 404 || r.code() != "ROUTER_NOT_FOUND" {
		t.Fatalf("router de A configurado por B: %d %s", r.Status, r.Raw)
	}
	if b := a.must(a.do(req{Method: "GET", Path: "/api/v1/librenms/routers", Token: B.token}), 200); len(b.Body["data"].([]any)) != 0 {
		t.Fatalf("B ve routers de A: %s", b.Raw)
	}

	// Conectar con LibreNMS.
	sync1 := a.must(a.post(A.token, "/api/v1/notification-channels/"+lnID+"/librenms/sync", map[string]any{"cpu_threshold": 80}), 200)
	if sync1.Body["ok"] != true {
		t.Fatalf("sync = %s", sync1.Raw)
	}
	integ := sync1.Body["integration"].(map[string]any)
	if integ["status"] != "ok" || integ["horus_hostname"] != "horus.isp-a.test" || integ["cpu_threshold"] != float64(80) ||
		integ["tz_offset_minutes"] != float64(0) || len(integ["objects"].([]any)) != 18 {
		t.Fatalf("integración = %s", sync1.Raw)
	}
	if m := lib.groupMembers("Horus · Routers"); len(m) != 1 {
		t.Fatalf("miembros del grupo de routers = %v", m)
	}
	if a.auditCount("alerts.librenms.synced") != 1 {
		t.Fatal("sincronización no auditada")
	}
	// Idempotente: no crea nada nuevo.
	posts := lib.count("POST /api/v0/devices") - lib.count("POST /api/v0/devices/")
	a.must(a.post(A.token, "/api/v1/notification-channels/"+lnID+"/librenms/sync", nil), 200)
	if now := lib.count("POST /api/v0/devices") - lib.count("POST /api/v0/devices/"); now != posts {
		t.Fatalf("segunda sincronización creó dispositivos: %d → %d", posts, now)
	}

	// Prueba de extremo a extremo.
	test := a.must(a.post(A.token, "/api/v1/notification-channels/"+lnID+"/librenms/test", nil), 200)
	if test.Body["ok"] != true || len(test.Body["steps"].([]any)) != 5 || !strings.HasPrefix(test.str("line"), "HORUS INFO test ") {
		t.Fatalf("test = %s", test.Raw)
	}

	// Envío de prueba del canal: línea de syslog del dispositivo Horus.
	a.must(a.do(req{Method: "POST", Path: "/api/v1/notification-channels/" + lnID + "/test", Token: A.token,
		Header: map[string]string{"Idempotency-Key": uuid.NewString()}}), 202)
	a.waitFor("syslog de la prueba del canal", func() bool {
		for _, l := range lib.syslogLines() {
			if strings.Contains(l, "Notificación de prueba") {
				return true
			}
		}
		return false
	})

	// RLS: con el tenant de B en la sesión no se ve ninguna fila de A.
	ctx := context.Background()
	for _, tb := range []string{"alerts.librenms_integration", "alerts.librenms_object", "alerts.librenms_router"} {
		var all, visible int
		_ = a.admin.QueryRow(ctx, `SELECT count(*) FROM `+tb).Scan(&all)
		tx, err := a.admin.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = tx.Exec(ctx, `SET LOCAL ROLE alerts_app`)
		_, _ = tx.Exec(ctx, `SELECT set_config('horus.tenant_id', $1, true)`, B.id)
		_ = tx.QueryRow(ctx, `SELECT count(*) FROM `+tb).Scan(&visible)
		_ = tx.Rollback(ctx)
		if all == 0 || visible != 0 {
			t.Fatalf("%s: total=%d visibles para B=%d", tb, all, visible)
		}
	}

	// Borrado explícito del dispositivo creado por Horus para el router.
	a.must(a.do(req{Method: "DELETE", Path: "/api/v1/notification-channels/" + lnID + "/librenms/devices/" + rt, Token: A.token}), 204)
	if r := a.do(req{Method: "DELETE", Path: "/api/v1/notification-channels/" + lnID + "/librenms/devices/" + rt, Token: A.token}); r.Status != 404 {
		t.Fatalf("segundo borrado: %d", r.Status)
	}
	// Quitar la configuración del router: sale del grupo en la siguiente sincronización.
	a.must(a.do(req{Method: "DELETE", Path: "/api/v1/librenms/routers/" + rt, Token: A.token}), 204)
	a.must(a.post(A.token, "/api/v1/notification-channels/"+lnID+"/librenms/sync", nil), 200)
	if m := lib.groupMembers("Horus · Routers"); len(m) != 0 {
		t.Fatalf("router sin configurar sigue en el grupo: %v", m)
	}
}
