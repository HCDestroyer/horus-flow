package main

// Matriz de reinicio brusco de la cadena de flujos (D23, `make chaos-restart-flows`).
//
// Con el simulador enviando a tasa constante (CHAOS_RATE, escenario de
// SCENARIO) se reinicia CHAOS_REPEAT veces, en momentos aleatorios
// (CHAOS_SEED), cada componente de la cadena con cada método:
//
//   - kill9: SIGKILL al proceso principal del contenedor desde el host; lo
//     arranca la política de reinicio de Docker (unless-stopped), como tras
//     un fallo real;
//   - restart: `docker restart` (SIGTERM, apagado ordenado).
//
// Componentes: horus-collector, horus-app (ingester, inventario, descubrimiento),
// nats y clickhouse. Cada combinación es un escenario con su propio simulador y
// su verificación, segundo a segundo de fin de flujo (flowsim -sendlog):
//
//   - pérdida 0, salvo lo enviado por UDP mientras el collector estaba caído:
//     se mide (registros enviados en la ventana caída → arranque) y se
//     informa aparte; cualquier registro que falte fuera de esa ventana es un
//     fallo;
//   - 0 duplicados en flows_raw (misma clave de flujo; misma clave y batch_id);
//   - agregados por cliente y por nodo (customer_5m, customer_1h, customer_1d,
//     site_5m) iguales a los recalculados desde flows_raw;
//   - inventario intacto: el exportador sigue registrado en collector e
//     ingester (sin descartes unknown_exporter, la API lo devuelve), los
//     prefijos siguen atribuyendo (proporción de filas atribuidas antes y
//     después) y los clientes conocidos no se reenvían en first_seen; los
//     clientes de devices no disminuyen;
//   - el exportador acaba en exporting.
//
// Además se mide lo recibido por el collector (horus_collector_received_records_total,
// sumando entre reinicios), los datagramas perdidos en su socket, lo que el
// propio collector mide como enviado con él caído
// (horus_collector_downtime_lost_records_total) y lo reenviado desde el spool.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/hcdestroyer/horus-flow/tests/loadkit"
)

type restartEvent struct {
	At        time.Time `json:"at"`
	Up        time.Time `json:"up"`        // proceso de vuelta (collector: escuchando UDP)
	Healthy   time.Time `json:"healthy"`   // toda la pila healthy
	Restarted string    `json:"restarted"` // policy | manual
	Down      string    `json:"down"`
}

type restartResult struct {
	Scenario string         `json:"scenario"`
	Target   string         `json:"target"`
	Method   string         `json:"method"`
	Events   []restartEvent `json:"events"`
	Rate     float64        `json:"rate"`
	Sent     uint64         `json:"sent_records"`
	Rows     uint64         `json:"clickhouse_rows"`
	// Missing y Extra salen de comparar, segundo a segundo de fin de flujo, lo
	// enviado con lo que hay en flows_raw.
	Missing     int64 `json:"missing_records"`
	Extra       int64 `json:"extra_records"`
	Unexplained int64 `json:"missing_outside_collector_down"`
	// SentWhileDown: registros enviados mientras el collector estaba caído
	// (del kill/parada a escuchar de nuevo, más 2 s de lo que tenía en memoria).
	SentWhileDown     uint64            `json:"sent_while_collector_down"`
	CollectorReceived uint64            `json:"collector_received_records"`
	CollectorDowntime uint64            `json:"collector_measured_downtime_records"`
	UDPDrops          uint64            `json:"udp_rcvbuf_errors"`
	SpoolReplayed     uint64            `json:"spool_replayed_batches"`
	QueueDrops        uint64            `json:"collector_queue_full_drops"`
	DupKey            uint64            `json:"duplicate_rows_by_flow_key"`
	DupBatch          uint64            `json:"duplicate_rows_by_flow_key_and_batch"`
	AggMismatch       map[string]uint64 `json:"aggregate_mismatches"`
	Attributed        [2]float64        `json:"attributed_ratio_before_after"`
	UnknownExporter   uint64            `json:"unknown_exporter_drops"`
	FirstSeenAgain    int               `json:"first_seen_reemitted_known"`
	Customers         [2]float64        `json:"customers_before_after"`
	Prefixes          [2]int            `json:"prefixes_before_after"`
	ExporterState     string            `json:"exporter_state"`
	Drain             string            `json:"drain"`
	MaxLag            uint64            `json:"max_lag_batches"`
	Pass              bool              `json:"pass"`
	Why               []string          `json:"why,omitempty"`
	Notes             []string          `json:"notes,omitempty"`
}

