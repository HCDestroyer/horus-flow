//go:build acceptance

// Package i1 es la batería de aceptación del incremento 1 (historia I1-24)
// contra el backend REAL: una instalación hecha con scripts/install.sh (la
// que monta scripts/accept/accept-i1.sh en un raíz temporal o, con
// TARGET=installed, la del servidor). Recorre la demostración de I1
// (docs/roadmap.md §3) con routers simulados que se conectan por WireGuard de
// verdad y exportan los seis escenarios del simulador y la captura real de
// MikroTik. Cada paso queda en el informe (OK/FAIL/SKIP con su criterio e
// historia). La ejecuta el orquestador; a mano, ver tests/acceptance/README.md.
package i1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
	"github.com/hcdestroyer/horus-flow/tests/acceptance/acceptkit"
)

// scenarios son los seis escenarios de I0-10 / roadmap §3 (I1, criterio 1).
var scenarios = []string{"normal", "scan", "c2", "spam", "dos_out", "commercial"}

const realFixture = "mikrotik-real"

func TestAcceptI1(t *testing.T) {
	repo := repoRoot()
	stateD := env("ACCEPT_STATE_DIR", filepath.Join(repo, "bin", "accept-i1"))
	if err := os.MkdirAll(stateD, 0o700); err != nil {
		t.Fatal(err)
	}
	c := acceptkit.New(env("ACCEPT_BASE_URL", "https://127.0.0.1"))
	email := env("ACCEPT_ADMIN_EMAIL", "admin@horus.localhost")
	credsPath := filepath.Join(stateD, "admin-creds.json")
	def := acceptkit.Creds{Email: email, Password: readSecret(os.Getenv("ACCEPT_ADMIN_PASSWORD_FILE")),
		TOTPSecret: readSecret(os.Getenv("ACCEPT_ADMIN_TOTP_SECRET_FILE"))}
	creds, err := acceptkit.LoadCreds(credsPath, def)
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, c: c, repo: repo, stateD: stateD, suffix: acceptkit.RandomSuffix(),
		isps: map[string]*isp{}, byName: map[string]*node{}, reported: map[string]string{},
		flowsim: env("ACCEPT_FLOWSIM", filepath.Join(repo, "bin", "flowsim")),
		replay:  env("ACCEPT_PCAPREPLAY", filepath.Join(repo, "bin", "pcapreplay")),
		simrtr:  env("ACCEPT_SIM_ROUTER", filepath.Join(repo, "scripts", "accept", "sim-router.sh"))}
	w.s = &acceptkit.Session{C: c, Creds: creds, CredsPath: credsPath, Logf: t.Logf}
	seed := def.Password

	runStages(t, w, []stage{
		{"platform", "I0-06,I1-31", "superadmin (cambio de contraseña y TOTP), estado del sistema y alta de los ISP de prueba", nil,
			func(w *world) error { return w.platform(seed) }},
		{"inventory", "I0-09,I1-01", "por ISP: nodo, router principal simulado con IP de túnel única y prefijos de clientes", []string{"platform"},
			(*world).inventory},
		{"reputation", "I1-10,I0-17", "feed de prueba con los C2 del escenario c2 publicado en el snapshot de reputación", []string{"platform"},
			(*world).reputation},
		{"onboarding", "I1-02,I1-01", "script RouterOS ≥ 7.12 (IPFIX con NAT, 1 min, sin comment en el target) y enrolamiento con la clave pública del router simulado", []string{"inventory"},
			(*world).onboarding},
		{"tunnel", "I1-01", "túnel WireGuard real: handshake y peer activo en < 60 s", []string{"onboarding"},
			(*world).tunnel},
		{"flows", "I1-03,I0-10,I0-12", "seis escenarios y captura real exportados por el túnel al colector", []string{"tunnel"},
			(*world).flows},
		{"exporters", "I1-09", "estado del exportador: Exportando mientras llegan flujos", []string{"flows"},
			(*world).exporters},
		{"customers", "I1-05,I1-06", "clientes descubiertos = los del expected.json de cada escenario y de la captura real (236)", []string{"flows"},
			(*world).customers},
		{"real-flows", "I1-04,I1-24", "captura real: mismos flujos en ClickHouse que el test dorado (registros, estado de atribución)", []string{"flows"},
			(*world).realFlows},
		{"traffic", "I1-07,I1-08", "tops de tráfico por cliente, servicio, categoría, organización y ASN", []string{"customers"},
			(*world).traffic},
		{"findings", "I1-10,I1-11,I1-12", "hallazgos esperados por escenario (ninguno en normal y commercial), con razones, evidencia y lenguaje prudente", []string{"flows"},
			(*world).findings},
		{"websocket", "I1-13,I1-12", "WebSocket: el cambio de un hallazgo llega al ISP dueño y no a otro ISP", []string{"findings"},
			(*world).websocket},
		{"kiosk", "I1-14,I1-15,I1-21", "kiosco: enrolamiento, JWT de solo lectura, dashboard NOC, widgets, /flow-exporters y WebSocket", []string{"flows"},
			(*world).kiosk},
		{"isolation", "I0-08,I1-24", "aislamiento con un segundo ISP: API (tráfico, clientes, hallazgos, exportadores), ClickHouse (row policies) y PostgreSQL (RLS)", []string{"customers"},
			(*world).isolation},
		{"silent", "I1-09", "exportador Silencioso ≤ 2 min después de dejar de exportar", []string{"exporters"},
			(*world).silent},
		{"cleanup", "-", "TARGET=installed: suspender los ISP de prueba accept-i1-*", []string{"platform"},
			(*world).cleanup},
	})
}

func readSecret(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path) //nolint:gosec // ruta de configuración de la batería
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// --- platform --------------------------------------------------------------------------------

func (w *world) platform(seed string) error {
	if err := w.s.Login(seed); err != nil {
		return err
	}
	sess, err := w.s.Access()
	if err != nil {
		return err
	}
	st := w.expect("GET /system/status", get(sess, "/api/v1/system/status"), 200)
	if st.Str("status") == "" {
		failf("/system/status sin status: %s", st.Raw)
	}
	plat := w.token("platform")
	for _, k := range append(append([]string{"demo"}, scenarios[1:]...), "otro") {
		slug := strings.ReplaceAll("accept-i1-"+k+"-"+w.suffix, "_", "-")
		r := w.expect("POST /platform/tenants "+slug, idem(post(plat, "/api/v1/platform/tenants", map[string]any{
			"slug": slug, "name": "Aceptación I1 " + k, "country": "MX", "timezone": "America/Mexico_City",
			"initial_admin_email": "noc@" + slug + ".example.net"})), 201)
		w.isps[k] = &isp{Slug: slug, ID: r.Str("id")}
	}
	return nil
}

