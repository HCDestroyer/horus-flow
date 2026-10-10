package loadkit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"sync"
	"time"
)

// ClientPrefix es el prefijo de clientes del escenario `normal` del simulador.
const ClientPrefix = "10.20.0.0/24"

// Sim es una ejecución del simulador (tools/flowsim) por UDP en tiempo real.
type Sim struct {
	Bin      string        // binario flowsim
	Target   string        // host:puerto del collector
	Rate     float64       // registros/s de fondo
	Duration time.Duration // duración simulada (≈ real con -speed 1)
	Seed     int64
	Expected string // ruta del expected.json
	Log      io.Writer

	cmd  *exec.Cmd
	done chan error
}

// Start lanza el simulador. Escenario `normal` sin NAT ni IPv6: 250 hogares
// en 10.20.0.0/24 con atribución directa (la carga no depende del NAT).
func (s *Sim) Start(ctx context.Context) error {
	args := []string{"-scenario", "normal", "-seed", strconv.FormatInt(s.Seed, 10), "-proto", "ipfix",
		"-rate", strconv.FormatFloat(s.Rate, 'f', 0, 64), "-duration", s.Duration.String(),
		"-nat=false", "-ipv6=false", "-target", s.Target, "-src", "any", "-speed", "1",
		"-expected", s.Expected, "-allow-unmet"}
	s.cmd = exec.CommandContext(ctx, s.Bin, args...) //nolint:gosec // binario de la prueba
	s.cmd.Stdout, s.cmd.Stderr = s.Log, s.Log
	if err := s.cmd.Start(); err != nil {
		return err
	}
	s.done = make(chan error, 1)
	go func() { s.done <- s.cmd.Wait() }()
	return nil
}

// Done se cierra al terminar el simulador.
func (s *Sim) Done() <-chan error { return s.done }

// Sent devuelve los registros enviados según el expected.json.
func (s *Sim) Sent() (uint64, error) {
	b, err := os.ReadFile(s.Expected)
	if err != nil {
		return 0, err
	}
	var exp struct {
		Exporters []struct {
			Totals struct {
				DataRecords uint64 `json:"data_records"`
			} `json:"totals"`
		} `json:"exporters"`
	}
	if err := json.Unmarshal(b, &exp); err != nil {
		return 0, err
	}
	var n uint64
	for _, e := range exp.Exporters {
		n += e.Totals.DataRecords
	}
	return n, nil
}

// Latencies acumula duraciones y errores de una sonda HTTP.
type Latencies struct {
	mu     sync.Mutex
	ok     []time.Duration
	errors map[string]int
}

// Add registra una muestra (status 0 = error de red).
func (l *Latencies) Add(d time.Duration, status int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.errors == nil {
		l.errors = map[string]int{}
	}
	if status == http.StatusOK {
		l.ok = append(l.ok, d)
		return
	}
	l.errors[strconv.Itoa(status)]++
}

// Stats resume la sonda.
type Stats struct {
	N, Errors     int
	P50, P95, Max time.Duration
	ByStatus      map[string]int
}

// Stats devuelve percentiles de las respuestas 200.
func (l *Latencies) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := Stats{N: len(l.ok), ByStatus: map[string]int{}}
	for k, v := range l.errors {
		st.Errors += v
		st.ByStatus[k] = v
	}
	if len(l.ok) == 0 {
		return st
	}
	d := append([]time.Duration(nil), l.ok...)
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	pct := func(p float64) time.Duration { return d[min(len(d)-1, int(p*float64(len(d))))] }
	st.P50, st.P95, st.Max = pct(0.50), pct(0.95), d[len(d)-1]
	return st
}

// TrafficPaths son las consultas que hace la UI de tráfico y el kiosco NOC.
var TrafficPaths = []string{
	"/api/v1/analytics/traffic/timeseries?range=1h&metrics=down_bps,up_bps",
	"/api/v1/analytics/traffic/top?dimension=customers&range=15m&n=10",
	"/api/v1/analytics/traffic/timeseries?range=24h&metrics=down_bps,up_bps,flows_per_second",
	"/api/v1/analytics/traffic/top?dimension=services&range=1h&n=10",
	"/api/v1/analytics/traffic/attribution?range=1h",
}

// ProbeTraffic consulta la API de tráfico con `workers` clientes en bucle
// (cada uno espera `every` entre peticiones) hasta que ctx se cancela.
func ProbeTraffic(ctx context.Context, a *API, workers int, every time.Duration, l *Latencies) *sync.WaitGroup {
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := w; ; i++ {
				start := time.Now()
				r, err := a.TenantDo(ctx, Call{Method: http.MethodGet, Path: TrafficPaths[i%len(TrafficPaths)]})
				if ctx.Err() != nil {
					return
				}
				status := r.Status
				if err != nil {
					status = 0
				}
				l.Add(time.Since(start), status)
				select {
				case <-ctx.Done():
					return
				case <-time.After(every):
				}
			}
		}()
	}
	return &wg
}

// Fmt formatea una duración en ms.
func Fmt(d time.Duration) string { return fmt.Sprintf("%.0f ms", float64(d.Microseconds())/1000) }
