//go:build acceptance

// Command horus-chaos-core es el driver de `make chaos-restart-core` (D23,
// tests/chaos/core/run.sh): matriz de reinicio brusco de horus-app,
// horus-wg-agent, PostgreSQL, NATS y Valkey sobre una instalación real con
// routers simulados por WireGuard.
//
// Prepara un ISP con dos routers simulados (túneles reales), un canal de
// alertas LibreNMS contra un receptor HTTP local (cuenta las entregas por
// X-Horus-Delivery-Id), un kiosco (cookie rotativa + WebSocket, tests/loadkit),
// un WebSocket de usuario, la sesión del superadministrador y flujos del
// escenario `scan` en bucle. Después aplica cada escenario (kill -9 del
// proceso principal del contenedor, que Docker reinicia por su política, o
// `docker restart`) en orden aleatorio, en un momento aleatorio y CHAOS_ROUNDS
// veces, y tras cada uno mide la recuperación y comprueba túneles, sesión,
// WebSocket y kiosco. Al final verifica hallazgos, alertas, outbox, eventos de
// plataforma y un paquete `horus diagnose`, escribe report.{md,json} y deja
// las credenciales del superadministrador para accept-i1.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/tests/acceptance/acceptkit"
	"github.com/hcdestroyer/horus-flow/tests/loadkit"
)

// Escenarios de la matriz: método × componente.
var allScenarios = []string{
	"kill:horus-app", "restart:horus-app",
	"kill:horus-wg-agent", "restart:horus-wg-agent",
	"kill:postgres", "restart:postgres",
	"kill:nats", "restart:nats",
	"kill:valkey", "restart:valkey",
}

type cfg struct {
	base, project, installDir, email, pwFile, pgUser, pgDB string
	state, simRouter, flowsim, acceptCreds                 string
	receiverPort, rounds                                   int
	seed                                                   uint64
	scenarios                                              []string
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	c := cfg{base: env("CHAOS_BASE_URL", "https://127.0.0.1:22443"), project: env("CHAOS_PROJECT", "horus-chaos-core"),
		installDir: os.Getenv("CHAOS_INSTALL_DIR"), email: env("CHAOS_ADMIN_EMAIL", "admin@horus.test"),
		pwFile: os.Getenv("CHAOS_ADMIN_PASSWORD_FILE"), pgUser: env("CHAOS_PG_USER", "horus"), pgDB: env("CHAOS_PG_DB", "horus"),
		state: env("CHAOS_STATE_DIR", "bin/chaos-core"), simRouter: env("CHAOS_SIM_ROUTER", "scripts/accept/sim-router.sh"),
		flowsim: env("CHAOS_FLOWSIM", "bin/chaos-core/flowsim"), acceptCreds: os.Getenv("CHAOS_ACCEPT_CREDS")}
	c.receiverPort, _ = strconv.Atoi(env("CHAOS_RECEIVER_PORT", "22999"))
	c.rounds, _ = strconv.Atoi(env("CHAOS_ROUNDS", "2"))
	c.seed = uint64(time.Now().UnixNano()) //nolint:gosec // semilla de la prueba
	if s := os.Getenv("CHAOS_SEED"); s != "" {
		c.seed, _ = strconv.ParseUint(s, 10, 64)
	}
	c.scenarios = allScenarios
	if s := os.Getenv("CHAOS_SCENARIOS"); s != "" {
		c.scenarios = strings.Split(s, ",")
	}
	r := &run{c: c, rnd: rand.New(rand.NewPCG(c.seed, c.seed^0x9e3779b97f4a7c15)), received: map[string]int{}} //nolint:gosec // orden de la prueba
	code := r.main()
	os.Exit(code)
}

// --- estado de la ejecución ------------------------------------------------------------------

type router struct {
	name, site, id, tunnelIP string
	idx                      int
	onb                      onboarding
}

type onboarding struct{ tunnelIP, hubKey, endpoint, port, services, collector, token string }

type scenarioResult struct {
	Round       int               `json:"round"`
	Scenario    string            `json:"scenario"`
	DelaySecond int               `json:"delay_seconds"`
	Recovery    map[string]string `json:"recovery"` // check → duración o error
	OK          bool              `json:"ok"`
	Errors      []string          `json:"errors,omitempty"`
}

type run struct {
	c   cfg
	rnd *rand.Rand
	api *loadkit.API
	ctx context.Context

	tenant  string
	routers []*router
	channel string
	scanExp *expected

	mu        sync.Mutex
	received  map[string]int // X-Horus-Delivery-Id → entregas recibidas
	accepted  []string       // ids de entregas de prueba aceptadas (202)
	wsEvents  map[string]time.Time
	wsConnAt  time.Time
	kiosk     *loadkit.Kiosk
	results   []scenarioResult
	final     map[string]string
	finalOK   bool
	killsApp  int
	kills     map[string]int
	restarts  map[string]int
	logf      func(string, ...any)
	sessToken string
}

