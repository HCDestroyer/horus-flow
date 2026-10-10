// Command load es la prueba de carga de I1 (historia I1-26, `make load-i1`).
//
// Contra el compose de tests/load/stack.sh: da de alta un ISP de prueba,
// registra el exportador del simulador y, por cada tasa, envía flujos IPFIX
// del escenario `normal` en tiempo real mientras consulta la API de tráfico.
// Mide:
//
//   - pérdida en el collector: registros enviados (expected.json del
//     simulador) frente a horus_collector_records_total, descartes por razón,
//     registros perdidos por secuencia y RcvbufErrors UDP del contenedor;
//   - lag del ingester: mensajes pendientes del durable flows-ingester en
//     TLM_FLOWS (en lotes y en segundos de flujo) y tiempo de drenaje;
//   - extremo a extremo: filas nuevas en flows.flows_raw;
//   - p95 de la API de tráfico (timeseries/top/attribution) < 500 ms.
//
// Con -ramp sube la tasa hasta el primer escalón que no cumple y publica el
// máximo sostenible. Escribe bin/load/results.{json,md}.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hcdestroyer/horus-flow/tests/loadkit"
)

// Criterios de I1-26 (criterio 1).
const (
	p95Budget = 500 * time.Millisecond
	// maxLagSeconds: backlog máximo tolerado durante la carga, en segundos de
	// flujo. Por encima, el ingester no da abasto y el lag crecería sin límite.
	maxLagSeconds = 30.0
	// maxDrain: tras parar el simulador el backlog debe vaciarse en este plazo.
	maxDrain = 90 * time.Second
)

type step struct {
	Rate          float64        `json:"rate"`
	Duration      string         `json:"duration"`
	Sent          uint64         `json:"sent_records"`
	Received      uint64         `json:"collector_records"`
	Drops         map[string]int `json:"collector_drops"`
	LostSeq       uint64         `json:"collector_lost_records"`
	UDPDrops      uint64         `json:"udp_rcvbuf_errors"`
	Rows          uint64         `json:"clickhouse_rows"`
	MaxLagBatches uint64         `json:"max_lag_batches"`
	MaxLagSeconds float64        `json:"max_lag_seconds"`
	EndLagBatches uint64         `json:"end_lag_batches"`
	LagSlope      float64        `json:"lag_slope_batches_per_min"`
	Drain         string         `json:"drain"`
	RecsPerBatch  float64        `json:"records_per_batch"`
	MaxBuffer     float64        `json:"collector_max_buffer_bytes"`
	Probe         loadkit.Stats  `json:"api"`
	Pass          bool           `json:"pass"`
	Why           []string       `json:"why,omitempty"`
}

func (s *step) loss() int64 { return int64(s.Sent) - int64(s.Received) } //nolint:gosec // recuentos