type sendLog struct {
	Ticks []struct {
		Tick    int64  `json:"tick_unix_ms"`
		First   int64  `json:"first_sent_unix_ms"`
		Last    int64  `json:"last_sent_unix_ms"`
		Records uint64 `json:"records"`
	} `json:"ticks"`
	ByEnd map[string]uint64 `json:"records_by_end_second"`
}

// collectorTally suma contadores del collector entre reinicios (un contador
// que baja es un proceso nuevo).
type collectorTally struct {
	mu   sync.Mutex
	last map[string]float64
	sum  map[string]float64
}

var tallied = []struct{ key, name, filter string }{
	{"received", "horus_collector_received_records_total", ""},
	{"downtime", "horus_collector_downtime_lost_records_total", ""},
	{"replayed", "horus_collector_spool_replayed_total", ""},
	{"unknown", "horus_collector_dropped_total", `reason="unknown_exporter"`},
	{"queue", "horus_collector_dropped_total", `reason="queue_full"`},
}

func (t *collectorTally) observe(m loadkit.Metrics) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, x := range tallied {
		var v float64
		if x.filter == "" {
			v = m.Sum(x.name)
		} else {
			v = m.Sum(x.name, x.filter)
		}
		prev := t.last[x.key]
		if v >= prev {
			t.sum[x.key] += v - prev
		} else {
			t.sum[x.key] += v // reinicio: el contador empieza de cero
		}
		t.last[x.key] = v
	}
}

func (t *collectorTally) get(k string) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return uint64(math.Round(t.sum[k]))
}

func restartMatrix(ctx context.Context, c *chaos) ([]restartResult, bool) {
	targets := strings.Split(envOr("CHAOS_TARGETS", "horus-collector,horus-app,nats,clickhouse"), ",")
	methods := strings.Split(envOr("CHAOS_METHODS", "kill9,restart"), ",")
	repeat := int(floatEnv("CHAOS_REPEAT", 3))
	seed := uint64(floatEnv("CHAOS_SEED", float64(time.Now().Unix()%100000)))
	rng := rand.New(rand.NewPCG(seed, 23)) //nolint:gosec // momentos aleatorios de la prueba
	log.Printf("chaos-restart-flows: %v × %v × %d a %.0f flujos/s, semilla %d", targets, methods, repeat, c.rate, seed)

	// first_seen y customer.discovered de toda la prueba.
	fs := &firstSeenWatch{discovered: map[string]time.Time{}, emitted: map[string][]time.Time{}}
	subs := fs.subscribe(c.bus.NC)
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()
	var out []restartResult
	ok := true
	for _, target := range targets {
		for _, method := range methods {
			r := c.restartScenario(ctx, strings.TrimSpace(target), strings.TrimSpace(method), repeat, rng, fs)
			out = append(out, r)
			ok = ok && r.Pass
			if ctx.Err() != nil {
				return out, false
			}
		}
	}
	return out, ok
}

type firstSeenWatch struct {
	mu         sync.Mutex
	discovered map[string]time.Time
	emitted    map[string][]time.Time
}

func (f *firstSeenWatch) subscribe(nc *nats.Conn) []*nats.Subscription {
	var subs []*nats.Subscription
	s1, err := nc.Subscribe("horus.flows.client.first_seen.>", func(m *nats.Msg) {
		var env struct {
			Data struct {
				Clients []struct {
					Address string `json:"address"`
				} `json:"clients"`
			} `json:"data"`
		}
		if json.Unmarshal(m.Data, &env) != nil {
			return
		}
		now := time.Now()
		f.mu.Lock()
		for _, cl := range env.Data.Clients {
			f.emitted[cl.Address] = append(f.emitted[cl.Address], now)
		}
		f.mu.Unlock()
	})
	if err == nil {
		subs = append(subs, s1)
	}
	s2, err := nc.Subscribe("horus.devices.customer.discovered.>", func(m *nats.Msg) {
		var env struct {
			Data struct {
				Address string `json:"address"`
			} `json:"data"`
		}
		if json.Unmarshal(m.Data, &env) != nil {
			return
		}
		a := strings.Split(env.Data.Address, "/")[0]
		f.mu.Lock()
		if _, ok := f.discovered[a]; !ok {
			f.discovered[a] = time.Now()
		}
		f.mu.Unlock()
	})
	if err == nil {
		subs = append(subs, s2)
	}
	return subs
}