// --- inventory -------------------------------------------------------------------------------

func (w *world) inventory() error {
	fixtures := filepath.Join(w.repo, "tools", "flowsim", "fixtures", "sim")
	idx := 0
	add := func(name, ispKey, expPath, fixture string) {
		e, err := loadExpected(expPath)
		if err != nil {
			panic(fatal{err})
		}
		idx++
		n := &node{Name: name, Exp: e, ISP: w.isps[ispKey], Index: idx, Fixture: fixture}
		w.nodes = append(w.nodes, n)
		w.byName[name] = n
	}
	add("normal", "demo", filepath.Join(fixtures, "normal", "ipfix.expected.json"), "")
	for _, s := range scenarios[1:] {
		add(s, s, filepath.Join(fixtures, s, "ipfix.expected.json"), "")
	}
	real := filepath.Join(w.repo, "tests", "fixtures", "mikrotik-real")
	add(realFixture, "demo", filepath.Join(real, "ipfix-nat-20s.expected.json"), filepath.Join(real, "ipfix-nat-20s.pcapng"))

	for _, n := range w.nodes {
		tok := w.token(n.ISP.ID)
		code := strings.ToUpper(strings.ReplaceAll(n.Name, "_", ""))
		if len(code) > 6 {
			code = code[:6]
		}
		site := w.expect("alta del nodo "+n.Name, post(tok, "/api/v1/sites", map[string]any{"name": "Nodo " + n.Name, "code": code}), 201)
		n.Site = site.Str("id")
		rt := w.expect("alta del router principal de "+n.Name, post(tok, "/api/v1/routers", map[string]any{
			"site_id": n.Site, "name": "rt-" + strings.ReplaceAll(n.Name, "_", "-"), "model": "CCR2116-12G-4S+", "routeros_version": "7.16.1"}), 201)
		n.Router = rt.Str("id")
		ex := n.Exp.Exporters[0]
		addPrefixes := func(list []string, role string) {
			for _, p := range list {
				body := map[string]any{"prefix": p, "role": role}
				if pf := netip.MustParsePrefix(p); pf.Addr().Is6() && role == "customers" {
					body["ipv6_client_len"] = ex.IPv6ClientLen
				}
				w.expect(fmt.Sprintf("prefijo %s (%s) en %s", p, role, n.Name), post(tok, "/api/v1/sites/"+n.Site+"/client-prefixes", body), 201)
			}
		}
		addPrefixes(ex.Prefixes.Customers, "customers")
		addPrefixes(ex.Prefixes.Infrastructure, "infrastructure")
		addPrefixes(ex.Prefixes.Excluded, "excluded")
	}
	// La IP de túnel la asigna wireguard al consumir el alta del router (I1-01 criterio 1).
	seen := map[string]string{}
	for _, n := range w.nodes {
		err := eventually(60*time.Second, time.Second, func() error {
			r := w.do(get(w.token(n.ISP.ID), "/api/v1/routers/"+n.Router))
			n.TunnelIP = r.Str("tunnel_address")
			if n.TunnelIP == "" {
				return fmt.Errorf("router %s sin tunnel_address tras 60 s: %s", n.Name, trunc(r.Raw))
			}
			return nil
		})
		if err != nil {
			return err
		}
		n.TunnelIP = strings.TrimSuffix(n.TunnelIP, "/32")
		if other, dup := seen[n.TunnelIP]; dup {
			return fmt.Errorf("IP de túnel %s repetida (%s y %s)", n.TunnelIP, other, n.Name)
		}
		seen[n.TunnelIP] = n.Name
		w.logf("ok  %s: router %s con IP de túnel %s", n.Name, n.Router, n.TunnelIP)
	}
	return nil
}

// --- reputation ------------------------------------------------------------------------------

// reputation añade los indicadores del escenario c2 (feed de prueba
// flowsim-test-feed, IPs de documentación) al snapshot de reputación que
// leen el motor de detección y el ingester, conservando lo que ya hubiera.
func (w *world) reputation() error {
	store := os.Getenv("ACCEPT_STORE_DIR")
	if store == "" {
		return skipErr{"ACCEPT_STORE_DIR sin definir: no se puede publicar el feed de prueba (el escenario c2 no tendrá hallazgos)"}
	}
	e, err := loadExpected(filepath.Join(w.repo, "tools", "flowsim", "fixtures", "sim", "c2", "ipfix.expected.json"))
	if err != nil {
		return err
	}
	dir := datasets.SnapshotDir{Root: datasets.CatalogDir(store, reputation.Kind)}
	listed := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second)
	var entries []reputation.Entry
	var meta datasets.SnapshotMeta
	if _, path, err := dir.Latest(); err == nil {
		f, err := os.Open(path) //nolint:gosec // snapshot de la instalación
		if err != nil {
			return err
		}
		prev, err := reputation.ReadSnapshot(f)
		_ = f.Close()
		if err != nil {
			return fmt.Errorf("snapshot de reputación actual: %w", err)
		}
		meta = prev.Meta
		for p, inds := range prev.All() {
			for _, i := range inds {
				if i.Source != "flowsim-test-feed" {
					entries = append(entries, reputation.Entry{Prefix: p, Indicator: i})
				}
			}
		}
	}
	for _, i := range e.Indicators {
		a := netip.MustParseAddr(i.IP)
		entries = append(entries, reputation.Entry{Prefix: netip.PrefixFrom(a, a.BitLen()), Indicator: reputation.Indicator{
			Source: i.Source, Category: reputation.Category(i.Kind), Confidence: 90, FirstSeen: listed, Threat: "AcceptI1Bot"}})
	}
	var srcs []datasets.SourceRef
	for _, s := range meta.Sources {
		if s.ID != "flowsim-test-feed" {
			srcs = append(srcs, s)
		}
	}
	srcs = append(srcs, datasets.SourceRef{ID: "flowsim-test-feed", License: "pruebas (IPs de documentación RFC 5737)",
		CommercialUse: "yes", FetchedAt: time.Now().UTC(), Entries: len(e.Indicators)})
	snap, err := reputation.NewSnapshot(datasets.SnapshotMeta{CreatedAt: time.Now().UTC(), Sources: srcs, Labels: meta.Labels}, entries)
	if err != nil {
		return err
	}
	m, _, err := snap.Publish(dir)
	if err != nil {
		return err
	}
	// Los procesos de la imagen corren como 65532.
	_ = filepath.WalkDir(dir.Root, func(p string, _ os.DirEntry, _ error) error { return os.Lchown(p, 65532, 65532) })
	w.feedAt = time.Now()
	w.logf("ok  snapshot de reputación %s publicado (%d entradas, +%d de flowsim-test-feed)", m.Version, snap.Len(), len(e.Indicators))
	return nil
}

// --- onboarding ------------------------------------------------------------------------------