func main() {
	rates := flag.String("rates", envOr("RATE", "5000"), "tasas (registros/s) del criterio, separadas por comas")
	duration := flag.Duration("duration", durEnv("DURATION", 5*time.Minute), "duración de cada tasa del criterio (1h en el nocturno)")
	ramp := flag.Bool("ramp", os.Getenv("RAMP") == "1", "busca el máximo sostenible subiendo la tasa")
	rampRates := flag.String("ramp-rates", envOr("RAMP_RATES", "10000,15000,20000,30000,40000,60000"), "escalones de la rampa")
	rampDur := flag.Duration("ramp-duration", durEnv("RAMP_DURATION", 2*time.Minute), "duración de cada escalón de la rampa")
	flowsim := flag.String("flowsim", envOr("FLOWSIM", "bin/load/flowsim"), "binario del simulador")
	workers := flag.Int("probe-workers", 2, "clientes concurrentes de la API de tráfico")
	every := flag.Duration("probe-every", 500*time.Millisecond, "pausa entre peticiones de cada cliente")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	env, err := loadkit.LoadEnv()
	check(err)
	api, err := loadkit.NewAPI(env.APIURL, env.AdminEmail, env.AdminPasswordFile, filepath.Join(env.Dir, "state.json"))
	check(err)
	st, err := api.Setup(ctx, loadkit.ClientPrefix)
	check(err)
	target, source, err := env.CollectorAddr(ctx)
	check(err)
	log.Printf("ISP %s, nodo %s, exportador %s (origen %s) → collector %s", st.TenantID, st.SiteID, st.RouterID, source, target)
	check(env.WriteInventory(ctx, st, source))
	bus, err := loadkit.ConnectBus(env.NATSURL)
	check(err)
	defer bus.NC.Close()

	r := runner{env: env, api: api, bus: bus, tenant: st.TenantID, target: target, flowsim: *flowsim,
		workers: *workers, every: *every}
	var steps []step
	criterionOK := true
	for _, rate := range parseRates(*rates) {
		s := r.run(ctx, rate, *duration)
		steps = append(steps, s)
		criterionOK = criterionOK && s.Pass
		if ctx.Err() != nil {
			break
		}
	}
	if *ramp && ctx.Err() == nil {
		for _, rate := range parseRates(*rampRates) {
			s := r.run(ctx, rate, *rampDur)
			steps = append(steps, s)
			if !s.Pass || ctx.Err() != nil {
				break
			}
		}
	}
	check(report(env.Dir, steps))
	if !criterionOK {
		log.Print("load-i1: KO — la tasa del criterio no se sostiene (ver bin/load/results.md)")
		os.Exit(1)
	}
	log.Print("load-i1: OK")
}

type runner struct {
	env     loadkit.Env
	api     *loadkit.API
	bus     *loadkit.Bus
	tenant  string
	target  string
	flowsim string
	workers int
	every   time.Duration
	seed    int64
}

type collectorSnap struct {
	records, lost, batches float64
	drops                  map[string]float64
	udp                    uint64
	rows                   uint64
}

func (r *runner) snap(ctx context.Context) collectorSnap {
	m, err := loadkit.Scrape(ctx, r.env.CollectorMetrics)
	if err != nil {
		log.Printf("aviso: /metrics del collector: %v", err)
	}
	s := collectorSnap{records: m.Sum("horus_collector_records_total"), lost: m.Sum("horus_collector_lost_records_total"),
		batches: m.Sum("horus_collector_batches_published_total"), drops: map[string]float64{}}
	for _, reason := range []string{"unknown_exporter", "malformed", "no_template", "excluded", "tunnel", "queue_full", "bus_unavailable"} {
		s.drops[reason] = m.Sum("horus_collector_dropped_total", `reason="`+reason+`"`)
	}
	s.udp, _ = r.env.UDPDrops(ctx)
	s.rows, err = r.env.FlowRows(ctx, r.tenant)
	if err != nil {
		log.Printf("aviso: ClickHouse: %v", err)
	}
	return s
}