func (r *run) main() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.ctx = ctx
	r.kills, r.restarts, r.wsEvents, r.final = map[string]int{}, map[string]int{}, map[string]time.Time{}, map[string]string{}
	r.logf = func(f string, a ...any) {
		fmt.Printf("%s  %s\n", time.Now().UTC().Format("15:04:05"), fmt.Sprintf(f, a...))
	}
	r.logf("semilla %d, %d vueltas, escenarios %v", r.c.seed, r.c.rounds, r.c.scenarios)
	if err := r.setup(ctx); err != nil {
		r.logf("PREPARACIÓN FALLIDA: %v", err)
		r.final["setup"] = err.Error()
		r.writeReport()
		return 1
	}
	loadCtx, stopLoad := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); r.flowsLoop(loadCtx) }()
	go func() { defer wg.Done(); r.testNotifications(loadCtx) }()
	go func() { defer wg.Done(); r.userWS(loadCtx) }()
	go r.kiosk.Run(loadCtx)
	time.Sleep(20 * time.Second) // la carga en marcha antes del primer fallo
	ok := true
	for round := 1; round <= r.c.rounds; round++ {
		order := slices.Clone(r.c.scenarios)
		r.rnd.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		for _, sc := range order {
			res := r.scenario(ctx, round, sc)
			r.results = append(r.results, res)
			ok = ok && res.OK
			r.writeReport()
		}
	}
	stopLoad()
	wg.Wait()
	r.finalOK = r.verify(ctx)
	r.writeAcceptCreds()
	r.writeReport()
	if ok && r.finalOK {
		r.logf("matriz de reinicio: OK")
		return 0
	}
	r.logf("matriz de reinicio: KO")
	return 1
}

// --- preparación ------------------------------------------------------------------------------

type expected struct {
	Findings []struct {
		Client string `json:"client"`
		Kind   string `json:"kind"`
	} `json:"findings"`
	Exporters []struct {
		IPv6ClientLen int `json:"ipv6_client_len"`
		Prefixes      struct {
			Customers      []string `json:"customers"`
			Infrastructure []string `json:"infrastructure"`
		} `json:"prefixes"`
	} `json:"exporters"`
}

func (r *run) setup(ctx context.Context) error {
	api, err := loadkit.NewAPI(r.c.base, r.c.email, r.c.pwFile, filepath.Join(r.c.state, "admin-state.json"))
	if err != nil {
		return err
	}
	jar, _ := cookiejar.New(nil)
	api.HTTP = &http.Client{Timeout: 30 * time.Second, Jar: jar}
	r.api = api
	b, err := os.ReadFile("tools/flowsim/fixtures/sim/scan/ipfix.expected.json")
	if err != nil {
		return err
	}
	r.scanExp = &expected{}
	if err := json.Unmarshal(b, r.scanExp); err != nil {
		return err
	}
	// ISP con un nodo por router: "scan" exporta el escenario; "idle" solo mantiene el túnel.
	pt, err := api.Platform(ctx)
	if err != nil {
		return err
	}
	slug := "chaos-core-" + strings.ToLower(uuid.NewString()[:6])
	t, err := api.Do(ctx, loadkit.Call{Method: http.MethodPost, Path: "/api/v1/platform/tenants", Token: pt,
		Header: map[string]string{"Idempotency-Key": uuid.NewString()},
		Body: map[string]any{"slug": slug, "name": "Caos " + slug, "country": "MX", "timezone": "America/Mexico_City",
			"initial_admin_email": "noc@" + slug + ".example.net"}})
	if err != nil || t.Status != http.StatusCreated {
		return fmt.Errorf("alta del ISP: %v %d %s", err, t.Status, t.Raw)
	}
	r.tenant = t.Str("id")
	if err := api.SaveState(func(s *loadkit.State) { s.TenantID = r.tenant }); err != nil {
		return err
	}
	_, _ = r.sh(ctx, r.c.simRouter, "down-all")
	for i, name := range []string{"scan", "idle"} {
		rt := &router{name: "chaos-" + name, idx: 41 + i}
		if err := r.addRouter(ctx, rt, name == "scan"); err != nil {
			return fmt.Errorf("router %s: %w", rt.name, err)
		}
		r.routers = append(r.routers, rt)
	}
	for _, rt := range r.routers {
		if _, err := r.waitHandshake(ctx, rt, 90*time.Second); err != nil {
			return err
		}
	}
	r.logf("ok  ISP %s con %d routers simulados y túneles WireGuard activos", slug, len(r.routers))
	if err := r.alertsChannel(ctx); err != nil {
		return fmt.Errorf("canal de alertas: %w", err)
	}
	k, err := loadkit.NewKiosk(ctx, api)
	if err != nil {
		return fmt.Errorf("kiosco: %w", err)
	}
	r.kiosk = k
	if _, err := r.refreshSession(ctx); err != nil {
		return fmt.Errorf("sesión: %w", err)
	}
	r.logf("ok  canal LibreNMS, kiosco enrolado y sesión con cookie de refresh")
	return nil
}