func (w *world) onboarding() error {
	_, _ = w.run(w.simrtr, "down-all")
	for _, n := range w.nodes {
		tok := w.token(n.ISP.ID)
		r := w.expect("script de onboarding de "+n.Name, idem(post(tok, "/api/v1/routers/"+n.Router+"/provisioning-script",
			map[string]any{"routeros_version": "7.12"})), 201)
		n.Script = string(r.Raw)
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			failf("el script debe ser text/plain, es %q", ct)
		}
		if !strings.Contains(r.Header.Get("Cache-Control"), "no-store") {
			failf("el script lleva secretos y debe ser Cache-Control: no-store (es %q)", r.Header.Get("Cache-Control"))
		}
		o, err := parseScript(n.Script)
		if err != nil {
			return fmt.Errorf("%s: %w", n.Name, err)
		}
		n.Onb = o
		if o.TunnelIP != n.TunnelIP {
			failf("%s: el script usa la IP de túnel %s y el router tiene %s", n.Name, o.TunnelIP, n.TunnelIP)
		}
		target := scriptLine(n.Script, "/ip traffic-flow target add")
		if strings.Contains(target, "comment=") {
			failf("%s: /ip traffic-flow target add lleva comment (falla en RouterOS real): %s", n.Name, target)
		}
		if !strings.Contains(target, "version=ipfix") || !strings.Contains(target, "src-address="+n.TunnelIP) {
			failf("%s: destino IPFIX incompleto: %s", n.Name, target)
		}
		for _, must := range []string{"active-flow-timeout=1m", "packet-sampling=no", "nat-src-address=yes", "nat-dst-address=yes",
			"persistent-keepalive=25s", "!write", "check-certificate=yes"} {
			if !strings.Contains(n.Script, must) {
				failf("%s: el script no contiene %q", n.Name, must)
			}
		}
		if strings.Contains(n.Script, "private-key=") || strings.Contains(n.Script, "check-certificate=no") {
			failf("%s: el script lleva una clave privada o desactiva TLS", n.Name)
		}
		_ = os.WriteFile(filepath.Join(w.stateD, "onboarding-"+n.Name+".rsc"), []byte(n.Script), 0o600)
		// El router "pega" el script: crea su interfaz con clave propia y envía solo la pública.
		out, err := w.run(w.simrtr, "up", n.Name, strconv.Itoa(n.Index), o.TunnelIP, o.HubKey, o.Endpoint, o.Port, o.Services)
		if err != nil {
			return err
		}
		pub := ""
		for _, l := range strings.Split(out, "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(l), "PUBKEY="); ok {
				pub = v
			}
		}
		if len(pub) != 44 {
			return fmt.Errorf("%s: sim-router no devolvió la clave pública: %s", n.Name, out)
		}
		enr := w.enroll(o.Token, pub)
		if enr.Status != http.StatusAccepted {
			failf("POST /enroll/wireguard (%s): HTTP %d, se esperaba 202\n%s", n.Name, enr.Status, trunc(enr.Raw))
		}
		w.logf("ok  POST /enroll/wireguard (%s) (HTTP 202)", n.Name)
		if enr.Str("peer_status") != "pending_handshake" {
			failf("enroll: se esperaba peer_status=pending_handshake: %s", enr.Raw)
		}
		if n.Name == "normal" {
			// Token de un uso (I1-01 criterio 3) y clave inválida.
			again := w.enroll(o.Token, pub)
			if again.Status < 400 || again.Status >= 500 || again.Code() != "ENROLLMENT_TOKEN_INVALID" {
				failf("token reutilizado: se esperaba ENROLLMENT_TOKEN_INVALID, HTTP %d %s", again.Status, again.Raw)
			}
			w.logf("ok  token de enrolamiento reutilizado → %d ENROLLMENT_TOKEN_INVALID", again.Status)
			dep := w.expect("script inverso", idem(post(tok, "/api/v1/routers/"+n.Router+"/deprovisioning-script", map[string]any{})), 200)
			rm := scriptLine(string(dep.Raw), "/ip traffic-flow target remove")
			if !strings.Contains(rm, "dst-address="+o.Collector) || strings.Contains(rm, "comment=") {
				failf("el script inverso debe quitar solo el destino de Horus por dst-address y sin comment: %s", rm)
			}
		}
	}
	return nil
}

// enroll llama a POST /enroll/wireguard respetando el límite de 10/min por IP
// (api.md §2.4): todos los routers simulados salen de la misma IP.
func (w *world) enroll(token, pub string) acceptkit.Resp {
	var r acceptkit.Resp
	for i := 0; i < 8; i++ {
		r = w.do(post("", "/api/v1/enroll/wireguard", map[string]string{"token": token, "public_key": pub}))
		if r.Status != http.StatusTooManyRequests {
			break
		}
		wait := 10 * time.Second
		if s, err := strconv.Atoi(r.Header.Get("Retry-After")); err == nil && s > 0 && s <= 60 {
			wait = time.Duration(s) * time.Second
		}
		w.logf("..  POST /enroll/wireguard: 429 (límite por IP); se reintenta en %s", wait)
		time.Sleep(wait)
	}
	return r
}

// --- tunnel ----------------------------------------------------------------------------------