// reemitted cuenta first_seen emitidos en [from, to] de clientes que devices
// ya había dado de alta (customer.discovered) al menos 10 s antes.
func (f *firstSeenWatch) reemitted(from, to time.Time) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for addr, ts := range f.emitted {
		key := strings.Split(addr, "/")[0]
		d, ok := f.discovered[key]
		if !ok {
			continue
		}
		for _, t := range ts {
			if t.After(from) && t.Before(to) && t.Sub(d) > 10*time.Second {
				n++
			}
		}
	}
	return n
}

func (c *chaos) restartScenario(ctx context.Context, target, method string, repeat int, rng *rand.Rand, fs *firstSeenWatch) restartResult {
	r := restartResult{Scenario: target + "/" + method, Target: target, Method: method, Rate: c.rate, AggMismatch: map[string]uint64{}}
	log.Printf("== %s: %d veces a %.0f flujos/s", r.Scenario, repeat, c.rate)
	c.limit(ctx)
	pre, post := 25*time.Second, 40*time.Second
	gapMin, gapMax := 15*time.Second, 40*time.Second
	simDur := pre + time.Duration(repeat)*(gapMax+50*time.Second) + post
	r.Customers[0] = c.customers(ctx)
	r.Prefixes[0] = c.prefixes(ctx)
	tally := &collectorTally{last: map[string]float64{}, sum: map[string]float64{}}
	if m, err := loadkit.Scrape(ctx, c.env.CollectorMetrics); err == nil {
		tally.observe(m) // base: lo anterior no cuenta
		tally.mu.Lock()
		tally.sum = map[string]float64{}
		tally.mu.Unlock()
	}
	_, udp0 := c.collectorCounts(ctx)
	wctx, stopWatch := context.WithCancel(ctx)
	var lagMu sync.Mutex
	var udpAcc uint64
	lastUDP := udp0
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			if m, err := loadkit.Scrape(wctx, c.env.CollectorMetrics); err == nil {
				tally.observe(m)
			}
			if u, err := c.env.UDPDrops(wctx); err == nil {
				lagMu.Lock()
				if u >= lastUDP {
					udpAcc += u - lastUDP
				} else {
					udpAcc += u
				}
				lastUDP = u
				lagMu.Unlock()
			}
			if lag, err := c.bus.IngesterLag(wctx); err == nil {
				lagMu.Lock()
				r.MaxLag = max(r.MaxLag, lag.Total())
				lagMu.Unlock()
			}
			select {
			case <-wctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	sendLogPath := filepath.Join(c.env.Dir, "restart-"+target+"-"+method+".sendlog.json")
	sim, err := c.startSimArgs(ctx, "restart-"+target+"-"+method, simDur, "-sendlog", sendLogPath)
	if err != nil {
		stopWatch()
		r.Why = append(r.Why, "simulador: "+err.Error())
		return r
	}
	simStart := time.Now()
	sleep(ctx, pre)
	for i := range repeat {
		sleep(ctx, gapMin+time.Duration(rng.Int64N(int64(gapMax-gapMin))))
		ev, err := c.disrupt(ctx, target, method)
		if err != nil {
			r.Why = append(r.Why, fmt.Sprintf("evento %d: %v", i+1, err))
		}
		r.Events = append(r.Events, ev)
		log.Printf("   %s #%d: caída %s, de vuelta (proceso) %s, pila healthy %s, reinicio por %s", r.Scenario, i+1,
			ev.At.Format("15:04:05.000"), ev.Down, ev.Healthy.Sub(ev.At).Round(100*time.Millisecond), ev.Restarted)
		if ctx.Err() != nil {
			break
		}
	}
	lastUp := time.Now()
	if err := <-sim.Done(); err != nil {
		r.Why = append(r.Why, "simulador: "+err.Error())
	}
	simEnd := time.Now()
	start := time.Now()
	c.drainAll(ctx)
	r.Drain = time.Since(start).Round(time.Second).String()
	stopWatch()
	<-watchDone
	r.CollectorReceived, r.CollectorDowntime = tally.get("received"), tally.get("downtime")
	r.SpoolReplayed, r.UnknownExporter, r.QueueDrops = tally.get("replayed"), tally.get("unknown"), tally.get("queue")
	lagMu.Lock()
	r.UDPDrops = udpAcc
	lagMu.Unlock()
	r.Sent, _ = sim.Sent()
	c.verifyRestart(ctx, &r, sim, sendLogPath, simStart, simEnd, lastUp, fs)
	r.Pass = len(r.Why) == 0
	log.Printf("   %s: enviados %d, recibidos por el collector %d, ClickHouse %d, faltan %d (enviados con el collector caído %d, fuera de esa ventana %d), sobran %d, duplicados %d/%d, agregados %v, atribuidas %.3f→%.3f, first_seen repetidos %d, exportador %s → %v %s",
		r.Scenario, r.Sent, r.CollectorReceived, r.Rows, r.Missing, r.SentWhileDown, r.Unexplained, r.Extra, r.DupKey, r.DupBatch,
		r.AggMismatch, r.Attributed[0], r.Attributed[1], r.FirstSeenAgain, r.ExporterState, r.Pass, strings.Join(r.Why, "; "))
	return r
}

func (c *chaos) startSimArgs(ctx context.Context, name string, d time.Duration, extra ...string) (*loadkit.Sim, error) {
	c.seed++
	target, _, err := c.env.CollectorAddr(ctx)
	if err != nil {
		return nil, err
	}
	f, err := os.Create(filepath.Join(c.env.Dir, "chaos-"+name+"-flowsim.log"))
	if err != nil {
		return nil, err
	}
	sc := c.scen
	sc.SimArgs = append(append([]string(nil), sc.SimArgs...), extra...)
	sim := &loadkit.Sim{Bin: c.flowsim, Target: target, Rate: c.rate, Duration: d, Seed: 300 + c.seed, Scenario: sc,
		Expected: filepath.Join(c.env.Dir, "chaos-"+name+".expected.json"), Log: f}
	return sim, sim.Start(ctx)
}

// disrupt aplica el método al servicio y espera a que todo vuelva.
func (c *chaos) disrupt(ctx context.Context, service, method string) (restartEvent, error) {
	ctr := c.env.Container(service)
	ev := restartEvent{}
	started0, _ := loadkit.Docker(ctx, "inspect", "-f", "{{.State.StartedAt}}", ctr)
	switch method {
	case "kill9":
		pidS, err := loadkit.Docker(ctx, "inspect", "-f", "{{.State.Pid}}", ctr)
		if err != nil {
			return ev, err
		}
		pid, err := strconv.Atoi(strings.TrimSpace(pidS))
		if err != nil || pid <= 0 {
			return ev, fmt.Errorf("pid de %s: %q", ctr, pidS)
		}
		ev.At = time.Now()
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			return ev, fmt.Errorf("kill -9 %d: %w", pid, err)
		}
		ev.Restarted = "policy"
		deadline := time.Now().Add(30 * time.Second)
		for {
			s, _ := loadkit.Docker(ctx, "inspect", "-f", "{{.State.StartedAt}}", ctr)
			if s != "" && s != started0 {
				break
			}
			if time.Now().After(deadline) {
				ev.Restarted = "manual"
				if _, err := loadkit.Docker(ctx, "start", ctr); err != nil {
					return ev, err
				}
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	case "restart":
		ev.At = time.Now()
		ev.Restarted = "docker restart"
		if _, err := loadkit.Docker(ctx, "restart", "-t", "10", ctr); err != nil {
			return ev, err
		}
	default:
		return ev, fmt.Errorf("método desconocido %q", method)
	}
	err := c.env.WaitHealthy(ctx, 4*time.Minute, service, "horus-app", "horus-collector")
	ev.Healthy = time.Now()
	ev.Up = ev.Healthy
	if service == "horus-collector" {
		if up, ok := c.collectorListening(ctx, ev.At); ok {
			ev.Up = up
		}
	}
	ev.Down = ev.Up.Sub(ev.At).Round(100 * time.Millisecond).String()
	c.limit(ctx)
	return ev, err
}

// collectorListening devuelve cuándo el collector volvió a escuchar UDP
// (línea "collector listening" del log) después de since.
func (c *chaos) collectorListening(ctx context.Context, since time.Time) (time.Time, bool) {
	out, err := loadkit.Docker(ctx, "logs", "-t", "--since", since.Add(-time.Second).UTC().Format(time.RFC3339Nano), c.env.Container("horus-collector"))
	if err != nil {
		return time.Time{}, false
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "collector listening") {
			continue
		}
		ts, _, _ := strings.Cut(line, " ")
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil && t.After(since) {
			return t, true
		}
	}
	return time.Time{}, false
}

// drainAll espera a que el collector vacíe su spool y el ingester el backlog.
func (c *chaos) drainAll(ctx context.Context) {
	deadline := time.Now().Add(6 * time.Minute)
	var last uint64
	stable := 0
	for time.Now().Before(deadline) && ctx.Err() == nil {
		spoolOK := true
		if m, err := loadkit.Scrape(ctx, c.env.CollectorMetrics); err == nil {
			spoolOK = m.Sum("horus_collector_spool_batches") == 0 && m.Sum("horus_collector_spooling") == 0
		}
		lag, err := c.bus.IngesterLag(ctx)
		rows, rerr := c.env.FlowRows(ctx, c.st.TenantID)
		if err == nil && rerr == nil && lag.Total() == 0 && spoolOK {
			if rows == last {
				stable++
			} else {
				stable = 0
			}
			if stable >= 3 {
				return
			}
		}
		if rerr == nil {
			last = rows
		}
		time.Sleep(2 * time.Second)
	}
}

func (c *chaos) customers(ctx context.Context) float64 {
	r, err := c.api.TenantDo(ctx, loadkit.Call{Method: http.MethodGet, Path: "/api/v1/customers/stats"})
	if err != nil || r.Status != http.StatusOK {
		return -1
	}
	if v, ok := r.Body["total"].(float64); ok {
		return v
	}
	return -1
}

func (c *chaos) prefixes(ctx context.Context) int {
	r, err := c.api.TenantDo(ctx, loadkit.Call{Method: http.MethodGet, Path: "/api/v1/sites/" + c.st.SiteID + "/client-prefixes"})
	if err != nil || r.Status != http.StatusOK {
		return -1
	}
	data, _ := r.Body["data"].([]any)
	return len(data)
}

func chTime(t time.Time) string {
	return "toDateTime64('" + t.UTC().Format("2006-01-02 15:04:05.000") + "', 3, 'UTC')"
}

func (c *chaos) verifyRestart(ctx context.Context, r *restartResult, sim *loadkit.Sim, sendLogPath string, simStart, simEnd, lastUp time.Time, fs *firstSeenWatch) {
	tenant := "toUUID('" + c.st.TenantID + "')"
	var sl sendLog
	b, err := os.ReadFile(sendLogPath)
	if err == nil {
		err = json.Unmarshal(b, &sl)
	}
	if err != nil {
		r.Why = append(r.Why, "sendlog: "+fmt.Sprint(err))
		return
	}
	var exp struct {
		Start time.Time `json:"start"`
	}
	if eb, err := os.ReadFile(sim.Expected); err == nil {
		_ = json.Unmarshal(eb, &exp)
	}
	from, to := exp.Start, simEnd.Add(2*time.Second)
	if from.IsZero() {
		from = simStart.Add(-2 * time.Second).Truncate(time.Second)
	}
	rng := fmt.Sprintf("tenant_id = %s AND ts >= %s AND ts < %s", tenant, chTime(from), chTime(to))

	// Segundo a segundo de fin de flujo.
	out, err := c.env.CHQuery(ctx, "SELECT toUnixTimestamp(ts) AS s, count() FROM flows.flows_raw WHERE "+rng+" GROUP BY s ORDER BY s")
	if err != nil {
		r.Why = append(r.Why, "flows_raw por segundo: "+err.Error())
		return
	}
	got := map[int64]uint64{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		s, _ := strconv.ParseInt(f[0], 10, 64)
		n, _ := strconv.ParseUint(f[1], 10, 64)
		got[s] = n
		r.Rows += n
	}
	// Ventanas en las que faltar registros se explica por el collector caído:
	// fin de flujo desde 75 s antes de la caída (timeout activo 60 s + inactivo
	// 15 s) hasta que vuelve a escuchar.
	type win struct{ a, b time.Time }
	var down, explain []win
	if r.Target == "horus-collector" {
		for _, ev := range r.Events {
			down = append(down, win{ev.At.Add(-2 * time.Second), ev.Up.Add(500 * time.Millisecond)})
			explain = append(explain, win{ev.At.Add(-75 * time.Second), ev.Up.Add(2 * time.Second)})
		}
	}
	secs := map[int64]bool{}
	for k := range sl.ByEnd {
		s, _ := strconv.ParseInt(k, 10, 64)
		secs[s] = true
	}
	for s := range got {
		secs[s] = true
	}
	keys := make([]int64, 0, len(secs))
	for s := range secs {
		keys = append(keys, s)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var badSecs []string
	for _, s := range keys {
		sent := sl.ByEnd[strconv.FormatInt(s, 10)]
		n := got[s]
		switch {
		case n > sent:
			r.Extra += int64(n - sent) //nolint:gosec // recuentos
			badSecs = append(badSecs, fmt.Sprintf("%s:+%d", time.Unix(s, 0).UTC().Format("15:04:05"), n-sent))
		case n < sent:
			miss := int64(sent - n) //nolint:gosec // recuentos
			r.Missing += miss
			t := time.Unix(s, 0)
			ok := false
			for _, w := range explain {
				if !t.Before(w.a.Truncate(time.Second)) && !t.After(w.b) {
					ok = true
					break
				}
			}
			if !ok {
				r.Unexplained += miss
				badSecs = append(badSecs, fmt.Sprintf("%s:-%d", t.UTC().Format("15:04:05"), miss))
			}
		}
	}
	for _, w := range down {
		a, bb := w.a.UnixMilli(), w.b.UnixMilli()
		for _, t := range sl.Ticks {
			first, last := t.First, t.Last
			if last < a || first > bb || first == 0 {
				continue
			}
			if last == first {
				r.SentWhileDown += t.Records
				continue
			}
			ov := float64(min(last, bb)-max(first, a)) / float64(last-first)
			r.SentWhileDown += uint64(math.Round(float64(t.Records) * math.Max(0, math.Min(1, ov))))
		}
	}
	if r.Extra > 0 {
		r.Why = append(r.Why, fmt.Sprintf("%d registros de más en flows_raw", r.Extra))
	}
	if r.Unexplained > 0 {
		r.Why = append(r.Why, fmt.Sprintf("faltan %d registros fuera de la caída del collector", r.Unexplained))
	}
	if r.Target == "horus-collector" && uint64(r.Missing) > r.SentWhileDown { //nolint:gosec // >= 0
		r.Why = append(r.Why, fmt.Sprintf("faltan %d registros, más que los %d enviados con el collector caído", r.Missing, r.SentWhileDown))
	}
	if len(badSecs) > 0 {
		r.Notes = append(r.Notes, "segundos con diferencias: "+strings.Join(badSecs[:min(12, len(badSecs))], " "))
	}
	if r.Target == "horus-collector" && r.Missing > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf("perdido por UDP con el collector caído: %d registros (%.1f s de flujos); el collector midió %d con su secuencia",
			r.Missing, float64(r.Missing)/c.rate, r.CollectorDowntime))
	}

	// Duplicados por clave de flujo y por clave + batch_id.
	key := "router_id, client_ip, client_port, remote_ip, remote_port, protocol, flow_start, ts, bytes, packets, direction, tcp_flags"
	if s, err := c.env.CHQuery(ctx, "SELECT count() - uniqExact(cityHash64("+key+")), count() - uniqExact(cityHash64(batch_id, "+key+")) FROM flows.flows_raw WHERE "+rng); err == nil {
		f := strings.Fields(s)
		if len(f) == 2 {
			r.DupKey, _ = strconv.ParseUint(f[0], 10, 64)
			r.DupBatch, _ = strconv.ParseUint(f[1], 10, 64)
		}
	} else {
		r.Why = append(r.Why, "duplicados: "+err.Error())
	}
	if r.DupKey > 0 || r.DupBatch > 0 {
		r.Why = append(r.Why, fmt.Sprintf("duplicados en flows_raw: %d por clave de flujo, %d por clave y batch_id", r.DupKey, r.DupBatch))
	}

	// Agregados recalculados desde flows_raw (cubos enteros).
	b5a, b5b := from.Truncate(5*time.Minute), to.Truncate(5*time.Minute).Add(5*time.Minute)
	h1a, h1b := from.Truncate(time.Hour), to.Truncate(time.Hour).Add(time.Hour)
	d1a := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	d1b := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	attr := "attribution_status IN ('attributed', 'internal')"
	checks := []struct{ name, q string }{
		{"customer_5m", aggQuery("flows.customer_5m", "tenant_id, site_id, realm_id, client_ip, bucket, service_id, direction",
			"toStartOfFiveMinutes(ts)", attr, tenant, b5a, b5b, "flows")},
		{"customer_1h", aggQuery("flows.customer_1h", "tenant_id, site_id, realm_id, client_ip, bucket, service_id, remote_asn, direction",
			"toStartOfHour(ts)", attr, tenant, h1a, h1b, "flows")},
		{"customer_1d", aggQuery("flows.customer_1d", "tenant_id, realm_id, client_ip, bucket, service_id, remote_asn, direction",
			"toDate(ts)", attr, tenant, d1a, d1b, "flows")},
		{"site_5m", aggQuery("flows.site_5m", "tenant_id, site_id, bucket, service_id, remote_asn, direction",
			"toStartOfFiveMinutes(ts)", "1", tenant, b5a, b5b, "flows")},
	}
	for _, ch := range checks {
		n, err := c.env.CHCount(ctx, ch.q)
		if err != nil {
			r.Why = append(r.Why, ch.name+": "+err.Error())
			continue
		}
		r.AggMismatch[ch.name] = n
		if n > 0 {
			r.Why = append(r.Why, fmt.Sprintf("%s: %d grupos distintos de lo recalculado desde flows_raw", ch.name, n))
		}
	}

	// Inventario: atribución antes de la primera caída y después de la última.
	ratio := func(a, b time.Time) float64 {
		s, err := c.env.CHQuery(ctx, fmt.Sprintf("SELECT countIf(%s) / count() FROM flows.flows_raw WHERE tenant_id = %s AND received_at >= %s AND received_at < %s",
			attr, tenant, chTime(a), chTime(b)))
		if err != nil {
			return -1
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return -1
		}
		return v
	}
	if len(r.Events) > 0 {
		r.Attributed[0] = ratio(simStart.Add(5*time.Second), r.Events[0].At.Add(-time.Second))
		r.Attributed[1] = ratio(lastUp.Add(15*time.Second), simEnd)
		if r.Attributed[0] < 0 || r.Attributed[1] < 0 || r.Attributed[1] < r.Attributed[0]-0.01 {
			r.Why = append(r.Why, fmt.Sprintf("atribución %.3f → %.3f tras los reinicios: el inventario (prefijos) no se recuperó", r.Attributed[0], r.Attributed[1]))
		}
	}
	if r.UnknownExporter > 0 {
		r.Why = append(r.Why, fmt.Sprintf("%d datagramas descartados como exportador desconocido: el collector perdió el router", r.UnknownExporter))
	}
	if len(r.Events) > 0 {
		r.FirstSeenAgain = fs.reemitted(r.Events[0].At, time.Now())
		if r.FirstSeenAgain > 0 {
			r.Why = append(r.Why, fmt.Sprintf("%d first_seen de clientes ya conocidos tras los reinicios", r.FirstSeenAgain))
		}
	}
	r.Customers[1] = c.customers(ctx)
	r.Prefixes[1] = c.prefixes(ctx)
	if r.Customers[1] < r.Customers[0] || r.Prefixes[1] != r.Prefixes[0] {
		r.Why = append(r.Why, fmt.Sprintf("clientes %v → %v, prefijos %d → %d", r.Customers[0], r.Customers[1], r.Prefixes[0], r.Prefixes[1]))
	}
	// Exportador: registrado en el ingester (API) y exportando.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		res, err := c.api.TenantDo(ctx, loadkit.Call{Method: http.MethodGet, Path: "/api/v1/flow-exporters/" + c.st.RouterID})
		if err == nil && res.Status == http.StatusOK {
			r.ExporterState = res.Str("state")
			if r.ExporterState == "exporting" {
				break
			}
		} else if err == nil {
			r.ExporterState = fmt.Sprintf("HTTP %d", res.Status)
		}
		time.Sleep(2 * time.Second)
	}
	if r.ExporterState != "exporting" {
		r.Why = append(r.Why, "estado del exportador: "+r.ExporterState)
	}
}