func (r *run) tenantDo(ctx context.Context, c loadkit.Call, what string, status int) (loadkit.Resp, error) {
	resp, err := r.api.TenantDo(ctx, c)
	if err != nil {
		return resp, fmt.Errorf("%s: %w", what, err)
	}
	if resp.Status != status {
		return resp, fmt.Errorf("%s: HTTP %d (se esperaba %d): %s", what, resp.Status, status, trunc(resp.Raw))
	}
	return resp, nil
}

func (r *run) addRouter(ctx context.Context, rt *router, prefixes bool) error {
	code := strings.ToUpper(strings.TrimPrefix(rt.name, "chaos-"))
	site, err := r.tenantDo(ctx, loadkit.Call{Method: http.MethodPost, Path: "/api/v1/sites",
		Body: map[string]any{"name": "Nodo " + rt.name, "code": code}}, "alta del nodo", 201)
	if err != nil {
		return err
	}
	rt.site = site.Str("id")
	if prefixes {
		ex := r.scanExp.Exporters[0]
		add := func(list []string, role string) error {
			for _, p := range list {
				body := map[string]any{"prefix": p, "role": role}
				if strings.Contains(p, ":") && role == "customers" {
					body["ipv6_client_len"] = ex.IPv6ClientLen
				}
				if _, err := r.tenantDo(ctx, loadkit.Call{Method: http.MethodPost, Path: "/api/v1/sites/" + rt.site + "/client-prefixes",
					Body: body}, "prefijo "+p, 201); err != nil {
					return err
				}
			}
			return nil
		}
		if err := add(ex.Prefixes.Customers, "customers"); err != nil {
			return err
		}
		if err := add(ex.Prefixes.Infrastructure, "infrastructure"); err != nil {
			return err
		}
	}
	rr, err := r.tenantDo(ctx, loadkit.Call{Method: http.MethodPost, Path: "/api/v1/routers", Body: map[string]any{
		"site_id": rt.site, "name": rt.name, "model": "CCR2116-12G-4S+", "routeros_version": "7.16.1"}}, "alta del router", 201)
	if err != nil {
		return err
	}
	rt.id = rr.Str("id")
	err = eventually(ctx, 90*time.Second, 2*time.Second, func() error {
		g, err := r.tenantDo(ctx, loadkit.Call{Method: http.MethodGet, Path: "/api/v1/routers/" + rt.id}, "router", 200)
		if err != nil {
			return err
		}
		if rt.tunnelIP = strings.TrimSuffix(g.Str("tunnel_address"), "/32"); rt.tunnelIP == "" {
			return errors.New("sin tunnel_address")
		}
		return nil
	})
	if err != nil {
		return err
	}
	sc, err := r.tenantDo(ctx, loadkit.Call{Method: http.MethodPost, Path: "/api/v1/routers/" + rt.id + "/provisioning-script",
		Header: map[string]string{"Idempotency-Key": uuid.NewString()}, Body: map[string]any{"routeros_version": "7.12"}}, "script", 201)
	if err != nil {
		return err
	}
	if rt.onb, err = parseScript(string(sc.Raw)); err != nil {
		return err
	}
	out, err := r.sh(ctx, r.c.simRouter, "up", rt.name, strconv.Itoa(rt.idx), rt.onb.tunnelIP, rt.onb.hubKey, rt.onb.endpoint, rt.onb.port,
		rt.onb.services)
	if err != nil {
		return err
	}
	pub := ""
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "PUBKEY="); ok {
			pub = v
		}
	}
	for i := 0; i < 8; i++ {
		e, err := r.api.Do(ctx, loadkit.Call{Method: http.MethodPost, Path: "/api/v1/enroll/wireguard",
			Body: map[string]string{"token": rt.onb.token, "public_key": pub}})
		if err == nil && e.Status == http.StatusAccepted {
			return nil
		}
		if err == nil && e.Status != http.StatusTooManyRequests {
			return fmt.Errorf("enroll: HTTP %d %s", e.Status, trunc(e.Raw))
		}
		time.Sleep(10 * time.Second)
	}
	return errors.New("enroll: límite por IP")
}

var (
	reAddr     = regexp.MustCompile(`/ip address add address=([0-9.]+)/32 interface=wg-horus`)
	reHub      = regexp.MustCompile(`public-key="([A-Za-z0-9+/]{43}=)"`)
	reEndpoint = regexp.MustCompile(`endpoint-address=(\S+) endpoint-port=(\d+)`)
	reServices = regexp.MustCompile(`allowed-address=([0-9./]+)`)
	reTarget   = regexp.MustCompile(`/ip traffic-flow target add dst-address=([0-9.]+) port=4739`)
	reToken    = regexp.MustCompile(`\\"token\\":\\"([A-Za-z0-9_-]{20,})\\"`)
)