func (w *world) tunnel() error {
	for _, n := range w.nodes {
		err := eventually(60*time.Second, 2*time.Second, func() error {
			out, err := w.run(w.simrtr, "handshake", n.Name)
			if err != nil {
				return err
			}
			if strings.TrimSpace(out) == "never" {
				return fmt.Errorf("%s: sin handshake WireGuard con el hub en 60 s", n.Name)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	w.logf("ok  handshake WireGuard de los %d routers simulados", len(w.nodes))
	// Estado del peer observado por wg-agent (I1-01 criterio 5).
	n := w.byName["normal"]
	return eventually(60*time.Second, 3*time.Second, func() error {
		r := w.do(get(w.token(n.ISP.ID), "/api/v1/wireguard/peers?router_id="+n.Router))
		peers := r.Data()
		if len(peers) != 1 {
			return fmt.Errorf("GET /wireguard/peers?router_id: se esperaba 1 peer: %s", trunc(r.Raw))
		}
		p := peers[0]
		if p["last_handshake_at"] == nil || (p["status"] != "active" && p["handshake_state"] != "active" && p["handshake_state"] != "ok") {
			return fmt.Errorf("peer sin handshake observado: %v", p)
		}
		w.logf("ok  peer de normal: status=%v handshake_state=%v last_handshake_at=%v", p["status"], p["handshake_state"], p["last_handshake_at"])
		return nil
	})
}

// --- flows -----------------------------------------------------------------------------------

func (w *world) flows() error {
	if !w.feedAt.IsZero() {
		// El ingester recarga el snapshot cada 30 s y el motor cada minuto.
		if d := time.Until(w.feedAt.Add(65 * time.Second)); d > 0 {
			w.logf("..  esperando %s a que ingester y detection carguen el feed de prueba", d.Round(time.Second))
			time.Sleep(d)
		}
	}
	simDir := filepath.Join(w.stateD, "sim")
	_ = os.MkdirAll(simDir, 0o700)
	for _, n := range w.nodes {
		target := n.Onb.Collector + ":4739"
		var args []string
		if n.Fixture != "" {
			args = []string{"exec", n.Name, w.replay, "-in", n.Fixture, "-target", target, "-src", n.TunnelIP}
		} else {
			args = []string{"exec", n.Name, w.flowsim, "-scenario", n.Name, "-seed", "1", "-proto", "ipfix", "-fixture",
				"-target", target, "-src", n.TunnelIP, "-speed", "1", "-expected", filepath.Join(simDir, n.Name+".expected.json")}
		}
		logf, err := os.Create(filepath.Join(simDir, n.Name+".log")) //nolint:gosec // log de la batería
		if err != nil {
			return err
		}
		cmd := exec.Command(w.simrtr, args...) //nolint:gosec // orden de la batería
		cmd.Stdout, cmd.Stderr = logf, logf
		if err := cmd.Start(); err != nil {
			return err
		}
		n.started = time.Now()
		n.done = make(chan error, 1)
		go func(n *node) {
			err := cmd.Wait()
			_ = logf.Close()
			n.finished = time.Now()
			n.done <- err
		}(n)
		w.logf("..  %s: exportando %s a %s desde %s", n.Name, map[bool]string{true: "la captura real", false: "el escenario"}[n.Fixture != ""], target, n.TunnelIP)
	}
	return nil
}

// waitSenders espera a que terminen los envíos (≈ 2 min de los escenarios).
func (w *world) waitSenders() error {
	var errs []error
	for _, n := range w.nodes {
		if n.done == nil {
			continue
		}
		if err := <-n.done; err != nil {
			b, _ := os.ReadFile(filepath.Join(w.stateD, "sim", n.Name+".log"))
			errs = append(errs, fmt.Errorf("%s: el envío falló: %w\n%s", n.Name, err, trunc(b)))
		}
		n.done = nil
		if n.finished.After(w.sendEnd) {
			w.sendEnd = n.finished
		}
	}
	return errors.Join(errs...)
}

// --- exporters -------------------------------------------------------------------------------

func (w *world) exporterStates(n *node) (map[string]any, error) {
	r := w.do(get(w.token(n.ISP.ID), "/api/v1/flow-exporters?site_id="+n.Site))
	if r.Status != 200 {
		return nil, fmt.Errorf("GET /flow-exporters (%s): HTTP %d %s", n.Name, r.Status, trunc(r.Raw))
	}
	for _, e := range r.Data() {
		if e["router_id"] == n.Router {
			return e, nil
		}
	}
	return nil, fmt.Errorf("GET /flow-exporters (%s) no incluye el router %s: %s", n.Name, n.Router, trunc(r.Raw))
}

func (w *world) exporters() error {
	for _, n := range w.nodes {
		want := []string{"exporting"}
		if n.Fixture != "" {
			// Captura de 2026-10-09 con saltos de secuencia: su exportTime está desfasado y hay pérdidas.
			want = []string{"exporting", "clock_skew", "lossy"}
		}
		var last map[string]any
		err := eventually(45*time.Second, 2*time.Second, func() error {
			e, err := w.exporterStates(n)
			if err != nil {
				return err
			}
			last = e
			st, _ := e["state"].(string)
			for _, ws := range want {
				if st == ws {
					return nil
				}
			}
			return fmt.Errorf("%s: exportador en %q, se esperaba %v: %v", n.Name, st, want, e)
		})
		if err != nil {
			return err
		}
		w.logf("ok  exportador de %s: state=%v flows_per_second=%v last_flow_at=%v", n.Name, last["state"], last["flows_per_second"], last["last_flow_at"])
	}
	return nil
}

// --- customers -------------------------------------------------------------------------------

func (w *world) customers() error {
	if err := w.waitSenders(); err != nil {
		return err
	}
	for _, n := range w.nodes {
		want := len(n.Exp.Exporters[0].Clients)
		var got float64
		err := eventually(4*time.Minute, 5*time.Second, func() error {
			r := w.do(get(w.token(n.ISP.ID), "/api/v1/customers/stats?site_id="+n.Site))
			if r.Status != 200 {
				return fmt.Errorf("GET /customers/stats: HTTP %d %s", r.Status, trunc(r.Raw))
			}
			got, _ = r.Body["total"].(float64)
			if int(got) != want {
				return fmt.Errorf("%s: %d clientes descubiertos, se esperaban %d (expected.json)", n.Name, int(got), want)
			}
			return nil
		})
		if err != nil {
			return err
		}
		// Cada IP/prefijo delegado del expected.json es un cliente (D1).
		keys := map[string]bool{}
		cursor := ""
		for page := 0; page < 20; page++ {
			path := "/api/v1/customers?limit=200&site_id=" + n.Site
			if cursor != "" {
				path += "&cursor=" + cursor
			}
			r := w.expect("GET /customers ("+n.Name+")", get(w.token(n.ISP.ID), path), 200)
			for _, c := range r.Data() {
				a, _ := c["address"].(string)
				keys[strings.TrimSuffix(a, "/32")] = true
				if c["kind"] != "residential" && c["kind_source"] == "default" {
					failf("%s: cliente %s con tipo por defecto %v (se esperaba residential)", n.Name, a, c["kind"])
				}
			}
			pg, _ := r.Body["page"].(map[string]any)
			cursor, _ = pg["next_cursor"].(string)
			if cursor == "" {
				break
			}
		}
		var missing []string
		for _, c := range n.Exp.Exporters[0].Clients {
			k := c.Key
			if !keys[k] && !keys[strings.TrimSuffix(k, "/64")] {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			if len(missing) > 10 {
				missing = append(missing[:10], "…")
			}
			failf("%s: faltan clientes del expected.json en GET /customers: %v (ejemplo de dirección devuelta: %v)", n.Name, missing, firstKey(keys))
		}
		w.logf("ok  %s: %d clientes descubiertos, todos los del expected.json", n.Name, int(got))
	}
	return nil
}

func firstKey(m map[string]bool) string {
	for k := range m {
		return k
	}
	return ""
}

// --- ClickHouse ------------------------------------------------------------------------------

// chQuery ejecuta SQL en el ClickHouse de la instalación (docker exec) como
// user (vacío = administrador) y devuelve las filas TSV.
func (w *world) chQuery(user, password string, settings map[string]string, sql string) ([][]string, error) {
	ctr := os.Getenv("ACCEPT_CH_CONTAINER")
	if ctr == "" {
		return nil, skipErr{"ACCEPT_CH_CONTAINER sin definir"}
	}
	if user == "" {
		user = env("ACCEPT_CH_USER", "horus")
		password = readSecret(filepath.Join(os.Getenv("ACCEPT_SECRETS_DIR"), "clickhouse_password"))
	}
	args := []string{"exec", "-i", ctr, "clickhouse-client", "--user", user, "--password", password, "--format", "TSV"}
	if len(settings) > 0 {
		// Ajustes personalizados (SQL_*): en la cláusula SETTINGS, no como opción del cliente.
		var kv []string
		for k, v := range settings {
			kv = append(kv, k+" = '"+v+"'")
		}
		sort.Strings(kv)
		sql += " SETTINGS " + strings.Join(kv, ", ")
	}
	args = append(args, "-q", sql)
	out, err := w.run("docker", args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse (%s): %w", user, err)
	}
	var rows [][]string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			rows = append(rows, strings.Split(l, "\t"))
		}
	}
	return rows, nil
}

func (w *world) realFlows() error {
	n := w.byName[realFixture]
	ex := n.Exp.Exporters[0]
	// La captura se exportó desde 10.255.3.17 y aquí llega desde la IP de túnel del router
	// simulado: los registros del tráfico propio del exportador original (estado "tunnel" en el
	// expected.json, que el test dorado descarta porque allí el inventario usa 10.255.3.17)
	// ya no son del exportador y se atribuyen como cualquier otro (su otro extremo es un cliente).
	want := ex.Totals.DataRecords
	wantStatus := map[string]int{"attributed": ex.ByStatus["attributed"] + ex.ByStatus["tunnel"],
		"internal": ex.ByStatus["internal"], "unknown": ex.ByStatus["unknown"]}
	var byStatus map[string]int
	err := eventually(3*time.Minute, 5*time.Second, func() error {
		rows, err := w.chQuery("", "", nil, fmt.Sprintf(
			"SELECT toString(attribution_status), count() FROM flows.flows_raw WHERE tenant_id = '%s' AND router_id = '%s' GROUP BY 1",
			n.ISP.ID, n.Router))
		if err != nil {
			return err
		}
		byStatus = map[string]int{}
		total := 0
		for _, r := range rows {
			v, _ := strconv.Atoi(r[1])
			byStatus[r[0]] = v
			total += v
		}
		if total != want {
			return fmt.Errorf("flows_raw de la captura real: %d filas, se esperaban %d (todos los registros del expected.json); por estado %v", total, want, byStatus)
		}
		return nil
	})
	var sk skipErr
	if errors.As(err, &sk) {
		return err
	}
	if err != nil {
		return err
	}
	for k, v := range wantStatus {
		if byStatus[k] != v {
			return fmt.Errorf("attribution_status %s = %d, se esperaba %d (expected.json); todo: %v", k, byStatus[k], v, byStatus)
		}
	}
	w.logf("ok  captura real en flows_raw: %d filas, por estado %v (= expected.json)", want, byStatus)
	return nil
}

// --- traffic ---------------------------------------------------------------------------------

func (w *world) traffic() error {
	n := w.byName["normal"]
	from := n.started.Add(-5 * time.Minute).UTC().Format(time.RFC3339)
	to := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339)
	for _, dim := range []string{"customers", "services", "categories", "organizations", "asns"} {
		var r acceptkit.Resp
		err := eventually(3*time.Minute, 10*time.Second, func() error {
			r = w.do(get(w.token(n.ISP.ID), "/api/v1/analytics/traffic/top?dimension="+dim+"&site_id="+n.Site+"&n=10&from="+from+"&to="+to))
			if r.Status != 200 {
				return fmt.Errorf("top %s: HTTP %d %s", dim, r.Status, trunc(r.Raw))
			}
			data, _ := r.Body["data"].(map[string]any)
			rows, _ := data["rows"].([]any)
			if len(rows) == 0 {
				return fmt.Errorf("top %s del nodo normal vacío: %s", dim, trunc(r.Raw))
			}
			return nil
		})
		if err != nil {
			return err
		}
		data, _ := r.Body["data"].(map[string]any)
		rows, _ := data["rows"].([]any)
		first, _ := rows[0].(map[string]any)
		w.logf("ok  top %s: %d filas; primera %v", dim, len(rows), compact(first))
	}
	return nil
}

func compact(m map[string]any) string {
	b, _ := json.Marshal(m)
	if len(b) > 200 {
		b = append(b[:200], "…"...)
	}
	return string(b)
}

// --- findings --------------------------------------------------------------------------------

type gotFinding struct {
	ID, Client, Kind, Severity, Summary string
	Version                             float64
	Raw                                 map[string]any
}

func (w *world) listFindings(n *node) ([]gotFinding, error) {
	r := w.do(get(w.token(n.ISP.ID), "/api/v1/findings?limit=100&site_id="+n.Site))
	if r.Status != 200 {
		return nil, fmt.Errorf("GET /findings (%s): HTTP %d %s", n.Name, r.Status, trunc(r.Raw))
	}
	var out []gotFinding
	for _, f := range r.Data() {
		g := gotFinding{Raw: f}
		g.ID, _ = f["id"].(string)
		g.Kind, _ = f["kind"].(string)
		g.Severity, _ = f["severity"].(string)
		g.Version, _ = f["version"].(float64)
		if c, ok := f["customer"].(map[string]any); ok {
			g.Client, _ = c["address"].(string)
		}
		if s, ok := f["summary"].(map[string]any); ok {
			g.Summary, _ = s["text"].(string)
		} else {
			g.Summary, _ = f["summary"].(string)
		}
		g.Client = strings.TrimSuffix(g.Client, "/32")
		out = append(out, g)
	}
	return out, nil
}

func (w *world) findings() error {
	if err := w.waitSenders(); err != nil {
		return err
	}
	// Ventanas de 5 min + retraso del motor (2 min) + intervalo (1 min).
	deadline := w.sendEnd.Add(10 * time.Minute)
	var errs []error
	for _, n := range w.nodes {
		if n.Fixture != "" {
			continue // captura de un día anterior: fuera de las ventanas del motor
		}
		want := map[string]bool{}
		for _, f := range n.Exp.Findings {
			want[f.Client+"|"+f.Kind+"|"+f.Severity] = true
		}
		var got []gotFinding
		check := func() error {
			var err error
			if got, err = w.listFindings(n); err != nil {
				return err
			}
			have := map[string]bool{}
			for _, g := range got {
				have[g.Client+"|"+g.Kind+"|"+g.Severity] = true
			}
			if len(got) != len(want) || !mapsEqual(have, want) {
				return fmt.Errorf("%s: hallazgos %v, se esperaban %v", n.Name, keysOf(have), keysOf(want))
			}
			return nil
		}
		var err error
		if len(want) == 0 {
			// Sin hallazgos esperados: se espera a que el motor haya evaluado todas las ventanas.
			if d := time.Until(w.sendEnd.Add(8 * time.Minute)); d > 0 {
				w.logf("..  %s: esperando %s a que el motor evalúe sus ventanas (no debe abrir hallazgos)", n.Name, d.Round(time.Second))
				time.Sleep(d)
			}
			err = check()
		} else {
			err = eventually(time.Until(deadline), 15*time.Second, check)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, g := range got {
			if err := checkFinding(g); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", n.Name, err))
			}
		}
		w.logf("ok  %s: %d hallazgos, los esperados %v", n.Name, len(got), keysOf(want))
	}
	return errors.Join(errs...)
}

// checkFinding: ≥ 2 elementos de evidencia, confianza, razones y nunca "infectado" (roadmap §3, criterio 7).
func checkFinding(g gotFinding) error {
	raw, _ := json.Marshal(g.Raw)
	if strings.Contains(strings.ToLower(string(raw)), "infectad") {
		return fmt.Errorf("el hallazgo %s usa la palabra «infectado»", g.ID)
	}
	reasons, _ := g.Raw["reasons"].([]any)
	if len(reasons) == 0 {
		return fmt.Errorf("hallazgo %s sin razones", g.ID)
	}
	ev, _ := g.Raw["evidence"].(map[string]any)
	n := 0
	for _, v := range ev {
		if v != nil {
			n++
		}
	}
	if n+len(reasons) < 2 || n < 1 {
		return fmt.Errorf("hallazgo %s con menos de 2 elementos de evidencia: reasons=%d evidence=%v", g.ID, len(reasons), ev)
	}
	if g.Raw["confidence"] == nil || g.Raw["confidence_level"] == nil {
		return fmt.Errorf("hallazgo %s sin confianza", g.ID)
	}
	return nil
}

func mapsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- websocket -------------------------------------------------------------------------------

type wsConn struct {
	c    *websocket.Conn
	msgs chan map[string]any
}

func (w *world) wsOpen(tok string) (*wsConn, error) {
	r := w.do(post(tok, "/api/v1/ws/tickets", nil))
	if r.Status != 200 && r.Status != 201 {
		return nil, fmt.Errorf("POST /ws/tickets: HTTP %d %s", r.Status, trunc(r.Raw))
	}
	u := strings.Replace(w.c.Base, "http", "ws", 1) + "/api/v1/ws?ticket=" + r.Str("ticket")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPClient: w.c.HTTP, Subprotocols: []string{"horus.ws.v1"},
		HTTPHeader: http.Header{"Origin": []string{w.c.Base}}})
	if err != nil {
		return nil, fmt.Errorf("WebSocket: %w", err)
	}
	ws := &wsConn{c: c, msgs: make(chan map[string]any, 256)}
	go func() {
		defer close(ws.msgs)
		for {
			_, b, err := c.Read(context.Background())
			if err != nil {
				return
			}
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				ws.msgs <- m
			}
		}
	}()
	return ws, nil
}

