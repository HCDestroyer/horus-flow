// Command chaos son las pruebas de fallo de I1 (historia I1-26, `make chaos-i1`).
//
// Contra el compose de tests/load/stack.sh y con el simulador enviando flujos
// en tiempo real (CHAOS_RATE, 5 000/s por defecto) durante todo el escenario:
//
//   - clickhouse, nats, app: se detiene el servicio CHAOS_DOWN (30 s), se
//     arranca y se espera a que vuelva healthy. Criterio: ninguna pérdida
//     más allá del búfer documentado (TLM_FLOWS para ClickHouse y horus-app,
//     búfer en memoria del collector para NATS): enviados = filas en
//     flows_raw tras el drenaje. El kiosco (WebSocket + /kiosk/config + datos
//     de widgets) debe recuperarse solo: reconecta y recibe un evento nuevo.
//   - collector: se detiene el collector CHAOS_COLLECTOR_DOWN (1 min).
//     Criterio: lo enviado durante la caída se pierde (UDP sin reintento,
//     como un router), el hueco es ausencia de filas (no filas a cero) y la
//     serie de la API no tiene ceros, y el exportador pasa por Silencioso y
//     vuelve a Exportando.
//
// CHAOS_SCENARIOS=clickhouse,nats,app,collector elige los escenarios.
// Escribe bin/load/chaos.{json,md}; sale con 1 si algún escenario falla.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/hcdestroyer/horus-flow/tests/loadkit"
)

type result struct {
	Scenario      string   `json:"scenario"`
	Down          string   `json:"down"`
	Recovery      string   `json:"recovery"` // de arrancar a healthy
	Sent          uint64   `json:"sent_records"`
	Rows          uint64   `json:"clickhouse_rows"`
	Lost          int64    `json:"lost_records"`
	LostSeconds   float64  `json:"lost_seconds_of_flows"`
	CollectorDrop float64  `json:"collector_drops"`
	MaxLagBatches uint64   `json:"max_lag_batches"`
	MaxStreamMB   float64  `json:"max_tlm_flows_mb"`
	MaxBufferMB   float64  `json:"collector_max_buffer_mb"`
	Drain         string   `json:"drain"`
	KioskBack     string   `json:"kiosk_back"` // de healthy a WS reconectado + evento recibido
	KioskDisc     int      `json:"kiosk_disconnects"`
	EventLatency  string   `json:"event_latency"`
	States        []string `json:"exporter_states,omitempty"`
	GapBuckets    string   `json:"gap,omitempty"`
	Pass          bool     `json:"pass"`
	Why           []string `json:"why,omitempty"`
	Notes         []string `json:"notes,omitempty"`
}

func main() {
	scen := envOr("CHAOS_SCENARIOS", "clickhouse,nats,app,collector")
	rate := floatEnv("CHAOS_RATE", 5000)
	down := durEnv("CHAOS_DOWN", 30*time.Second)
	colDown := durEnv("CHAOS_COLLECTOR_DOWN", time.Minute)
	flowsim := envOr("FLOWSIM", "bin/load/flowsim")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	env, err := loadkit.LoadEnv()
	check(err)
	api, err := loadkit.NewAPI(env.APIURL, env.AdminEmail, env.AdminPasswordFile, filepath.Join(env.Dir, "state.json"))
	check(err)
	st, err := api.Setup(ctx, loadkit.ClientPrefix)
	check(err)
	_, source, err := env.CollectorAddr(ctx)
	check(err)
	check(env.WriteInventory(ctx, st, source))
	bus, err := loadkit.ConnectBus(env.NATSURL)
	check(err)
	defer bus.NC.Close()
	kiosk, err := loadkit.NewKiosk(ctx, api)
	check(err)
	kctx, kstop := context.WithCancel(ctx)
	defer kstop()
	go kiosk.Run(kctx)
	if err := waitKiosk(ctx, kiosk, time.Now(), 30*time.Second); err != nil {
		log.Fatalf("el kiosco no conecta antes de empezar: %v", err)
	}
	if _, err := kiosk.ProbeRealtime(ctx, bus, st.TenantID, 15*time.Second); err != nil {
		log.Fatalf("sonda de tiempo real antes de empezar: %v", err)
	}

	c := &chaos{env: env, api: api, bus: bus, kiosk: kiosk, st: st, rate: rate, flowsim: flowsim}
	var results []result
	ok := true
	for _, s := range strings.Split(scen, ",") {
		var r result
		switch strings.TrimSpace(s) {
		case "clickhouse":
			r = c.restart(ctx, "clickhouse", "clickhouse", down)
		case "nats":
			r = c.restart(ctx, "nats", "nats", down)
		case "app":
			r = c.restart(ctx, "app", "horus-app", down)
		case "collector":
			r = c.collector(ctx, colDown)
		default:
			log.Fatalf("escenario desconocido %q", s)
		}
		results = append(results, r)
		ok = ok && r.Pass
		if ctx.Err() != nil {
			break
		}
	}
	check(report(env.Dir, rate, results))
	if !ok {
		log.Print("chaos-i1: KO")
		os.Exit(1)
	}
	log.Print("chaos-i1: OK")
}