// aggQuery cuenta los grupos del agregado que no coinciden (bytes, paquetes,
// flujos) con los recalculados desde flows_raw en los cubos [a, b).
func aggQuery(table, keys, bucketExpr, where, tenant string, a, b time.Time, flowsCol string) string {
	const bucketCol = "bucket"
	ta, tb := chTime(a), chTime(b)
	return fmt.Sprintf(`SELECT count() FROM (
  SELECT %[1]s, sum(bytes) AS b, sum(packets) AS p, sum(%[8]s) AS f FROM %[2]s
  WHERE tenant_id = %[5]s AND %[9]s >= %[6]s AND %[9]s < %[7]s GROUP BY %[1]s
) AS agg FULL OUTER JOIN (
  SELECT %[10]s, sum(bytes) AS b, sum(packets) AS p, sum(toUInt64(merged_flows)) AS f FROM flows.flows_raw
  WHERE tenant_id = %[5]s AND (%[4]s) AND %[3]s >= %[6]s AND %[3]s < %[7]s GROUP BY %[1]s
) AS raw USING (%[1]s)
WHERE agg.b != raw.b OR agg.p != raw.p OR agg.f != raw.f
SETTINGS join_use_nulls = 0`, keys, table, bucketExpr, where, tenant, ta, tb, flowsCol, bucketCol,
		strings.Replace(keys, "bucket", bucketExpr+" AS bucket", 1))
}