func (ws *wsConn) send(m map[string]any) error {
	b, _ := json.Marshal(m)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ws.c.Write(ctx, websocket.MessageText, b)
}

// wait devuelve el primer mensaje que cumple f antes de d (nil si no llega).
func (ws *wsConn) wait(d time.Duration, f func(map[string]any) bool) map[string]any {
	t := time.After(d)
	for {
		select {
		case m, ok := <-ws.msgs:
			if !ok {
				return nil
			}
			if f(m) {
				return m
			}
		case <-t:
			return nil
		}
	}
}

func (ws *wsConn) subscribe(topic string) (map[string]any, error) {
	id := "s-" + acceptkit.RandomSuffix()
	if err := ws.send(map[string]any{"type": "subscribe", "id": id, "topic": topic}); err != nil {
		return nil, err
	}
	m := ws.wait(10*time.Second, func(m map[string]any) bool { return m["id"] == id })
	if m == nil {
		return nil, fmt.Errorf("sin respuesta a subscribe %s", topic)
	}
	return m, nil
}

func (w *world) websocket() error {
	n := w.byName["scan"]
	tokA := w.token(n.ISP.ID)
	tokB := w.token(w.isps["otro"].ID)
	a, err := w.wsOpen(tokA)
	if err != nil {
		return err
	}
	defer func() { _ = a.c.CloseNow() }()
	b, err := w.wsOpen(tokB)
	if err != nil {
		return err
	}
	defer func() { _ = b.c.CloseNow() }()
	for _, ws := range []*wsConn{a, b} {
		m, err := ws.subscribe("security")
		if err != nil {
			return err
		}
		if m["type"] != "ack" {
			return fmt.Errorf("subscribe security: %v", m)
		}
	}
	w.logf("ok  WebSocket abierto (ticket de un uso) para el ISP del escenario scan y para el ISP otro; suscritos a security")
	got, err := w.listFindings(n)
	if err != nil || len(got) == 0 {
		return fmt.Errorf("no hay hallazgo que reconocer en scan: %v", err)
	}
	f := got[0]
	w.expect("reconocer el hallazgo "+f.ID, acceptkit.Call{Method: http.MethodPost, Path: "/api/v1/findings/" + f.ID + "/acknowledge",
		Token: tokA, Header: map[string]string{"If-Match": fmt.Sprintf(`"%d"`, int(f.Version))}, Body: map[string]any{"comment": "Aceptación I1: visto en el NOC"}}, 200)
	isEvent := func(m map[string]any) bool {
		if m["type"] != "event" {
			return false
		}
		ev, _ := m["event"].(map[string]any)
		return ev["subject"] == f.ID || strings.Contains(compact(ev), f.ID)
	}
	if m := a.wait(15*time.Second, isEvent); m == nil {
		return errors.New("el ISP dueño no recibió por WebSocket el evento del hallazgo reconocido en 15 s")
	}
	w.logf("ok  evento del hallazgo recibido por WebSocket en el ISP dueño")
	if m := b.wait(5*time.Second, isEvent); m != nil {
		return fmt.Errorf("FUGA: el ISP otro recibió por WebSocket un evento de otro ISP: %v", compact(m))
	}
	w.logf("ok  el ISP otro no recibe el evento")
	// Falso positivo con comentario (I1-12; la persona lo usará en I1-27).
	r := w.do(get(tokA, "/api/v1/findings/"+f.ID))
	ver, _ := r.Body["version"].(float64)
	fp := w.expect("marcar falso positivo", acceptkit.Call{Method: http.MethodPost, Path: "/api/v1/findings/" + f.ID + "/mark-false-positive",
		Token: tokA, Header: map[string]string{"If-Match": fmt.Sprintf(`"%d"`, int(ver))}, Body: map[string]any{"comment": "Aceptación I1: equipo de pruebas del ISP"}}, 200)
	if fp.Str("state") != "false_positive" {
		return fmt.Errorf("tras marcar falso positivo el estado es %q", fp.Str("state"))
	}
	return nil
}