func parseScript(s string) (onboarding, error) {
	var o onboarding
	find := func(re *regexp.Regexp, dst ...*string) error {
		m := re.FindStringSubmatch(s)
		if m == nil {
			return fmt.Errorf("el script no contiene %s", re)
		}
		for i, d := range dst {
			*d = m[i+1]
		}
		return nil
	}
	return o, errors.Join(find(reAddr, &o.tunnelIP), find(reHub, &o.hubKey), find(reEndpoint, &o.endpoint, &o.port),
		find(reServices, &o.services), find(reTarget, &o.collector), find(reToken, &o.token))
}

// alertsChannel levanta el receptor LibreNMS y da de alta el canal.
func (r *run) alertsChannel(ctx context.Context) error {
	gw, err := docker(ctx, "network", "inspect", r.c.project+"_horus", "-f", "{{(index .IPAM.Config 0).Gateway}}")
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", r.c.receiverPort))
	if err != nil {
		return err
	}
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		if id := req.Header.Get("X-Horus-Delivery-Id"); id != "" {
			r.mu.Lock()
			r.received[id]++
			r.mu.Unlock()
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})}
	go func() { _ = srv.Serve(ln) }()
	ch, err := r.tenantDo(ctx, loadkit.Call{Method: http.MethodPost, Path: "/api/v1/notification-channels", Body: map[string]any{
		"name": "LibreNMS caos", "kind": "librenms",
		"config":       map[string]any{"base_url": fmt.Sprintf("http://%s:%d", gw, r.c.receiverPort), "username": "horus", "timeout_seconds": 5},
		"subscription": map[string]any{"event_types": []string{"finding_opened", "finding_reopened", "tunnel_down", "tunnel_recovered"}, "throttle_minutes": 0},
	}}, "alta del canal", 201)
	if err != nil {
		return err
	}
	r.channel = ch.Str("id")
	_, err = r.tenantDo(ctx, loadkit.Call{Method: http.MethodPut, Path: "/api/v1/notification-channels/" + r.channel + "/credentials",
		Body: map[string]any{"api_token": "chaos-core-token-" + strings.ReplaceAll(uuid.NewString(), "-", "")}}, "credenciales", 204)
	return err
}

// --- carga de fondo ---------------------------------------------------------------------------

// flowsLoop exporta el escenario scan por el túnel una y otra vez.
func (r *run) flowsLoop(ctx context.Context) {
	rt := r.routers[0]
	for i := 0; ctx.Err() == nil; i++ {
		logf, _ := os.Create(filepath.Join(r.c.state, "flowsim.log"))                                                                   //nolint:gosec // log de la prueba
		cmd := exec.CommandContext(ctx, r.c.simRouter, "exec", rt.name, r.c.flowsim, "-scenario", "scan", "-seed", strconv.Itoa(1+i%3), //nolint:gosec // prueba
			"-proto", "ipfix", "-fixture", "-target", rt.onb.collector+":4739", "-src", rt.tunnelIP, "-speed", "1",
			"-expected", filepath.Join(r.c.state, "scan.expected.json"))
		cmd.Stdout, cmd.Stderr = logf, logf
		_ = cmd.Run()
		_ = logf.Close()
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}

// testNotifications pide una prueba del canal cada 15 s: cada 202 es una
// entrega que debe llegar exactamente una vez.
func (r *run) testNotifications(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		resp, err := r.api.TenantDo(cctx, loadkit.Call{Method: http.MethodPost, Path: "/api/v1/notification-channels/" + r.channel + "/test"})
		cancel()
		if err == nil && resp.Status == http.StatusAccepted {
			if id := resp.Str("id"); id != "" {
				r.mu.Lock()
				r.accepted = append(r.accepted, id)
				r.mu.Unlock()
			}
		}
	}
}

// userWS mantiene un WebSocket de usuario suscrito a `routers` (reconexión cada 2 s).
func (r *run) userWS(ctx context.Context) {
	for ctx.Err() == nil {
		if err := r.wsSession(ctx); err != nil && ctx.Err() == nil {
			time.Sleep(2 * time.Second)
		}
	}
}

func (r *run) wsSession(ctx context.Context) error {
	tk, err := r.api.TenantDo(ctx, loadkit.Call{Method: http.MethodPost, Path: "/api/v1/ws/tickets"})
	if err != nil || tk.Status != http.StatusCreated {
		return fmt.Errorf("ticket: %v %d", err, tk.Status)
	}
	u := "ws" + strings.TrimPrefix(r.c.base, "http") + "/api/v1/ws?ticket=" + tk.Str("ticket")
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	c, _, err := websocket.Dial(dctx, u, &websocket.DialOptions{Subprotocols: []string{"horus.ws.v1"}})
	cancel()
	if err != nil {
		return err
	}
	defer func() { _ = c.CloseNow() }()
	sub, _ := json.Marshal(map[string]any{"type": "subscribe", "id": "s-1", "topic": "routers"})
	if err := c.Write(ctx, websocket.MessageText, sub); err != nil {
		return err
	}
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return err
		}
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		switch m["type"] {
		case "ack":
			r.mu.Lock()
			r.wsConnAt = time.Now()
			r.mu.Unlock()
		case "event":
			if ev, ok := m["event"].(map[string]any); ok {
				if s, _ := ev["subject"].(string); s != "" {
					r.mu.Lock()
					r.wsEvents[s] = time.Now()
					r.mu.Unlock()
				}
			}
		}
	}
}