func (r *runner) run(ctx context.Context, rate float64, d time.Duration) step {
	r.seed++
	s := step{Rate: rate, Duration: d.String(), Drops: map[string]int{}}
	log.Printf("== %.0f flujos/s durante %s", rate, d)
	before := r.snap(ctx)
	simLog, _ := os.Create(filepath.Join(r.env.Dir, fmt.Sprintf("flowsim-%.0f.log", rate)))
	defer func() { _ = simLog.Close() }()
	sim := &loadkit.Sim{Bin: r.flowsim, Target: r.target, Rate: rate, Duration: d, Seed: r.seed,
		Expected: filepath.Join(r.env.Dir, fmt.Sprintf("expected-%.0f.json", rate)), Log: io.MultiWriter(simLog)}
	pctx, pstop := context.WithCancel(ctx)
	lat := &loadkit.Latencies{}
	wg := loadkit.ProbeTraffic(pctx, r.api, r.workers, r.every, lat)
	if err := sim.Start(ctx); err != nil {
		pstop()
		s.Why = append(s.Why, "simulador: "+err.Error())
		return s
	}
	type sample struct {
		t       time.Duration
		pending uint64
	}
	var samples []sample
	start := time.Now()
	tick := time.NewTicker(5 * time.Second)
	var simErr error
loop:
	for {
		select {
		case simErr = <-sim.Done():
			break loop
		case <-ctx.Done():
			break loop
		case <-tick.C:
			lag, err := r.bus.IngesterLag(ctx)
			if err != nil {
				log.Printf("aviso: lag: %v", err)
				continue
			}
			samples = append(samples, sample{time.Since(start), lag.Total()})
			s.MaxLagBatches = max(s.MaxLagBatches, lag.Total())
			if m, err := loadkit.Scrape(ctx, r.env.CollectorMetrics); err == nil {
				s.MaxBuffer = math.Max(s.MaxBuffer, m.Sum("horus_collector_buffer_bytes"))
			}
			if len(samples)%12 == 0 {
				log.Printf("   t=%s lag=%d lotes stream=%d msgs", time.Since(start).Round(time.Second), lag.Total(), lag.StreamMsgs)
			}
		}
	}
	tick.Stop()
	pstop()
	wg.Wait()
	s.Probe = lat.Stats()
	if simErr != nil {
		s.Why = append(s.Why, "simulador: "+simErr.Error())
	}
	if len(samples) > 0 {
		s.EndLagBatches = samples[len(samples)-1].pending
	}
	s.LagSlope = slope(samplesXY(samples, func(x sample) (float64, float64) { return x.t.Minutes(), float64(x.pending) }))

	// Drenaje: backlog del ingester a cero y filas estables en ClickHouse.
	drainStart := time.Now()
	var after collectorSnap
	for {
		lag, err := r.bus.IngesterLag(ctx)
		after = r.snap(ctx)
		published := after.records - before.records
		if err == nil && lag.Total() == 0 && float64(after.rows-before.rows) >= published {
			break
		}
		if time.Since(drainStart) > 3*time.Minute || ctx.Err() != nil {
			s.Why = append(s.Why, "el backlog no se vacía en 3 min")
			break
		}
		time.Sleep(2 * time.Second)
	}
	drain := time.Since(drainStart)
	s.Drain = drain.Round(time.Second).String()
	s.Sent, _ = sim.Sent()
	s.Received = uint64(after.records - before.records)
	s.LostSeq = uint64(after.lost - before.lost)
	s.UDPDrops = after.udp - before.udp
	s.Rows = after.rows - before.rows
	for k, v := range after.drops {
		if dv := int(v - before.drops[k]); dv > 0 {
			s.Drops[k] = dv
		}
	}
	if b := after.batches - before.batches; b > 0 {
		s.RecsPerBatch = math.Round(float64(s.Received)/b*10) / 10
	}
	s.MaxLagSeconds = math.Round(float64(s.MaxLagBatches)*s.RecsPerBatch/rate*10) / 10

	// Veredicto.
	if s.Sent == 0 {
		s.Why = append(s.Why, "el simulador no envió nada")
	}
	if s.loss() != 0 || s.LostSeq > 0 || s.UDPDrops > 0 || len(s.Drops) > 0 {
		s.Why = append(s.Why, fmt.Sprintf("pérdida en el collector: %d registros (secuencia %d, UDP %d, descartes %v)",
			s.loss(), s.LostSeq, s.UDPDrops, s.Drops))
	}
	if s.Rows != s.Received {
		s.Why = append(s.Why, fmt.Sprintf("ClickHouse: %d filas para %d registros publicados", s.Rows, s.Received))
	}
	if s.MaxLagSeconds > maxLagSeconds {
		s.Why = append(s.Why, fmt.Sprintf("lag máximo %.1f s > %.0f s", s.MaxLagSeconds, maxLagSeconds))
	}
	if drain > maxDrain {
		s.Why = append(s.Why, "drenaje "+s.Drain+" > "+maxDrain.String())
	}
	if s.Probe.N == 0 || s.Probe.P95 >= p95Budget || s.Probe.Errors > 0 {
		s.Why = append(s.Why, fmt.Sprintf("API de tráfico: p95 %s, %d errores %v", loadkit.Fmt(s.Probe.P95), s.Probe.Errors, s.Probe.ByStatus))
	}
	s.Pass = len(s.Why) == 0
	verdict := "OK"
	if !s.Pass {
		verdict = "KO: " + strings.Join(s.Why, "; ")
	}
	log.Printf("   enviados %d, collector %d, ClickHouse %d, lag máx %d lotes (%.1f s), drenaje %s, p95 %s (%d peticiones) → %s",
		s.Sent, s.Received, s.Rows, s.MaxLagBatches, s.MaxLagSeconds, s.Drain, loadkit.Fmt(s.Probe.P95), s.Probe.N, verdict)
	return s
}