// --- kiosk -----------------------------------------------------------------------------------

func (w *world) kiosk() error {
	n := w.byName["normal"]
	tok := w.token(n.ISP.ID)
	ds := w.expect("GET /dashboards", get(tok, "/api/v1/dashboards"), 200)
	var noc string
	for _, d := range ds.Data() {
		if d["template_key"] == "noc-isp" || strings.Contains(fmt.Sprint(d["name"]), "NOC") {
			noc, _ = d["id"].(string)
		}
	}
	if noc == "" {
		return fmt.Errorf("no está la plantilla «NOC del ISP» en GET /dashboards: %s", trunc(ds.Raw))
	}
	k := w.expect("POST /kiosks", post(tok, "/api/v1/kiosks", map[string]any{"name": "TV NOC aceptación", "dashboard_ids": []string{noc}}), 201)
	kid := k.Str("id")
	code := w.expect("código de enrolamiento", idem(post(tok, "/api/v1/kiosks/"+kid+"/enrollment-codes", nil)), 201)
	tv := w.c.Fork() // la pantalla: otro navegador
	// Rutas con cookie de dispositivo: X-Requested-With y Origin de la propia instalación (CSRF).
	xrw := map[string]string{"X-Requested-With": "horus", "Origin": w.c.Base}
	en, err := tv.Do(acceptkit.Call{Method: http.MethodPost, Path: "/api/v1/kiosk/enroll", Header: xrw, Body: map[string]string{"code": code.Str("code")}})
	if err != nil {
		return err
	}
	if en.Status != 200 && en.Status != 201 && en.Status != 204 {
		return fmt.Errorf("POST /kiosk/enroll: HTTP %d %s", en.Status, trunc(en.Raw))
	}
	kt, err := tv.Do(acceptkit.Call{Method: http.MethodPost, Path: "/api/v1/kiosk/token", Header: xrw})
	if err != nil {
		return err
	}
	if kt.Status != 200 || kt.Str("access_token") == "" {
		return fmt.Errorf("POST /kiosk/token (cookie de dispositivo): HTTP %d %s", kt.Status, trunc(kt.Raw))
	}
	ktok := kt.Str("access_token")
	w.logf("ok  kiosco enrolado con código de un uso; JWT de kiosco emitido")
	kget := func(path string) acceptkit.Resp {
		r, err := tv.Do(get(ktok, path))
		if err != nil {
			panic(fatal{err})
		}
		return r
	}
	cfg := kget("/api/v1/kiosk/config")
	if cfg.Status != 200 || !strings.Contains(string(cfg.Raw), noc) {
		return fmt.Errorf("GET /kiosk/config: HTTP %d %s", cfg.Status, trunc(cfg.Raw))
	}
	d := kget("/api/v1/dashboards/" + noc)
	if d.Status != 200 {
		return fmt.Errorf("GET /dashboards/{noc} con JWT de kiosco: HTTP %d %s", d.Status, trunc(d.Raw))
	}
	widgets, _ := d.Body["widgets"].([]any)
	okW := 0
	for _, x := range widgets {
		wd, _ := x.(map[string]any)
		id, _ := wd["id"].(string)
		r := kget("/api/v1/dashboards/" + noc + "/widgets/" + id + "/data")
		if r.Status != 200 {
			return fmt.Errorf("datos del widget %s (%v) con JWT de kiosco: HTTP %d %s", id, wd["type"], r.Status, trunc(r.Raw))
		}
		okW++
	}
	w.logf("ok  kiosco: /kiosk/config, dashboard NOC y datos de sus %d widgets", okW)
	// Widget de exportadores del kiosco (bug corregido en I1-24): GET /flow-exporters con JWT de kiosco.
	fe := kget("/api/v1/flow-exporters")
	if fe.Status != 200 {
		return fmt.Errorf("GET /flow-exporters con JWT de kiosco: HTTP %d %s", fe.Status, trunc(fe.Raw))
	}
	if !strings.Contains(string(fe.Raw), n.Router) {
		return fmt.Errorf("GET /flow-exporters del kiosco no incluye el router del nodo: %s", trunc(fe.Raw))
	}
	w.logf("ok  GET /flow-exporters con JWT de kiosco: %d exportadores", len(fe.Data()))
	for _, c := range []acceptkit.Call{get(ktok, "/api/v1/customers"), get(ktok, "/api/v1/flow-exporters/" + n.Router), get(ktok, "/api/v1/me"),
		post(ktok, "/api/v1/sites", map[string]any{"name": "Intruso"})} {
		r, err := tv.Do(c)
		if err != nil {
			return err
		}
		if r.Status != 403 || r.Code() != "KIOSK_FORBIDDEN" {
			return fmt.Errorf("%s %s con JWT de kiosco: se esperaba 403 KIOSK_FORBIDDEN, HTTP %d %s", c.Method, c.Path, r.Status, trunc(r.Raw))
		}
	}
	w.logf("ok  fuera de su lista blanca el kiosco recibe 403 KIOSK_FORBIDDEN (incluido escribir)")
	ws, err := w.wsOpenWith(tv, ktok)
	if err != nil {
		return fmt.Errorf("WebSocket del kiosco: %w", err)
	}
	defer func() { _ = ws.c.CloseNow() }()
	m, err := ws.subscribe("dashboard." + noc)
	if err != nil {
		return err
	}
	if m["type"] != "ack" {
		return fmt.Errorf("el kiosco no puede suscribirse a dashboard.%s: %v", noc, m)
	}
	w.logf("ok  WebSocket del kiosco suscrito a dashboard.%s", noc)
	return nil
}