// --- escenarios -------------------------------------------------------------------------------

func (r *run) container(svc string) string { return r.c.project + "-" + svc + "-1" }

func (r *run) scenario(ctx context.Context, round int, sc string) scenarioResult {
	method, svc, _ := strings.Cut(sc, ":")
	res := scenarioResult{Round: round, Scenario: sc, Recovery: map[string]string{}}
	res.DelaySecond = 5 + r.rnd.IntN(36)
	r.logf("==> vuelta %d · %s dentro de %d s", round, sc, res.DelaySecond)
	time.Sleep(time.Duration(res.DelaySecond) * time.Second)
	t0 := time.Now()
	var err error
	switch method {
	case "kill":
		var pid string
		if pid, err = docker(ctx, "inspect", "-f", "{{.State.Pid}}", r.container(svc)); err == nil {
			err = exec.CommandContext(ctx, "kill", "-9", pid).Run() //nolint:gosec // pid del contenedor
		}
		r.kills[svc]++
		if svc == "horus-app" {
			r.killsApp++
		}
	case "restart":
		_, err = docker(ctx, "restart", "-t", "10", r.container(svc))
		r.restarts[svc]++
	default:
		err = fmt.Errorf("escenario desconocido %q", sc)
	}
	if err != nil {
		res.Errors = append(res.Errors, "fallo al aplicar: "+err.Error())
		return res
	}
	check := func(name string, d time.Duration, f func() error) {
		start := time.Now()
		if err := eventually(ctx, d, 2*time.Second, f); err != nil {
			res.Recovery[name] = "FAIL: " + err.Error()
			res.Errors = append(res.Errors, name+": "+err.Error())
			return
		}
		res.Recovery[name] = time.Since(t0).Round(time.Second).String()
		_ = start
	}
	check("container_healthy", 4*time.Minute, func() error { return r.healthy(ctx, svc) })
	check("app_healthy", 4*time.Minute, func() error { return r.healthy(ctx, "horus-app") })
	check("api", 2*time.Minute, func() error {
		resp, err := r.api.Do(ctx, loadkit.Call{Method: http.MethodGet, Path: "/api/v1/system/status"})
		if err != nil || resp.Status != 200 {
			return fmt.Errorf("system/status: %v %d", err, resp.Status)
		}
		return nil
	})
	check("session", 2*time.Minute, func() error { _, err := r.refreshSession(ctx); return err })
	check("tunnels", 4*time.Minute, func() error {
		for _, rt := range r.routers {
			age, err := r.handshakeAge(ctx, rt)
			if err != nil {
				return err
			}
			if age > 150 {
				return fmt.Errorf("%s: último handshake hace %d s", rt.name, age)
			}
		}
		return nil
	})
	check("websocket", 2*time.Minute, func() error { return r.wsProbe(ctx) })
	check("kiosk", 2*time.Minute, func() error {
		st := r.kiosk.Status()
		if !st.Connected || st.HTTPStatus != 200 || st.HTTPOKAt.Before(t0) {
			return fmt.Errorf("kiosco: conectado=%v http=%d (%s)", st.Connected, st.HTTPStatus, st.LastErr)
		}
		return nil
	})
	res.OK = len(res.Errors) == 0
	r.logf("    %s: %v", map[bool]string{true: "OK", false: "FAIL"}[res.OK], res.Recovery)
	return res
}

func (r *run) healthy(ctx context.Context, svc string) error {
	st, err := docker(ctx, "inspect", "-f", "{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}", r.container(svc))
	if err != nil {
		return err
	}
	if st != "running healthy" && st != "running none" {
		return fmt.Errorf("%s: %s", svc, st)
	}
	return nil
}

// refreshSession renueva la sesión del superadministrador con su cookie de
// refresh (la sesión sobrevive al reinicio) y comprueba que el token emitido
// antes del fallo sigue siendo válido (claves de firma persistentes).
func (r *run) refreshSession(ctx context.Context) (string, error) {
	if r.sessToken == "" {
		tok, err := r.api.Session(ctx)
		if err != nil {
			return "", err
		}
		r.sessToken = tok
	}
	if me, err := r.api.Do(ctx, loadkit.Call{Method: http.MethodGet, Path: "/api/v1/me", Token: r.sessToken}); err != nil {
		return "", err
	} else if me.Status != 200 && me.Status != 401 {
		return "", fmt.Errorf("/me con el token anterior: HTTP %d", me.Status)
	} else if me.Status == 401 && !strings.Contains(string(me.Raw), "TOKEN_EXPIRED") {
		return "", fmt.Errorf("token emitido antes del reinicio rechazado: %s", trunc(me.Raw))
	}
	rf, err := r.api.Do(ctx, loadkit.Call{Method: http.MethodPost, Path: "/api/v1/auth/refresh",
		Header: map[string]string{"X-Requested-With": "horus", "Origin": r.c.base}})
	if err != nil {
		return "", err
	}
	if rf.Status != 200 || rf.Str("access_token") == "" {
		return "", fmt.Errorf("refresh: HTTP %d %s", rf.Status, trunc(rf.Raw))
	}
	r.sessToken = rf.Str("access_token")
	return r.sessToken, nil
}