func restartReport(dir string, rate float64, rs []restartResult) error {
	b, err := json.MarshalIndent(map[string]any{"at": time.Now().UTC(), "rate": rate, "results": rs}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "chaos-restart.json"), b, 0o644); err != nil { //nolint:gosec // informe
		return err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Matriz de reinicio de la cadena de flujos — %s — %.0f flujos/s\n\n", time.Now().UTC().Format(time.RFC3339), rate)
	sb.WriteString("| Escenario | Reinicios (caída → escuchando / healthy) | Enviados | Recibidos collector | flows_raw | Faltan | Enviados con collector caído | Faltan fuera de la caída | Sobran | Duplicados (clave / clave+lote) | Agregados distintos | Atribuidas antes → después | first_seen repetidos | Clientes | Exportador | Spool reenviados | Drenaje | Resultado |\n")
	sb.WriteString("| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | --- | ---: | --- | --- | ---: | ---: | --- |\n")
	for _, r := range rs {
		var evs []string
		for _, e := range r.Events {
			evs = append(evs, fmt.Sprintf("%s/%s", e.Down, e.Healthy.Sub(e.At).Round(time.Second)))
		}
		var agg []string
		for _, k := range []string{"customer_5m", "customer_1h", "customer_1d", "site_5m"} {
			agg = append(agg, fmt.Sprintf("%s %d", k, r.AggMismatch[k]))
		}
		res := "OK"
		if !r.Pass {
			res = "KO: " + strings.Join(r.Why, "; ")
		}
		if len(r.Notes) > 0 {
			res += " (" + strings.Join(r.Notes, "; ") + ")"
		}
		fmt.Fprintf(&sb, "| %s | %s | %d | %d | %d | %d | %d | %d | %d | %d / %d | %s | %.3f → %.3f | %d | %.0f → %.0f | %s | %d | %s | %s |\n",
			r.Scenario, strings.Join(evs, ", "), r.Sent, r.CollectorReceived, r.Rows, r.Missing, r.SentWhileDown, r.Unexplained, r.Extra,
			r.DupKey, r.DupBatch, strings.Join(agg, ", "), r.Attributed[0], r.Attributed[1], r.FirstSeenAgain, r.Customers[0], r.Customers[1],
			r.ExporterState, r.SpoolReplayed, r.Drain, res)
	}
	md := sb.String()
	fmt.Print("\n" + md)
	return os.WriteFile(filepath.Join(dir, "chaos-restart.md"), []byte(md), 0o644) //nolint:gosec // informe
}