func (w *world) wsOpenWith(c *acceptkit.Client, tok string) (*wsConn, error) {
	saved := w.c
	w.c = c
	defer func() { w.c = saved }()
	return w.wsOpen(tok)
}

// --- isolation -------------------------------------------------------------------------------

func (w *world) isolation() error {
	b := w.token(w.isps["otro"].ID)
	demo := w.byName["normal"]
	scan := w.byName["scan"]
	// Lo de A por id → 404; listados de B vacíos.
	w.expect("ISP otro: GET /routers/{router de demo} → 404", get(b, "/api/v1/routers/"+demo.Router), 404)
	w.expect("ISP otro: GET /flow-exporters/{router de demo} → 404", get(b, "/api/v1/flow-exporters/"+demo.Router), 404)
	if got, err := w.listFindings(scan); err == nil && len(got) > 0 {
		w.expect("ISP otro: GET /findings/{hallazgo de scan} → 404", get(b, "/api/v1/findings/"+got[0].ID), 404)
	}
	cs := w.expect("ISP demo: GET /customers", get(w.token(demo.ISP.ID), "/api/v1/customers?limit=1&site_id="+demo.Site), 200)
	if d := cs.Data(); len(d) == 1 {
		id, _ := d[0]["id"].(string)
		w.expect("ISP otro: GET /customers/{cliente de demo} → 404", get(b, "/api/v1/customers/"+id), 404)
	}
	for _, path := range []string{"/api/v1/customers", "/api/v1/findings", "/api/v1/flow-exporters", "/api/v1/sites", "/api/v1/routers"} {
		r := w.expect("ISP otro: GET "+path+" vacío", get(b, path), 200)
		if len(r.Data()) != 0 {
			return fmt.Errorf("FUGA: el ISP otro ve datos en %s: %s", path, trunc(r.Raw))
		}
	}
	st := w.expect("ISP otro: GET /customers/stats", get(b, "/api/v1/customers/stats"), 200)
	if v, _ := st.Body["total"].(float64); v != 0 {
		return fmt.Errorf("FUGA: /customers/stats del ISP otro = %v", v)
	}
	top := w.expect("ISP otro: top de clientes", get(b, "/api/v1/analytics/traffic/top?dimension=customers&range=24h"), 200)
	data, _ := top.Body["data"].(map[string]any)
	if rows, _ := data["rows"].([]any); len(rows) != 0 {
		return fmt.Errorf("FUGA: el top de tráfico del ISP otro tiene filas: %s", trunc(top.Raw))
	}
	ss := w.expect("ISP otro: GET /security/summary", get(b, "/api/v1/security/summary"), 200)
	if v, _ := ss.Body["affected_customers"].(float64); v != 0 {
		return fmt.Errorf("FUGA: /security/summary del ISP otro: %s", trunc(ss.Raw))
	}
	// ClickHouse: los lectores por tenant (row policies con SQL_horus_tenant) no ven filas de otro ISP.
	pw := readSecret(filepath.Join(os.Getenv("ACCEPT_SECRETS_DIR"), "ch_analytics_password"))
	if pw == "" {
		return skipErr{"API aislada; sin ACCEPT_SECRETS_DIR/ch_analytics_password no se comprueban las row policies de ClickHouse"}
	}
	count := func(tenant string) (int, error) {
		rows, err := w.chQuery("horus_analytics", pw, map[string]string{"SQL_horus_tenant": tenant},
			"SELECT count() FROM flows.flows_raw WHERE tenant_id = '"+demo.ISP.ID+"'")
		if err != nil {
			return 0, err
		}
		if len(rows) != 1 {
			return 0, fmt.Errorf("respuesta inesperada: %v", rows)
		}
		return strconv.Atoi(rows[0][0])
	}
	own, err := count(demo.ISP.ID)
	if err != nil {
		return err
	}
	other, err := count(w.isps["otro"].ID)
	if err != nil {
		return err
	}
	if own == 0 || other != 0 {
		return fmt.Errorf("row policies de ClickHouse: horus_analytics ve %d filas de demo con su tenant y %d con el tenant otro (se esperaba >0 y 0)", own, other)
	}
	w.logf("ok  ClickHouse: horus_analytics con SQL_horus_tenant=demo ve %d filas; con el tenant otro, 0", own)
	// PostgreSQL: RLS en toda tabla con tenant_id (salvo outbox), como en accept-i0.
	if ctr := os.Getenv("ACCEPT_PG_CONTAINER"); ctr != "" {
		out, err := w.run("docker", "exec", "-i", ctr, "psql", "-v", "ON_ERROR_STOP=1", "-U", env("ACCEPT_PG_USER", "horus"), "-d", env("ACCEPT_PG_DB", "horus"), "-Atc",
			`SELECT n.nspname || '.' || c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace JOIN pg_attribute a ON a.attrelid = c.oid
			 WHERE c.relkind IN ('r','p') AND NOT c.relispartition AND a.attname = 'tenant_id' AND NOT c.relrowsecurity
			   AND c.relname <> 'outbox' AND n.nspname NOT IN ('pg_catalog','information_schema')`)
		if err != nil {
			return err
		}
		if s := strings.TrimSpace(out); s != "" {
			return fmt.Errorf("tablas con tenant_id sin RLS: %s", s)
		}
		w.logf("ok  PostgreSQL: RLS en todas las tablas con tenant_id")
	}
	return nil
}