func samplesXY[T any](xs []T, f func(T) (float64, float64)) (x, y []float64) {
	for _, v := range xs {
		a, b := f(v)
		x, y = append(x, a), append(y, b)
	}
	return x, y
}

// slope es la pendiente por mínimos cuadrados (lotes/min) de la segunda
// mitad de la carga: positiva y sostenida = el lag crece.
func slope(x, y []float64) float64 {
	h := len(x) / 2
	x, y = x[h:], y[h:]
	n := float64(len(x))
	if n < 2 {
		return 0
	}
	var sx, sy, sxx, sxy float64
	for i := range x {
		sx, sy, sxx, sxy = sx+x[i], sy+y[i], sxx+x[i]*x[i], sxy+x[i]*y[i]
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0
	}
	return math.Round((n*sxy-sx*sy)/den*10) / 10
}

func report(dir string, steps []step) error {
	b, err := json.MarshalIndent(map[string]any{"at": time.Now().UTC(), "host": hostInfo(), "steps": steps}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "results.json"), b, 0o644); err != nil { //nolint:gosec // informe
		return err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Prueba de carga I1-26 — %s — %s\n\n", time.Now().UTC().Format(time.RFC3339), hostInfo())
	sb.WriteString("| Flujos/s | Duración | Enviados | Collector | Pérdida | ClickHouse | Lag máx. (lotes / s) | Pendiente lag (lotes/min) | Drenaje | p95 API | Peticiones | Resultado |\n")
	sb.WriteString("| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |\n")
	best := 0.0
	for _, s := range steps {
		res := "OK"
		if !s.Pass {
			res = "KO: " + strings.Join(s.Why, "; ")
		} else {
			best = math.Max(best, s.Rate)
		}
		fmt.Fprintf(&sb, "| %.0f | %s | %d | %d | %d | %d | %d / %.1f | %.1f | %s | %s | %d | %s |\n", s.Rate, s.Duration, s.Sent, s.Received,
			s.loss(), s.Rows, s.MaxLagBatches, s.MaxLagSeconds, s.LagSlope, s.Drain, loadkit.Fmt(s.Probe.P95), s.Probe.N, res)
	}
	fmt.Fprintf(&sb, "\nMáximo sostenible medido: **%.0f flujos/s**\n", best)
	md := sb.String()
	fmt.Print("\n" + md)
	return os.WriteFile(filepath.Join(dir, "results.md"), []byte(md), 0o644) //nolint:gosec // informe
}

func hostInfo() string {
	cpu := "?"
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "model name") {
				cpu = strings.TrimSpace(strings.SplitN(l, ":", 2)[1])
				break
			}
		}
	}
	mem := ""
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		f := strings.Fields(strings.SplitN(string(b), "\n", 2)[0])
		if len(f) >= 2 {
			if kb, err := strconv.ParseFloat(f[1], 64); err == nil {
				mem = fmt.Sprintf(", %.0f GiB RAM", kb/1024/1024)
			}
		}
	}
	return fmt.Sprintf("%d vCPU %s%s", runtime.NumCPU(), cpu, mem)
}

func parseRates(s string) []float64 {
	var out []float64
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		v, err := strconv.ParseFloat(p, 64)
		check(err)
		out = append(out, v)
	}
	return out
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

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