type chaos struct {
	env     loadkit.Env
	api     *loadkit.API
	bus     *loadkit.Bus
	kiosk   *loadkit.Kiosk
	st      loadkit.State
	rate    float64
	flowsim string
	seed    int64
}

// watch muestrea lag, tamaño de TLM_FLOWS y búfer del collector hasta que stop se cierra.
type watch struct {
	mu                 sync.Mutex
	maxLag             uint64
	maxStream, maxBuff float64
	drops              float64
}

func (c *chaos) watch(ctx context.Context) (*watch, func()) {
	w := &watch{}
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			if lag, err := c.bus.IngesterLag(wctx); err == nil {
				w.mu.Lock()
				w.maxLag = max(w.maxLag, lag.Total())
				w.maxStream = math.Max(w.maxStream, float64(lag.StreamByte))
				w.mu.Unlock()
			}
			if m, err := loadkit.Scrape(wctx, c.env.CollectorMetrics); err == nil {
				w.mu.Lock()
				w.maxBuff = math.Max(w.maxBuff, m.Sum("horus_collector_buffer_bytes"))
				w.drops = math.Max(w.drops, m.Sum("horus_collector_dropped_total", `reason="bus_unavailable"`)+
					m.Sum("horus_collector_dropped_total", `reason="queue_full"`))
				w.mu.Unlock()
			}
			select {
			case <-wctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return w, func() { cancel(); <-done }
}

func (c *chaos) startSim(ctx context.Context, name string, d time.Duration) (*loadkit.Sim, error) {
	c.seed++
	target, _, err := c.env.CollectorAddr(ctx)
	if err != nil {
		return nil, err
	}
	f, err := os.Create(filepath.Join(c.env.Dir, "chaos-"+name+"-flowsim.log"))
	if err != nil {
		return nil, err
	}
	sim := &loadkit.Sim{Bin: c.flowsim, Target: target, Rate: c.rate, Duration: d, Seed: 100 + c.seed,
		Expected: filepath.Join(c.env.Dir, "chaos-"+name+".expected.json"), Log: f}
	return sim, sim.Start(ctx)
}

// drain espera a que el ingester vacíe el backlog y las filas dejen de crecer.
func (c *chaos) drain(ctx context.Context, rows0 uint64) (uint64, time.Duration) {
	start := time.Now()
	var last uint64
	stable := 0
	for time.Since(start) < 5*time.Minute && ctx.Err() == nil {
		lag, err := c.bus.IngesterLag(ctx)
		rows, rerr := c.env.FlowRows(ctx, c.st.TenantID)
		if err == nil && rerr == nil && lag.Total() == 0 {
			if rows == last {
				stable++
			} else {
				stable = 0
			}
			if stable >= 3 {
				return rows - rows0, time.Since(start)
			}
		}
		if rerr == nil {
			last = rows
		}
		time.Sleep(2 * time.Second)
	}
	return last - rows0, time.Since(start)
}

func (c *chaos) rows(ctx context.Context) uint64 {
	for range 30 {
		if n, err := c.env.FlowRows(ctx, c.st.TenantID); err == nil {
			return n
		}
		time.Sleep(2 * time.Second)
	}
	log.Fatal("ClickHouse no responde")
	return 0
}

// recoverKiosk mide cuánto tarda el kiosco en volver: WebSocket conectado
// después de `since`, sondas HTTP en 200 y un evento nuevo recibido.
func (c *chaos) recoverKiosk(ctx context.Context, r *result, since time.Time) {
	if err := waitKiosk(ctx, c.kiosk, since, 90*time.Second); err != nil {
		r.Why = append(r.Why, "kiosco: "+err.Error())
		return
	}
	lat, err := c.kiosk.ProbeRealtime(ctx, c.bus, c.st.TenantID, 30*time.Second)
	if err != nil {
		r.Why = append(r.Why, "kiosco: "+err.Error())
		return
	}
	r.EventLatency = loadkit.Fmt(lat)
	r.KioskBack = time.Since(since).Round(time.Second).String()
}

// waitKiosk espera WebSocket conectado (si el servicio lo cortó, reconectado
// después de since) y sondas HTTP en 200 después de since.
func waitKiosk(ctx context.Context, k *loadkit.Kiosk, since time.Time, d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		s := k.Status()
		if s.Connected && s.HTTPOKAt.After(since) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	s := k.Status()
	return fmt.Errorf("no se recupera en %s (ws conectado=%v, último error ws %q, última sonda HTTP %d)", d, s.Connected, s.LastErr, s.HTTPStatus)
}

// restart: caída de `service` durante `down` con ingesta continua.
func (c *chaos) restart(ctx context.Context, name, service string, down time.Duration) result {
	r := result{Scenario: name, Down: down.String()}
	log.Printf("== %s: caída de %s durante %s a %.0f flujos/s", name, service, down, c.rate)
	pre, post := 20*time.Second, 40*time.Second
	rows0 := c.rows(ctx)
	disc0 := c.kiosk.Status().Disconnects
	w, stopWatch := c.watch(ctx)
	sim, err := c.startSim(ctx, name, pre+down+post)
	if err != nil {
		stopWatch()
		r.Why = append(r.Why, "simulador: "+err.Error())
		return r
	}
	sleep(ctx, pre)
	if _, err := c.env.ComposeCmd(ctx, "stop", service); err != nil {
		r.Why = append(r.Why, err.Error())
	}
	sleep(ctx, down)
	if _, err := c.env.ComposeCmd(ctx, "start", service); err != nil {
		r.Why = append(r.Why, err.Error())
	}
	started := time.Now()
	// horus-app depende de los demás: tras reiniciar ClickHouse o NATS se comprueba todo.
	if err := c.env.WaitHealthy(ctx, 3*time.Minute, service, "horus-app", "horus-collector"); err != nil {
		r.Why = append(r.Why, err.Error())
	}
	healthy := time.Now()
	r.Recovery = healthy.Sub(started).Round(time.Second).String()
	c.recoverKiosk(ctx, &r, started)
	if err := <-sim.Done(); err != nil {
		r.Why = append(r.Why, "simulador: "+err.Error())
	}
	var drain time.Duration
	r.Rows, drain = c.drain(ctx, rows0)
	stopWatch()
	r.Drain = drain.Round(time.Second).String()
	r.Sent, _ = sim.Sent()
	r.Lost = int64(r.Sent) - int64(r.Rows) //nolint:gosec // recuentos
	r.LostSeconds = math.Round(float64(r.Lost)/c.rate*10) / 10
	r.MaxLagBatches, r.MaxStreamMB, r.MaxBufferMB, r.CollectorDrop = w.maxLag, w.maxStream/1e6, w.maxBuff/1e6, w.drops
	r.KioskDisc = c.kiosk.Status().Disconnects - disc0
	if r.Lost != 0 {
		r.Why = append(r.Why, fmt.Sprintf("pérdida: %d registros (%.1f s de flujos) dentro del búfer documentado", r.Lost, r.LostSeconds))
	}
	r.Pass = len(r.Why) == 0
	log.Printf("   enviados %d, ClickHouse %d, pérdida %d, lag máx %d lotes, TLM_FLOWS máx %.1f MB, búfer collector máx %.1f MB, vuelta %s, kiosco %s → %v %s",
		r.Sent, r.Rows, r.Lost, r.MaxLagBatches, r.MaxStreamMB, r.MaxBufferMB, r.Recovery, r.KioskBack, r.Pass, strings.Join(r.Why, "; "))
	return r
}

// collector: caída del collector `down`; el hueco debe verse como hueco y el
// exportador pasar por Silencioso.
func (c *chaos) collector(ctx context.Context, down time.Duration) result {
	r := result{Scenario: "collector", Down: down.String()}
	log.Printf("== collector: caída del collector durante %s a %.0f flujos/s", down, c.rate)
	var mu sync.Mutex
	sub, err := c.bus.NC.Subscribe("horus.flows.exporter.>", func(m *nats.Msg) {
		var env struct {
			Type string `json:"type"`
			Data struct {
				RouterID string `json:"router_id"`
				State    string `json:"state"`
			} `json:"data"`
		}
		if json.Unmarshal(m.Data, &env) != nil || env.Data.RouterID != c.st.RouterID {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch kind := strings.TrimPrefix(env.Type, "horus.flows.exporter."); kind {
		case "state_changed":
			r.States = append(r.States, env.Data.State)
		default: // silent | recovered
			r.States = append(r.States, "evento:"+kind)
		}
	})
	if err != nil {
		r.Why = append(r.Why, err.Error())
		return r
	}
	defer func() { _ = sub.Unsubscribe() }()
	pre, post := 40*time.Second, 60*time.Second
	rows0 := c.rows(ctx)
	ipBefore, _, _ := c.env.CollectorAddr(ctx)
	sim, err := c.startSim(ctx, "collector", pre+down+post)
	if err != nil {
		r.Why = append(r.Why, "simulador: "+err.Error())
		return r
	}
	sleep(ctx, pre)
	// La caída empieza cuando el contenedor ha parado (el apagado ordenado
	// sigue recibiendo y vaciando lotes unos segundos tras el SIGTERM).
	if _, err := c.env.ComposeCmd(ctx, "stop", "horus-collector"); err != nil {
		r.Why = append(r.Why, err.Error())
	}
	stopAt := time.Now().UTC()
	sleep(ctx, down)
	startAt := time.Now().UTC()
	if _, err := c.env.ComposeCmd(ctx, "start", "horus-collector"); err != nil {
		r.Why = append(r.Why, err.Error())
	}
	if err := c.env.WaitHealthy(ctx, 2*time.Minute, "horus-collector"); err != nil {
		r.Why = append(r.Why, err.Error())
	}
	r.Recovery = time.Since(startAt).Round(time.Second).String()
	if ipAfter, _, _ := c.env.CollectorAddr(ctx); ipAfter != ipBefore {
		r.Why = append(r.Why, "el collector cambió de IP al arrancar ("+ipBefore+" → "+ipAfter+"): el simulador no lo alcanza")
	}
	if err := <-sim.Done(); err != nil {
		r.Why = append(r.Why, "simulador: "+err.Error())
	}
	var drain time.Duration
	r.Rows, drain = c.drain(ctx, rows0)
	r.Drain = drain.Round(time.Second).String()
	r.Sent, _ = sim.Sent()
	r.Lost = int64(r.Sent) - int64(r.Rows) //nolint:gosec // recuentos
	r.LostSeconds = math.Round(float64(r.Lost)/c.rate*10) / 10
	// Lo perdido es lo enviado mientras no había collector (± el arranque).
	outage := startAt.Sub(stopAt).Seconds()
	r.Notes = append(r.Notes, fmt.Sprintf("caída real %.0f s; perdidos %.1f s de flujos", outage, r.LostSeconds))
	if r.LostSeconds > outage+20 {
		r.Why = append(r.Why, fmt.Sprintf("se perdió más (%.1f s) que la caída (%.0f s)", r.LostSeconds, outage))
	}

	// Hueco = ausencia de filas (por recepción, en tramos de 10 s del interior de la caída).
	q := fmt.Sprintf(`SELECT count() FROM flows.flows_raw WHERE tenant_id = toUUID('%s')
		AND received_at > toDateTime64('%s', 3, 'UTC') + INTERVAL 5 SECOND
		AND received_at < toDateTime64('%s', 3, 'UTC')`, c.st.TenantID, stopAt.Format("2006-01-02 15:04:05.000"),
		startAt.Format("2006-01-02 15:04:05.000"))
	if n, err := c.env.CHCount(ctx, q); err != nil {
		r.Why = append(r.Why, "hueco: "+err.Error())
	} else {
		r.GapBuckets = fmt.Sprintf("%d filas recibidas en el interior de la caída", n)
		if n != 0 {
			r.Why = append(r.Why, r.GapBuckets)
		}
	}
	// La serie de la API no rellena con ceros (I1-08): ningún punto vale 0.
	ts, err := c.api.TenantDo(ctx, loadkit.Call{Method: http.MethodGet, Path: "/api/v1/analytics/traffic/timeseries?range=1h&metrics=flows_per_second"})
	switch {
	case err != nil:
		r.Why = append(r.Why, "serie: "+err.Error())
	case ts.Status != http.StatusOK:
		r.Why = append(r.Why, fmt.Sprintf("serie: HTTP %d", ts.Status))
	default:
		zeros, nulls, vals := seriesCounts(ts.Body)
		r.Notes = append(r.Notes, fmt.Sprintf("serie 1 h (pasos de 5 min): %d puntos con valor, %d huecos (null), %d ceros", vals, nulls, zeros))
		if zeros > 0 {
			r.Why = append(r.Why, fmt.Sprintf("la serie de tráfico tiene %d puntos a cero", zeros))
		}
	}
	// El exportador pasó por Silencioso y volvió.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := strings.Join(r.States, ",")
		mu.Unlock()
		if strings.Contains(got, "evento:silent") && strings.Contains(got, "evento:recovered") {
			break
		}
		time.Sleep(time.Second)
	}
	mu.Lock()
	got := strings.Join(r.States, ",")
	mu.Unlock()
	if !strings.Contains(got, "evento:silent") || !strings.Contains(got, "evento:recovered") || !silentThenBack(r.States) {
		r.Why = append(r.Why, "el exportador no pasó por silent → recovered (eventos: "+got+")")
	}
	r.Pass = len(r.Why) == 0
	log.Printf("   enviados %d, ClickHouse %d, perdidos %d (%.1f s), %s, estados %v → %v %s", r.Sent, r.Rows, r.Lost, r.LostSeconds,
		r.GapBuckets, r.States, r.Pass, strings.Join(r.Why, "; "))
	return r
}

// silentThenBack: un state_changed a silent seguido de otro a un estado activo.
func silentThenBack(states []string) bool {
	seen := false
	for _, s := range states {
		switch {
		case s == "silent":
			seen = true
		case seen && (s == "exporting" || s == "lossy" || s == "clock_skew"):
			return true
		}
	}
	return false
}

func seriesCounts(body map[string]any) (zeros, nulls, vals int) {
	data, _ := body["data"].(map[string]any)
	series, _ := data["series"].([]any)
	for _, s := range series {
		m, _ := s.(map[string]any)
		pts, _ := m["points"].([]any)
		for _, p := range pts {
			pt, _ := p.([]any) // [instante, valor | null]
			if len(pt) != 2 {
				continue
			}
			switch x := pt[1].(type) {
			case nil:
				nulls++
			case float64:
				if x == 0 {
					zeros++
				} else {
					vals++
				}
			}
		}
	}
	return zeros, nulls, vals
}

func report(dir string, rate float64, rs []result) error {
	b, err := json.MarshalIndent(map[string]any{"at": time.Now().UTC(), "rate": rate, "results": rs}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "chaos.json"), b, 0o644); err != nil { //nolint:gosec // informe
		return err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Pruebas de fallo I1-26 — %s — %.0f flujos/s\n\n", time.Now().UTC().Format(time.RFC3339), rate)
	sb.WriteString("| Escenario | Caída | Vuelta a healthy | Enviados | ClickHouse | Perdidos (s) | Lag máx. (lotes) | TLM_FLOWS máx. | Búfer collector máx. | Drenaje | Kiosco de vuelta | Evento al kiosco | Resultado |\n")
	sb.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |\n")
	for _, r := range rs {
		res := "OK"
		if !r.Pass {
			res = "KO: " + strings.Join(r.Why, "; ")
		}
		if len(r.Notes) > 0 || len(r.States) > 0 {
			res += " (" + strings.Join(r.Notes, "; ")
			if len(r.States) > 0 {
				res += "; estados: " + strings.Join(r.States, " → ")
			}
			res += ")"
		}
		fmt.Fprintf(&sb, "| %s | %s | %s | %d | %d | %d (%.1f) | %d | %.1f MB | %.1f MB | %s | %s | %s | %s |\n", r.Scenario, r.Down, r.Recovery,
			r.Sent, r.Rows, r.Lost, r.LostSeconds, r.MaxLagBatches, r.MaxStreamMB, r.MaxBufferMB, r.Drain, dash(r.KioskBack), dash(r.EventLatency), res)
	}
	md := sb.String()
	fmt.Print("\n" + md)
	return os.WriteFile(filepath.Join(dir, "chaos.md"), []byte(md), 0o644) //nolint:gosec // informe
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func sleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func durEnv(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		d, err := time.ParseDuration(v)
		check(err)
		return d
	}
	return def
}

func floatEnv(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		var f float64
		_, err := fmt.Sscan(v, &f)
		check(err)
		return f
	}
	return def
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