// --- silent ----------------------------------------------------------------------------------

func (w *world) silent() error {
	if err := w.waitSenders(); err != nil {
		return err
	}
	n := w.byName["normal"]
	limit := n.finished.Add(2*time.Minute + 30*time.Second) // 2 min + intervalo de estado del colector
	var last map[string]any
	err := eventually(time.Until(limit), 5*time.Second, func() error {
		e, err := w.exporterStates(n)
		if err != nil {
			return err
		}
		last = e
		if e["state"] != "silent" {
			return fmt.Errorf("exportador de normal en %v %s después de dejar de exportar (se esperaba silent en ≤ 2 min)", e["state"], time.Since(n.finished).Round(time.Second))
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Cuándo pasó a Silencioso (state_since), no cuándo lo miramos.
	since, perr := time.Parse(time.RFC3339Nano, fmt.Sprint(last["state_since"]))
	if perr != nil {
		return fmt.Errorf("exportador sin state_since válido: %v", compact(last))
	}
	if d := since.Sub(n.finished); d > 2*time.Minute+30*time.Second {
		return fmt.Errorf("el exportador de normal pasó a Silencioso %s después del último flujo (máximo 2 min + intervalo de estado)", d.Round(time.Second))
	}
	w.logf("ok  exportador de normal Silencioso %s después del último flujo: %v", since.Sub(n.finished).Round(time.Second), compact(last))
	return nil
}

// --- cleanup ---------------------------------------------------------------------------------

// cleanup suspende los ISP de prueba en una instalación que se queda en
// servicio (TARGET=installed); la instalación temporal se borra entera.
func (w *world) cleanup() error {
	if os.Getenv("ACCEPT_SUSPEND_TENANTS") != "1" {
		return skipErr{"instalación temporal (se desinstala con --purge)"}
	}
	_, _ = w.run(w.simrtr, "down-all")
	if err := w.s.Reauth(); err != nil {
		return err
	}
	plat := w.token("platform")
	for k, i := range w.isps {
		w.expect("suspender el ISP de prueba "+i.Slug, post(plat, "/api/v1/platform/tenants/"+i.ID+"/suspend",
			map[string]any{"reason": "ISP de prueba de make accept-i1 (" + k + ")"}), 200)
	}
	return nil
}