// wsProbe crea un nodo y espera su evento por el WebSocket de usuario
// (HTTP → outbox → relay → NATS → hub → WebSocket).
func (r *run) wsProbe(ctx context.Context) error {
	code := "W" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:4])
	s, err := r.tenantDo(ctx, loadkit.Call{Method: http.MethodPost, Path: "/api/v1/sites", Body: map[string]any{"name": "Sonda " + code, "code": code}},
		"sonda", 201)
	if err != nil {
		return err
	}
	id := s.Str("id")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		_, ok := r.wsEvents[id]
		r.mu.Unlock()
		if ok {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("el evento del nodo creado no llegó por WebSocket en 20 s")
}

func (r *run) handshakeAge(ctx context.Context, rt *router) (int, error) {
	out, err := r.sh(ctx, r.c.simRouter, "handshake", rt.name)
	if err != nil {
		return 0, err
	}
	out = strings.TrimSpace(out)
	if out == "never" {
		return 1 << 30, nil
	}
	return strconv.Atoi(out)
}

func (r *run) waitHandshake(ctx context.Context, rt *router, d time.Duration) (int, error) {
	var age int
	err := eventually(ctx, d, 2*time.Second, func() error {
		var err error
		if age, err = r.handshakeAge(ctx, rt); err != nil {
			return err
		}
		if age > 120 {
			return fmt.Errorf("%s sin handshake reciente (%d)", rt.name, age)
		}
		return nil
	})
	return age, err
}

// --- verificación final ----------------------------------------------------------------------

func (r *run) psql(ctx context.Context, q string) (string, error) {
	return docker(ctx, "exec", r.container("postgres"), "psql", "-U", r.c.pgUser, "-d", r.c.pgDB, "-At", "-F", "|", "-c", q)
}

