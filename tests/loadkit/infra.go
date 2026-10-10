package loadkit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Env son las variables que imprime `tests/load/stack.sh env`.
type Env struct {
	APIURL, AppMetrics, CollectorMetrics string
	NATSURL, CHHTTP, CHUser, CHPassword  string
	AdminEmail, AdminPasswordFile        string
	Inventory, Compose, Project, Dir     string
	CollectorTarget                      string
}

// LoadEnv lee el entorno (falla si falta algo).
func LoadEnv() (Env, error) {
	get := func(k string) string { return os.Getenv(k) }
	e := Env{APIURL: get("LOAD_API_URL"), AppMetrics: get("LOAD_APP_METRICS"), CollectorMetrics: get("LOAD_COLLECTOR_METRICS"),
		NATSURL: get("LOAD_NATS_URL"), CHHTTP: get("LOAD_CH_HTTP"), CHUser: get("LOAD_CH_USER"),
		AdminEmail: get("LOAD_ADMIN_EMAIL"), AdminPasswordFile: get("LOAD_ADMIN_PASSWORD_FILE"),
		Inventory: get("LOAD_INVENTORY"), Compose: get("LOAD_COMPOSE"), Project: get("COMPOSE_PROJECT_NAME"),
		CollectorTarget: get("LOAD_COLLECTOR_TARGET")}
	for k, v := range map[string]string{"LOAD_API_URL": e.APIURL, "LOAD_NATS_URL": e.NATSURL, "LOAD_COMPOSE": e.Compose,
		"LOAD_INVENTORY": e.Inventory, "LOAD_CH_HTTP": e.CHHTTP} {
		if v == "" {
			return e, fmt.Errorf("falta %s (eval \"$(tests/load/stack.sh env)\")", k)
		}
	}
	e.Dir = filepath.Dir(filepath.Dir(e.Inventory))
	pw, err := os.ReadFile(get("LOAD_CH_PASSWORD_FILE"))
	if err != nil {
		return e, fmt.Errorf("contraseña de ClickHouse: %w", err)
	}
	e.CHPassword = strings.TrimSpace(string(pw))
	return e, nil
}

// ComposeCmd ejecuta `docker compose <args>` del proyecto de la prueba.
func (e Env) ComposeCmd(ctx context.Context, args ...string) (string, error) {
	parts := strings.Fields(e.Compose)
	cmd := exec.CommandContext(ctx, parts[0], append(parts[1:], args...)...) //nolint:gosec // comando de la prueba
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker compose %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

// Docker ejecuta `docker <args>`.
func Docker(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// Container devuelve el nombre del contenedor de un servicio.
func (e Env) Container(service string) string { return e.Project + "-" + service + "-1" }

// CollectorAddr devuelve el destino UDP del simulador: la IP del contenedor
// del collector en la red del compose (sin el proxy UDP de Docker, que en
// carga descarta datagramas) y la IP de origen que verá el collector (la
// puerta de enlace de esa red, que es la del host).
func (e Env) CollectorAddr(ctx context.Context) (target, source string, err error) {
	net := e.Project + "_default"
	source, err = Docker(ctx, "network", "inspect", net, "-f", "{{(index .IPAM.Config 0).Gateway}}")
	if err != nil {
		return "", "", err
	}
	if e.CollectorTarget != "" && os.Getenv("LOAD_VIA_PROXY") == "1" {
		return e.CollectorTarget, source, nil
	}
	ip, err := Docker(ctx, "inspect", e.Container("horus-collector"), "-f",
		"{{(index .NetworkSettings.Networks \""+net+"\").IPAddress}}")
	if err != nil {
		return "", "", err
	}
	return ip + ":4739", source, nil
}

// WriteInventory registra el exportador del simulador en el inventario base
// y reinicia collector y horus-app (con NATS, el fichero base solo se lee al
// arrancar). Espera a que vuelvan a estar healthy.
func (e Env) WriteInventory(ctx context.Context, st State, exporterIP string) error {
	inv := map[string]any{"exporters": []map[string]any{{
		"tenant_id": st.TenantID, "router_id": st.RouterID, "site_id": st.SiteID,
		"name": "rt-carga-01", "site_name": "Nodo de carga", "tunnel_ip": exporterIP, "admin_state": "active",
	}}}
	b, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return err
	}
	if old, err := os.ReadFile(e.Inventory); err == nil && string(old) == string(b) {
		return nil
	}
	tmp := e.Inventory + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil { //nolint:gosec // lo lee el usuario 65532 del contenedor
		return err
	}
	if err := os.Rename(tmp, e.Inventory); err != nil {
		return err
	}
	if _, err := e.ComposeCmd(ctx, "restart", "horus-collector", "horus-app"); err != nil {
		return err
	}
	return e.WaitHealthy(ctx, 3*time.Minute, "horus-collector", "horus-app")
}

// WaitHealthy espera a que los servicios estén healthy.
func (e Env) WaitHealthy(ctx context.Context, d time.Duration, services ...string) error {
	deadline := time.Now().Add(d)
	for {
		pending := ""
		for _, s := range services {
			h, _ := Docker(ctx, "inspect", "-f", "{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}", e.Container(s))
			if h != "healthy" {
				pending += " " + s + "=" + h
			}
		}
		if pending == "" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no healthy tras %s:%s", d, pending)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Metrics es un volcado Prometheus (texto) indexado por nombre+etiquetas.
type Metrics map[string]float64

// Scrape lee /metrics; un error devuelve un mapa vacío y el error.
func Scrape(ctx context.Context, u string) (Metrics, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Metrics{}, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return Metrics{}, err
	}
	defer func() { _ = res.Body.Close() }()
	m := Metrics{}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		if i < 0 {
			continue
		}
		v, err := strconv.ParseFloat(line[i+1:], 64)
		if err != nil {
			continue
		}
		m[line[:i]] = v
	}
	return m, sc.Err()
}

// Sum suma las series de name cuyo texto de etiquetas contiene todos los filtros.
func (m Metrics) Sum(name string, filters ...string) float64 {
	var s float64
	for k, v := range m {
		if k != name && !strings.HasPrefix(k, name+"{") {
			continue
		}
		ok := true
		for _, f := range filters {
			if !strings.Contains(k, f) {
				ok = false
				break
			}
		}
		if ok {
			s += v
		}
	}
	return s
}

// Bus es la conexión NATS de observación (lag y eventos).
type Bus struct {
	NC *nats.Conn
	JS jetstream.JetStream
}

// ConnectBus conecta a NATS con reconexión infinita (sobrevive al reinicio de NATS).
func ConnectBus(u string) (*Bus, error) {
	nc, err := nats.Connect(u, nats.Name("horus-load"), nats.MaxReconnects(-1), nats.ReconnectWait(time.Second),
		nats.RetryOnFailedConnect(true))
	if err != nil {
		return nil, err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, err
	}
	return &Bus{NC: nc, JS: js}, nil
}

// IngesterStream y IngesterDurable son el stream y el durable del ingester.
const (
	IngesterStream  = "TLM_FLOWS"
	IngesterDurable = "flows-ingester"
)

// Lag es el estado del consumer del ingester.
type Lag struct {
	Pending    uint64 // mensajes (lotes) sin entregar
	AckPending int    // entregados sin confirmar
	StreamMsgs uint64
	StreamByte uint64
}

// Total son los lotes aún no confirmados.
func (l Lag) Total() uint64 { return l.Pending + uint64(l.AckPending) } //nolint:gosec // >= 0

// IngesterLag consulta el consumer durable del ingester en TLM_FLOWS.
func (b *Bus) IngesterLag(ctx context.Context) (Lag, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	s, err := b.JS.Stream(ctx, IngesterStream)
	if err != nil {
		return Lag{}, err
	}
	c, err := s.Consumer(ctx, IngesterDurable)
	if err != nil {
		return Lag{}, err
	}
	ci, err := c.Info(ctx)
	if err != nil {
		return Lag{}, err
	}
	si, err := s.Info(ctx)
	if err != nil {
		return Lag{}, err
	}
	return Lag{Pending: ci.NumPending, AckPending: ci.NumAckPending, StreamMsgs: si.State.Msgs, StreamByte: si.State.Bytes}, nil
}

// LimitTelemetry fija el max_bytes de TLM_FLOWS (el búfer ante caídas de
// ClickHouse o del ingester). En dev los roles crean los streams del
// contrato C4 con HORUS_NATS_ENSURE_STREAMS (natsx.EnsureStreams expande
// `${HORUS_TLM_FLOWS_MAX_BYTES:-50GB}`, así que ya no quedan sin límite); en
// producción los aplica `horus nats-provision`. Se mantiene como garantía
// por si el entorno del rol no lleva la variable.
func (b *Bus) LimitTelemetry(ctx context.Context, maxBytes int64) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	s, err := b.JS.Stream(ctx, IngesterStream)
	if err != nil {
		return err
	}
	info, err := s.Info(ctx)
	if err != nil {
		return err
	}
	if info.Config.MaxBytes == maxBytes {
		return nil
	}
	cfg := info.Config
	cfg.MaxBytes = maxBytes
	_, err = b.JS.UpdateStream(ctx, cfg)
	return err
}