func (r *run) verify(ctx context.Context) bool {
	ok := true
	fail := func(k, f string, a ...any) {
		ok = false
		r.final[k] = "FAIL: " + fmt.Sprintf(f, a...)
		r.logf("FAIL %s: %s", k, r.final[k])
	}
	pass := func(k, f string, a ...any) {
		r.final[k] = "OK: " + fmt.Sprintf(f, a...)
		r.logf("ok  %s: %s", k, r.final[k])
	}

	// Outbox sin eventos colgados (todo publicado en ≤ 5 min).
	var pending string
	err := eventually(ctx, 5*time.Minute, 5*time.Second, func() error {
		schemas, err := r.psql(ctx, "SELECT table_schema FROM information_schema.tables WHERE table_name='outbox' ORDER BY 1")
		if err != nil {
			return err
		}
		var parts []string
		total := 0
		for _, s := range strings.Fields(schemas) {
			n, err := r.psql(ctx, fmt.Sprintf("SELECT count(*) FROM %q.outbox WHERE published_at IS NULL", s))
			if err != nil {
				return err
			}
			c, _ := strconv.Atoi(strings.TrimSpace(n))
			total += c
			parts = append(parts, s+"="+strings.TrimSpace(n))
		}
		pending = strings.Join(parts, " ")
		if total > 0 {
			return fmt.Errorf("pendientes: %s", pending)
		}
		return nil
	})
	if err != nil {
		fail("outbox", "%v", err)
	} else {
		pass("outbox", "0 eventos pendientes (%s)", pending)
	}

	// Alertas: cada entrega aceptada (prueba) o de un hallazgo abierto llega una vez.
	err = eventually(ctx, 5*time.Minute, 5*time.Second, func() error {
		q, err := r.psql(ctx, "SELECT count(*) FROM alerts.notification_delivery WHERE status='queued'")
		if err != nil {
			return err
		}
		if strings.TrimSpace(q) != "0" {
			return fmt.Errorf("%s entregas en cola", strings.TrimSpace(q))
		}
		return nil
	})
	if err != nil {
		fail("alerts_queue", "%v", err)
	}
	rows, err := r.psql(ctx, "SELECT id, status, attempts, coalesce(event_type,'test') FROM alerts.notification_delivery WHERE channel_id='"+r.channel+"'")
	if err != nil {
		fail("alerts", "%v", err)
	} else {
		r.mu.Lock()
		received := map[string]int{}
		for k, v := range r.received {
			received[k] = v
		}
		accepted := slices.Clone(r.accepted)
		r.mu.Unlock()
		status := map[string]string{}
		var lost, dupAllowed, dupBad []string
		sent, findingAlerts := 0, 0
		for _, line := range strings.Split(strings.TrimSpace(rows), "\n") {
			f := strings.Split(line, "|")
			if len(f) != 4 {
				continue
			}
			status[f[0]] = f[1]
			attempts, _ := strconv.Atoi(f[2])
			if f[3] == "finding_opened" || f[3] == "finding_reopened" {
				findingAlerts++
			}
			switch {
			case f[1] == "sent" && received[f[0]] == 0:
				lost = append(lost, f[0]+" (sent sin recibir)")
			case f[1] == "sent" && received[f[0]] > 1 && attempts > 1:
				dupAllowed = append(dupAllowed, f[0])
			case f[1] == "sent" && received[f[0]] > 1:
				dupBad = append(dupBad, f[0])
			case f[1] != "sent" && f[1] != "throttled":
				lost = append(lost, f[0]+" ("+f[1]+")")
			}
			if f[1] == "sent" {
				sent++
			}
		}
		for _, id := range accepted {
			if _, ok := status[id]; !ok {
				lost = append(lost, id+" (aceptada y sin registro)")
			}
		}
		if len(lost) > 0 || len(dupBad) > 0 {
			fail("alerts", "perdidas %v, duplicadas sin reintento %v", lost, dupBad)
		} else {
			pass("alerts", "%d entregas enviadas (%d pruebas aceptadas durante el caos, %d de hallazgos), ninguna perdida; duplicados por reintento tras kill (deduplicables por X-Horus-Delivery-Id): %d",
				sent, len(accepted), findingAlerts, len(dupAllowed))
		}
	}

	// Hallazgos: los esperados del escenario scan, sin duplicados activos.
	dups, err := r.psql(ctx, "SELECT count(*) FROM (SELECT customer_id, kind, target_type, target_value FROM detection.finding WHERE tenant_id='"+r.tenant+
		"' AND state IN ('open','acknowledged') GROUP BY 1,2,3,4 HAVING count(*)>1) d")
	if err != nil {
		fail("findings", "%v", err)
	} else {
		got, _ := r.psql(ctx, "SELECT host(address), kind FROM detection.finding WHERE tenant_id='"+r.tenant+"'")
		have := map[string]bool{}
		for _, l := range strings.Split(strings.TrimSpace(got), "\n") {
			if a, k, ok := strings.Cut(l, "|"); ok {
				have[a+"|"+k] = true
			}
		}
		var missing []string
		for _, f := range r.scanExp.Findings {
			key := clientHost(f.Client) + "|" + f.Kind
			if !have[key] {
				missing = append(missing, key)
			}
		}
		opened, _ := r.psql(ctx, "SELECT count(*) FROM detection.finding WHERE tenant_id='"+r.tenant+"'")
		if strings.TrimSpace(dups) != "0" || len(missing) > 0 {
			fail("findings", "duplicados activos %s, esperados que faltan %v", strings.TrimSpace(dups), missing)
		} else {
			pass("findings", "%s hallazgos, los %d esperados del escenario scan presentes, 0 duplicados activos", strings.TrimSpace(opened), len(r.scanExp.Findings))
		}
	}

	// Eventos de plataforma: paradas no limpias y caídas registradas.
	ev, err := r.psql(ctx, "SELECT kind, count(*) FROM platform_events.event GROUP BY 1 ORDER BY 1")
	if err != nil {
		fail("platform_events", "%v", err)
	} else {
		counts := map[string]int{}
		for _, l := range strings.Split(strings.TrimSpace(ev), "\n") {
			if k, n, ok := strings.Cut(l, "|"); ok {
				counts[k], _ = strconv.Atoi(n)
			}
		}
		if counts["unclean_shutdown"] < r.killsApp {
			fail("platform_events", "%d kill -9 de horus-app y %d unclean_shutdown (%v)", r.killsApp, counts["unclean_shutdown"], counts)
		} else {
			pass("platform_events", "%v", counts)
		}
	}

	// Paquete de diagnóstico sin IPs de clientes.
	if err := r.diagnose(ctx); err != nil {
		fail("diagnose", "%v", err)
	} else {
		pass("diagnose", "bin/chaos-core/diagnose.tar.gz sin IPs de clientes del escenario")
	}

	// Túneles al final.
	for _, rt := range r.routers {
		if age, err := r.waitHandshake(ctx, rt, 3*time.Minute); err != nil {
			fail("tunnels", "%v", err)
		} else {
			pass("tunnel_"+rt.name, "handshake hace %d s", age)
		}
	}
	return ok
}

// clientHost normaliza la clave de cliente del expected (IP o prefijo /64).
func clientHost(c string) string {
	if p, err := netip.ParsePrefix(c); err == nil {
		return p.Addr().String()
	}
	return c
}

func (r *run) diagnose(ctx context.Context) error {
	if r.c.installDir == "" {
		return errors.New("sin CHAOS_INSTALL_DIR")
	}
	out := filepath.Join(r.c.state, "diagnose.tar.gz")
	f, err := os.Create(out) //nolint:gosec // salida de la prueba
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "docker", "compose", "--project-directory", r.c.installDir, "-f", filepath.Join(r.c.installDir, "compose.yaml"), //nolint:gosec // prueba
		"--env-file", filepath.Join(r.c.installDir, ".env"), "run", "--rm", "--no-deps", "-T", "--user", "0:0",
		"-v", "/var/run/docker.sock:/var/run/docker.sock:ro", "horus-app", "diagnose", "--output=-", "--since=6h")
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = f, &stderr
	err = cmd.Run()
	_ = f.Close()
	if err != nil {
		return fmt.Errorf("horus diagnose: %w: %s", err, trunc(stderr.Bytes()))
	}
	files, err := readTarGz(out)
	if err != nil {
		return err
	}
	for _, want := range []string{"manifest.json", "platform-events.json", "nats/streams.json", "postgres/outbox.json"} {
		if _, ok := files[want]; !ok {
			return fmt.Errorf("al paquete le falta %s", want)
		}
	}
	logs := 0
	for name := range files {
		if strings.HasPrefix(name, "logs/") {
			logs++
		}
	}
	if logs == 0 {
		return errors.New("el paquete no trae logs de contenedores")
	}
	var clients []string
	for _, f := range r.scanExp.Findings {
		clients = append(clients, clientHost(f.Client))
	}
	for name, data := range files {
		for _, ip := range clients {
			if bytes.Contains(data, []byte(ip)) {
				return fmt.Errorf("%s contiene la IP de cliente %s", name, ip)
			}
		}
	}
	return nil
}

func readTarGz(path string) (map[string][]byte, error) {
	f, err := os.Open(path) //nolint:gosec // salida de la prueba
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		b, _ := io.ReadAll(tr)
		out[strings.TrimPrefix(h.Name, "horus-diagnose/")] = b
	}
}

// writeAcceptCreds deja las credenciales del superadministrador en el
// formato de acceptkit para el accept-i1 posterior (TARGET=installed).
func (r *run) writeAcceptCreds() {
	if r.c.acceptCreds == "" || r.api == nil {
		return
	}
	st := r.api.State()
	_ = os.MkdirAll(filepath.Dir(r.c.acceptCreds), 0o700)
	_ = acceptkit.SaveCreds(r.c.acceptCreds, acceptkit.Creds{Email: r.c.email, Password: st.Password, TOTPSecret: st.TOTPSecret,
		LastStep: time.Now().Unix() / 30})
}

// --- informe ----------------------------------------------------------------------------------

func (r *run) writeReport() {
	type report struct {
		Seed      uint64            `json:"seed"`
		Rounds    int               `json:"rounds"`
		Scenarios []scenarioResult  `json:"scenarios"`
		Final     map[string]string `json:"final"`
		Kills     map[string]int    `json:"kills"`
		Restarts  map[string]int    `json:"restarts"`
	}
	rep := report{Seed: r.c.seed, Rounds: r.c.rounds, Scenarios: r.results, Final: r.final, Kills: r.kills, Restarts: r.restarts}
	b, _ := json.MarshalIndent(rep, "", "  ")
	_ = os.WriteFile(filepath.Join(r.c.state, "report.json"), b, 0o600)
	var md strings.Builder
	fmt.Fprintf(&md, "# Matriz de reinicio brusco (make chaos-restart-core)\n\nSemilla %d · %d vueltas · %s\n\n", r.c.seed, r.c.rounds,
		time.Now().UTC().Format(time.RFC3339))
	md.WriteString("| Vuelta | Escenario | Espera | Contenedor | horus-app | API | Sesión | Túneles | WebSocket | Kiosco | Resultado |\n")
	md.WriteString("|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, s := range r.results {
		cell := func(k string) string {
			v := s.Recovery[k]
			if strings.HasPrefix(v, "FAIL") {
				return "FAIL"
			}
			return v
		}
		fmt.Fprintf(&md, "| %d | %s | %d s | %s | %s | %s | %s | %s | %s | %s | %s |\n", s.Round, s.Scenario, s.DelaySecond,
			cell("container_healthy"), cell("app_healthy"), cell("api"), cell("session"), cell("tunnels"), cell("websocket"), cell("kiosk"),
			map[bool]string{true: "OK", false: "FAIL"}[s.OK])
	}
	md.WriteString("\nTiempos desde el fallo hasta que cada comprobación pasa.\n\n## Verificación final\n\n")
	keys := make([]string, 0, len(r.final))
	for k := range r.final {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Fprintf(&md, "- **%s**: %s\n", k, r.final[k])
	}
	for _, s := range r.results {
		for _, e := range s.Errors {
			fmt.Fprintf(&md, "- vuelta %d %s: %s\n", s.Round, s.Scenario, e)
		}
	}
	_ = os.WriteFile(filepath.Join(r.c.state, "report.md"), []byte(md.String()), 0o600)
}

// --- utilidades -------------------------------------------------------------------------------

func (r *run) sh(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // órdenes de la prueba
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args, " "), err, trunc(out))
	}
	return string(out), nil
}

func docker(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, trunc(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func eventually(ctx context.Context, d, every time.Duration, f func() error) error {
	deadline := time.Now().Add(d)
	var err error
	for {
		if err = f(); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		}
	}
}

func trunc(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}