// TelemetryBufferBytes es el búfer TLM_FLOWS de las pruebas (LOAD_TLM_MAX_BYTES, 2 GiB).
func TelemetryBufferBytes() int64 {
	if v, err := strconv.ParseInt(os.Getenv("LOAD_TLM_MAX_BYTES"), 10, 64); err == nil && v > 0 {
		return v
	}
	return 2 << 30
}

// CHQuery ejecuta una consulta en ClickHouse por HTTP y devuelve la salida (TSV).
func (e Env) CHQuery(ctx context.Context, q string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	u := e.CHHTTP + "/?" + url.Values{"query": {q}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(e.CHUser, e.CHPassword)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("clickhouse: HTTP %d: %s", res.StatusCode, b)
	}
	return strings.TrimSpace(string(b)), nil
}

// CHCount devuelve un count() de ClickHouse.
func (e Env) CHCount(ctx context.Context, q string) (uint64, error) {
	s, err := e.CHQuery(ctx, q)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(s, 10, 64)
}

// FlowRows cuenta las filas de flows_raw del ISP (FINAL no hace falta: la
// deduplicación es por insert_deduplication_token).
func (e Env) FlowRows(ctx context.Context, tenant string) (uint64, error) {
	return e.CHCount(ctx, "SELECT count() FROM flows.flows_raw WHERE tenant_id = toUUID('"+tenant+"')")
}

// UDPDrops devuelve los errores de recepción UDP (RcvbufErrors) del espacio
// de red del contenedor del collector (/proc/<pid>/net/snmp).
func (e Env) UDPDrops(ctx context.Context) (uint64, error) {
	pid, err := Docker(ctx, "inspect", "-f", "{{.State.Pid}}", e.Container("horus-collector"))
	if err != nil {
		return 0, err
	}
	b, err := os.ReadFile("/proc/" + pid + "/net/snmp")
	if err != nil {
		return 0, err
	}
	var hdr []string
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "Udp:" {
			continue
		}
		if hdr == nil {
			hdr = f
			continue
		}
		for i, h := range hdr {
			if h == "RcvbufErrors" && i < len(f) {
				return strconv.ParseUint(f[i], 10, 64)
			}
		}
	}
	return 0, errors.New("sin RcvbufErrors en net/snmp")
}
