package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/platformevents"
)

// DiagnoseFormatVersion es la versión del formato del paquete (manifest.json):
// sube si cambia la estructura de archivos de forma incompatible.
const DiagnoseFormatVersion = 1

// diagConfig son las conexiones que usa `horus diagnose` (las mismas
// variables que los roles; secretos por *_FILE).
type diagConfig struct {
	Process            string               `env:"HORUS_PROCESS" envDefault:"horus"`
	PostgresDSN        string               `env:"HORUS_POSTGRES_DSN"`
	PostgresPassword   observability.Secret `env:"HORUS_POSTGRES_PASSWORD"`
	NATSURL            observability.Secret `env:"HORUS_NATS_URL"`
	ClickHouseDSN      string               `env:"HORUS_CLICKHOUSE_DSN"`
	ClickHousePassword observability.Secret `env:"HORUS_CLICKHOUSE_PASSWORD"`
	ValkeyURL          string               `env:"HORUS_VALKEY_URL"`
	ValkeyPassword     observability.Secret `env:"HORUS_VALKEY_PASSWORD"`
	ComposeProject     string               `env:"COMPOSE_PROJECT_NAME"`
}

type diagOpts struct {
	output       string
	since        time.Duration
	project      string
	docker       string
	dockerSocket string
	tail         int
	logsDir      string
	adminURLs    []string
	keepCIDRs    []string
	events       int
	timeout      time.Duration
}

// diagnose implementa `horus diagnose`: genera un .tar.gz para investigar un
// fallo (docs/observability.md §11 "Cómo investigar un fallo") con logs
// recientes de todos los contenedores del compose, el registro de eventos de
// plataforma, versiones, estado de streams/consumidores de NATS, salud de las
// dependencias, outbox y cola de alertas, configuración efectiva sin
// secretos y métricas clave. Sin datos de clientes: toda IP que no sea de la
// infraestructura se enmascara (seudónimo estable dentro del paquete), igual
// que correos y tokens.
//
// Interfaz estable (la usa horus-ctl): ver docs/observability.md §11.3.
// Código de salida 0 si escribió el paquete (aunque alguna sección fallara:
// manifest.json lista cada sección y su error), 1 si no pudo escribirlo, 2
// error de uso.
func diagnose(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("horus diagnose", flag.ContinueOnError)
	fs.SetOutput(stderr)
	o := diagOpts{}
	fs.StringVar(&o.output, "output", "", `bundle path ("-" = stdout; default horus-diagnose-<UTC time>.tar.gz)`)
	fs.DurationVar(&o.since, "since", 24*time.Hour, "logs and platform events of this period")
	fs.StringVar(&o.project, "compose-project", "", "docker compose project (default $COMPOSE_PROJECT_NAME or horus)")
	fs.StringVar(&o.docker, "docker", "auto", "collect container logs and state through the docker socket: auto|on|off")
	fs.StringVar(&o.dockerSocket, "docker-socket", "/var/run/docker.sock", "docker API unix socket")
	fs.IntVar(&o.tail, "tail", 20000, "max log lines per container")
	fs.StringVar(&o.logsDir, "logs-dir", "", "extra directory with log files to include (masked), e.g. a journald export")
	admin := fs.String("admin-urls", "http://horus-app:8081,http://horus-collector:8081",
		"admin endpoints (readyz, metrics) of the running processes, comma separated")
	keep := fs.String("keep-cidr", "", "extra infrastructure CIDRs not to mask, comma separated")
	fs.IntVar(&o.events, "events", 5000, "max platform events")
	fs.DurationVar(&o.timeout, "timeout", 2*time.Minute, "overall time limit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	o.adminURLs = splitList(*admin)
	o.keepCIDRs = splitList(*keep)
	switch o.docker {
	case "auto", "on", "off":
	default:
		_, _ = fmt.Fprintln(stderr, "horus diagnose: --docker must be auto, on or off")
		return exitUsage
	}
	cfg, err := config.Load[diagConfig](environ)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "horus diagnose: %v\n", err)
		return exitUsage
	}
	if o.project == "" {
		o.project = cfg.ComposeProject
	}
	if o.project == "" {
		o.project = "horus"
	}
	var out io.Writer = stdout
	path := o.output
	if path != "-" {
		if path == "" {
			path = "horus-diagnose-" + time.Now().UTC().Format("20060102T150405Z") + ".tar.gz"
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // ruta del operador
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "horus diagnose: %v\n", err)
			return exitFailure
		}
		defer func() { _ = f.Close() }()
		out = f
	}
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	b := newBundle(out, newMasker(infraPrefixes(environ, o.keepCIDRs)))
	collectDiagnostics(ctx, b, cfg, o, environ)
	if err := b.Close(); err != nil {
		_, _ = fmt.Fprintf(stderr, "horus diagnose: %v\n", err)
		return exitFailure
	}
	if path != "-" {
		_, _ = fmt.Fprintf(stderr, "horus diagnose: wrote %s (%d client addresses masked)\n", path, b.m.Count())
	}
	return exitOK
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// bundle escribe el .tar.gz; todo el texto pasa por el enmascarador.
type bundle struct {
	mu       sync.Mutex
	gz       *gzip.Writer
	tw       *tar.Writer
	m        *masker
	sections map[string]string
	files    []string
	err      error
}

func newBundle(w io.Writer, m *masker) *bundle {
	gz := gzip.NewWriter(w)
	return &bundle{gz: gz, tw: tar.NewWriter(gz), m: m, sections: map[string]string{}}
}

func (b *bundle) add(name string, data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return
	}
	masked := []byte(b.m.Text(string(data)))
	hdr := &tar.Header{Name: "horus-diagnose/" + name, Mode: 0o600, Size: int64(len(masked)), ModTime: time.Now()}
	if err := b.tw.WriteHeader(hdr); err != nil {
		b.err = err
		return
	}
	if _, err := b.tw.Write(masked); err != nil {
		b.err = err
		return
	}
	b.files = append(b.files, name)
}

func (b *bundle) addJSON(name string, v any) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		raw = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	b.add(name, raw)
}

func (b *bundle) section(name string, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		b.sections[name] = b.m.Text(err.Error())
		return
	}
	if _, ok := b.sections[name]; !ok {
		b.sections[name] = "ok"
	}
}

// Close escribe manifest.json y cierra el paquete.
func (b *bundle) Close() error {
	b.mu.Lock()
	sections := copyMap(b.sections)
	files := slices.Clone(b.files)
	b.mu.Unlock()
	b.addJSON("manifest.json", map[string]any{
		"format_version": DiagnoseFormatVersion, "generated_at": time.Now().UTC().Format(time.RFC3339), "horus_version": version,
		"commit": commit(), "go_version": runtime.Version(), "sections": sections, "files": files,
		"masking": map[string]any{"client_addresses_masked": b.m.Count(),
			"policy": "IPs outside loopback, local container networks, WireGuard tunnel/services CIDRs, the collector IP and --keep-cidr are replaced by [ip:<hmac>] (random key, not stored); emails, bearer tokens and secret parameters are redacted"},
	})
	if b.err != nil {
		return b.err
	}
	if err := b.tw.Close(); err != nil {
		return err //nolint:wrapcheck // error de tar autoexplicativo
	}
	return b.gz.Close() //nolint:wrapcheck // error de gzip autoexplicativo
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

const diagReadme = `Horus Flow — paquete de diagnóstico (horus diagnose)

Sin datos de clientes: las IPs que no son de la infraestructura aparecen como [ip:xxxxxxxx]
(el mismo valor para la misma IP dentro de este paquete), y correos y tokens como [email]/[REDACTED].

  manifest.json          versión del formato, secciones (ok o error) y archivos
  versions.json          horus, PostgreSQL, ClickHouse, NATS, Valkey
  config.json            configuración efectiva de este proceso sin secretos
  health/*.json          salud de dependencias y /readyz de cada proceso
  metrics/*.prom         métricas clave de cada proceso (familias horus_*, http, go, process)
  platform-events.json   registro de eventos de plataforma del periodo (arranques, caídas, migraciones…)
  postgres/*.json        migraciones aplicadas por esquema, outbox pendiente por esquema, cola de alertas
  nats/streams.json      streams y consumidores de JetStream (pendientes, ack pendientes, reentregas)
  clickhouse/*.json      tablas activas (filas y bytes)
  docker/*.json          estado de cada contenedor (reinicios, OOM, salud, política de logs)
  logs/*.log             logs recientes de cada contenedor (JSON por línea, con trace_id/tenant_id/event_id)
  extra/*                archivos de --logs-dir

Guía: docs/observability.md §11 "Cómo investigar un fallo".
`

func collectDiagnostics(ctx context.Context, b *bundle, cfg diagConfig, o diagOpts, environ []string) {
	b.add("README.txt", []byte(diagReadme))
	cfgMap := effectiveConfig(environ)
	b.addJSON("config.json", map[string]any{"process": cfg.Process, "effective": cfgMap, "config_hash": configHash(cfgMap)})
	versions := map[string]any{"horus": map[string]string{"version": version, "commit": commit(), "go": runtime.Version()}}
	var vmu sync.Mutex
	setVersion := func(k string, v any) { vmu.Lock(); versions[k] = v; vmu.Unlock() }

	var wg sync.WaitGroup
	run := func(name string, f func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					b.section(name, fmt.Errorf("panic: %v", r))
				}
			}()
			b.section(name, f(ctx))
		}()
	}
	run("postgres", func(ctx context.Context) error { return diagPostgres(ctx, b, cfg, o, setVersion) })
	run("nats", func(ctx context.Context) error { return diagNATS(ctx, b, cfg, setVersion) })
	run("clickhouse", func(ctx context.Context) error { return diagClickHouse(ctx, b, cfg, setVersion) })
	run("valkey", func(ctx context.Context) error { return diagValkey(ctx, b, cfg, setVersion) })
	run("processes", func(ctx context.Context) error { return diagAdmin(ctx, b, o.adminURLs) })
	run("docker", func(ctx context.Context) error { return diagDocker(ctx, b, o) })
	if o.logsDir != "" {
		run("logs_dir", func(context.Context) error { return diagLogsDir(b, o.logsDir) })
	}
	wg.Wait()
	b.addJSON("versions.json", versions)
}

var errNotConfigured = errors.New("not configured in this process")

func diagPostgres(ctx context.Context, b *bundle, cfg diagConfig, o diagOpts, setVersion func(string, any)) error {
	if cfg.PostgresDSN == "" {
		return errNotConfigured
	}
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: cfg.PostgresDSN, Password: cfg.PostgresPassword.Reveal(), MaxConns: 2, StatementTimeout: 20 * time.Second})
	if err != nil {
		return err //nolint:wrapcheck // error de pgdb con contexto
	}
	defer db.Close()
	start := time.Now()
	if err := db.Ping(ctx); err != nil {
		b.addJSON("health/postgres.json", map[string]any{"status": "fail", "error": err.Error()})
		return err //nolint:wrapcheck // error de pgdb con contexto
	}
	b.addJSON("health/postgres.json", map[string]any{"status": "ok", "latency_ms": time.Since(start).Milliseconds()})
	var v string
	if err := db.Pool.QueryRow(ctx, `SELECT version()`).Scan(&v); err == nil {
		setVersion("postgres", v)
	}
	var errs []error
	// Migraciones aplicadas por esquema (tablas <esquema>.goose_db_version).
	migr := map[string]any{}
	rows, err := db.Pool.Query(ctx, `SELECT table_schema FROM information_schema.tables WHERE table_name = 'goose_db_version' ORDER BY 1`)
	if err == nil {
		schemas, _ := pgx.CollectRows(rows, pgx.RowTo[string])
		for _, s := range schemas {
			var ver int64
			var at time.Time
			if err := db.Pool.QueryRow(ctx, `SELECT max(version_id), max(tstamp) FROM `+pgx.Identifier{s, "goose_db_version"}.Sanitize()+
				` WHERE is_applied`).Scan(&ver, &at); err == nil {
				migr[s] = map[string]any{"version": ver, "last_applied_at": at.UTC()}
			}
		}
	} else {
		errs = append(errs, err)
	}
	b.addJSON("postgres/migrations.json", migr)
	// Outbox: pendientes por esquema (eventos colgados si oldest crece).
	outbox := map[string]any{}
	rows, err = db.Pool.Query(ctx, `SELECT table_schema FROM information_schema.tables WHERE table_name = 'outbox' ORDER BY 1`)
	if err == nil {
		schemas, _ := pgx.CollectRows(rows, pgx.RowTo[string])
		for _, s := range schemas {
			var pending, maxAttempts int64
			var oldest *time.Time
			if err := db.Pool.QueryRow(ctx, `SELECT count(*), min(occurred_at), coalesce(max(attempts), 0) FROM `+
				pgx.Identifier{s, "outbox"}.Sanitize()+` WHERE published_at IS NULL`).Scan(&pending, &oldest, &maxAttempts); err != nil {
				outbox[s] = map[string]any{"error": err.Error()}
				continue
			}
			e := map[string]any{"pending": pending, "max_attempts": maxAttempts}
			if oldest != nil {
				e["oldest_pending_age_seconds"] = int(time.Since(*oldest).Seconds())
			}
			outbox[s] = e
		}
	} else {
		errs = append(errs, err)
	}
	b.addJSON("postgres/outbox.json", outbox)
	// Cola de entregas de alertas (agregada; sin tenant ni destinatarios).
	if q, err := alertsQueue(ctx, db); err == nil {
		b.addJSON("postgres/alerts-queue.json", q)
	}
	// Registro de eventos de plataforma.
	st := platformevents.NewStore(db)
	since := time.Now().Add(-o.since)
	evs, err := st.List(ctx, platformevents.Query{Since: &since, Limit: o.events})
	if err != nil {
		errs = append(errs, fmt.Errorf("platform events: %w", err))
	} else {
		sort.Slice(evs, func(i, j int) bool { return evs[i].OccurredAt.Before(evs[j].OccurredAt) })
		b.addJSON("platform-events.json", evs)
	}
	return errors.Join(errs...)
}

func alertsQueue(ctx context.Context, db *pgdb.DB) (map[string]any, error) {
	out := map[string]any{}
	err := pgx.BeginFunc(ctx, db.Pool, func(tx pgx.Tx) error {
		// El rol de plataforma de alerts salta la RLS (si el usuario no es superusuario).
		_, _ = tx.Exec(ctx, `DO $$ BEGIN IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'alerts_platform') AND NOT
			(SELECT rolsuper FROM pg_roles WHERE rolname = current_user) THEN SET LOCAL ROLE alerts_platform; END IF; END $$`)
		rows, err := tx.Query(ctx, `SELECT status, count(*), min(created_at), coalesce(max(attempts), 0)
			FROM alerts.notification_delivery WHERE created_at > now() - interval '7 days' GROUP BY status`)
		if err != nil {
			return err //nolint:wrapcheck // error de pgx autoexplicativo
		}
		defer rows.Close()
		for rows.Next() {
			var status string
			var n, attempts int64
			var oldest time.Time
			if err := rows.Scan(&status, &n, &oldest, &attempts); err != nil {
				return err //nolint:wrapcheck // error de pgx autoexplicativo
			}
			out[status] = map[string]any{"count": n, "oldest": oldest.UTC(), "max_attempts": attempts}
		}
		return rows.Err() //nolint:wrapcheck // error de pgx autoexplicativo
	})
	return out, err //nolint:wrapcheck // error de pgx autoexplicativo
}

func diagNATS(ctx context.Context, b *bundle, cfg diagConfig, setVersion func(string, any)) error {
	u := cfg.NATSURL.Reveal()
	if u == "" {
		return errNotConfigured
	}
	nc, err := nats.Connect(u, nats.Name("horus-diagnose"), nats.Timeout(5*time.Second))
	if err != nil {
		b.addJSON("health/nats.json", map[string]any{"status": "fail", "error": err.Error()})
		return fmt.Errorf("nats: %w", err)
	}
	defer nc.Close()
	setVersion("nats", nc.ConnectedServerVersion())
	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("jetstream: %w", err)
	}
	acct, err := js.AccountInfo(ctx)
	health := map[string]any{"status": "ok", "connected_url": nc.ConnectedUrlRedacted(), "rtt_ms": 0}
	if rtt, err := nc.RTT(); err == nil {
		health["rtt_ms"] = rtt.Milliseconds()
	}
	if err == nil {
		health["jetstream"] = map[string]any{"memory": acct.Memory, "store": acct.Store, "streams": acct.Streams, "consumers": acct.Consumers,
			"limits": acct.Limits}
	} else {
		health["jetstream_error"] = err.Error()
	}
	b.addJSON("health/nats.json", health)
	var streams []map[string]any
	lister := js.ListStreams(ctx)
	for si := range lister.Info() {
		s := map[string]any{"name": si.Config.Name, "subjects": si.Config.Subjects, "retention": si.Config.Retention.String(),
			"max_bytes": si.Config.MaxBytes, "max_age_seconds": int64(si.Config.MaxAge.Seconds()), "storage": si.Config.Storage.String(),
			"messages": si.State.Msgs, "bytes": si.State.Bytes, "first_seq": si.State.FirstSeq, "last_seq": si.State.LastSeq,
			"last_ts": si.State.LastTime, "consumers": si.State.Consumers}
		if si.Config.MaxBytes > 0 {
			s["used_ratio"] = round2(float64(si.State.Bytes) / float64(si.Config.MaxBytes))
		}
		var consumers []map[string]any
		if st, err := js.Stream(ctx, si.Config.Name); err == nil {
			cl := st.ListConsumers(ctx)
			for ci := range cl.Info() {
				c := map[string]any{"name": ci.Name, "durable": ci.Config.Durable != "", "num_pending": ci.NumPending,
					"num_ack_pending": ci.NumAckPending, "num_redelivered": ci.NumRedelivered, "num_waiting": ci.NumWaiting,
					"delivered_stream_seq": ci.Delivered.Stream, "ack_floor_stream_seq": ci.AckFloor.Stream,
					"filter_subjects": append(ci.Config.FilterSubjects, ci.Config.FilterSubject), "max_deliver": ci.Config.MaxDeliver}
				if ci.Delivered.Last != nil {
					c["last_delivered"] = ci.Delivered.Last.UTC()
				}
				consumers = append(consumers, c)
			}
		}
		s["consumer_info"] = consumers
		streams = append(streams, s)
	}
	b.addJSON("nats/streams.json", streams)
	return lister.Err() //nolint:wrapcheck // error de jetstream autoexplicativo
}

func diagClickHouse(ctx context.Context, b *bundle, cfg diagConfig, setVersion func(string, any)) error {
	if cfg.ClickHouseDSN == "" {
		return errNotConfigured
	}
	opts, err := clickhouse.ParseDSN(cfg.ClickHouseDSN)
	if err != nil {
		return fmt.Errorf("clickhouse dsn: %w", err)
	}
	if p := cfg.ClickHousePassword.Reveal(); p != "" {
		opts.Auth.Password = p
	}
	opts.DialTimeout = 5 * time.Second
	db := clickhouse.OpenDB(opts)
	defer func() { _ = db.Close() }()
	start := time.Now()
	if err := db.PingContext(ctx); err != nil {
		b.addJSON("health/clickhouse.json", map[string]any{"status": "fail", "error": err.Error()})
		return fmt.Errorf("clickhouse: %w", err)
	}
	b.addJSON("health/clickhouse.json", map[string]any{"status": "ok", "latency_ms": time.Since(start).Milliseconds()})
	var v string
	if err := db.QueryRowContext(ctx, `SELECT version()`).Scan(&v); err == nil {
		setVersion("clickhouse", v)
	}
	rows, err := db.QueryContext(ctx, `SELECT database, table, sum(rows), sum(bytes_on_disk), count(), max(modification_time)
		FROM system.parts WHERE active AND database NOT IN ('system', 'INFORMATION_SCHEMA', 'information_schema')
		GROUP BY database, table ORDER BY sum(bytes_on_disk) DESC LIMIT 50`)
	if err != nil {
		return fmt.Errorf("clickhouse parts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var tables []map[string]any
	for rows.Next() {
		var dbn, tbl string
		var nrows, bytes, parts uint64
		var mod time.Time
		if err := rows.Scan(&dbn, &tbl, &nrows, &bytes, &parts, &mod); err != nil {
			return fmt.Errorf("clickhouse parts: %w", err)
		}
		tables = append(tables, map[string]any{"database": dbn, "table": tbl, "rows": nrows, "bytes_on_disk": bytes, "parts": parts,
			"last_modified": mod.UTC()})
	}
	b.addJSON("clickhouse/tables.json", tables)
	return closeRows(rows)
}

func closeRows(r *sql.Rows) error { return r.Err() } //nolint:wrapcheck // error de database/sql autoexplicativo

func diagValkey(ctx context.Context, b *bundle, cfg diagConfig, setVersion func(string, any)) error {
	if cfg.ValkeyURL == "" {
		return errNotConfigured
	}
	u, err := url.Parse(cfg.ValkeyURL)
	if err != nil {
		return fmt.Errorf("valkey url: %w", err)
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":6379"
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		b.addJSON("health/valkey.json", map[string]any{"status": "fail", "error": err.Error()})
		return fmt.Errorf("valkey: %w", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	user := "default"
	if u.User != nil && u.User.Username() != "" {
		user = u.User.Username()
	}
	pass := cfg.ValkeyPassword.Reveal()
	if pass == "" && u.User != nil {
		pass, _ = u.User.Password()
	}
	start := time.Now()
	if pass != "" {
		if _, err := respCmd(conn, r, "AUTH", user, pass); err != nil {
			return fmt.Errorf("valkey auth: %w", err)
		}
	}
	if _, err := respCmd(conn, r, "PING"); err != nil {
		b.addJSON("health/valkey.json", map[string]any{"status": "fail", "error": err.Error()})
		return fmt.Errorf("valkey ping: %w", err)
	}
	health := map[string]any{"status": "ok", "latency_ms": time.Since(start).Milliseconds()}
	if info, err := respCmd(conn, r, "INFO"); err == nil {
		keep := map[string]string{}
		for _, line := range strings.Split(info, "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
			if !ok {
				continue
			}
			switch k {
			case "redis_version", "valkey_version", "uptime_in_seconds", "used_memory_human", "maxmemory_human", "connected_clients",
				"rejected_connections", "evicted_keys", "rdb_last_bgsave_status", "aof_enabled", "loading", "role":
				keep[k] = v
			}
		}
		health["info"] = keep
		if v := keep["valkey_version"]; v != "" {
			setVersion("valkey", v)
		} else if v := keep["redis_version"]; v != "" {
			setVersion("valkey", v)
		}
	}
	b.addJSON("health/valkey.json", health)
	return nil
}

// respCmd envía un comando RESP y devuelve la respuesta simple o bulk.
func respCmd(w io.Writer, r *bufio.Reader, args ...string) (string, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&buf, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		return "", err //nolint:wrapcheck // error de red autoexplicativo
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err //nolint:wrapcheck // error de red autoexplicativo
	}
	line = strings.TrimRight(line, "\r\n")
	switch {
	case strings.HasPrefix(line, "+"):
		return line[1:], nil
	case strings.HasPrefix(line, "-"):
		return "", errors.New(line[1:])
	case strings.HasPrefix(line, "$"):
		var n int
		if _, err := fmt.Sscanf(line[1:], "%d", &n); err != nil || n < 0 {
			return "", nil
		}
		data := make([]byte, n+2)
		if _, err := io.ReadFull(r, data); err != nil {
			return "", err //nolint:wrapcheck // error de red autoexplicativo
		}
		return string(data[:n]), nil
	}
	return line, nil
}

// metricPrefixes son las familias que se incluyen de /metrics.
var metricPrefixes = []string{"horus_", "http_server_", "go_goroutines", "go_memstats_heap_inuse_bytes", "process_resident_memory_bytes",
	"process_start_time_seconds", "process_open_fds", "jetstream_"}

func diagAdmin(ctx context.Context, b *bundle, urls []string) error {
	hc := &http.Client{Timeout: 5 * time.Second}
	var errs []error
	reached := 0
	for _, base := range urls {
		u, err := url.Parse(base)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		name := strings.NewReplacer(":", "_", "/", "_").Replace(u.Host)
		if body, code, err := httpBody(ctx, hc, base+"/readyz"); err == nil {
			reached++
			var rep any
			if json.Unmarshal(body, &rep) != nil {
				rep = string(body)
			}
			b.addJSON("health/readyz-"+name+".json", map[string]any{"http_status": code, "report": rep})
		} else {
			errs = append(errs, fmt.Errorf("%s: %w", u.Host, err))
			continue
		}
		if body, _, err := httpBody(ctx, hc, base+"/metrics"); err == nil {
			b.add("metrics/"+name+".prom", filterMetrics(body))
		}
	}
	if reached == 0 && len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func httpBody(ctx context.Context, hc *http.Client, u string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err //nolint:wrapcheck // error de net/http autoexplicativo
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, err //nolint:wrapcheck // error de net/http autoexplicativo
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	return body, resp.StatusCode, err //nolint:wrapcheck // error de io autoexplicativo
}

func filterMetrics(body []byte) []byte {
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		name := strings.TrimPrefix(strings.TrimPrefix(line, "# HELP "), "# TYPE ")
		for _, p := range metricPrefixes {
			if strings.HasPrefix(name, p) {
				out.WriteString(line)
				out.WriteByte('\n')
				break
			}
		}
	}
	return out.Bytes()
}

func diagDocker(ctx context.Context, b *bundle, o diagOpts) error {
	if o.docker == "off" {
		return errors.New("disabled (--docker=off)")
	}
	if _, err := os.Stat(o.dockerSocket); err != nil {
		if o.docker == "auto" {
			return fmt.Errorf("docker socket %s not available: container logs not included (run from the host or mount the socket, docs/observability.md §11.3)", o.dockerSocket)
		}
		return fmt.Errorf("docker socket: %w", err)
	}
	d := newDockerAPI(o.dockerSocket)
	cs, err := d.containers(ctx, o.project)
	if err != nil {
		return err
	}
	if len(cs) == 0 {
		return fmt.Errorf("no containers in compose project %q", o.project)
	}
	var errs []error
	var summary []map[string]any
	for _, c := range cs {
		name := c.name()
		if st, err := d.inspect(ctx, c.ID); err == nil {
			b.addJSON("docker/"+name+".json", st)
			summary = append(summary, map[string]any{"name": name, "state": c.State, "status": c.Status, "image": c.Image,
				"restart_count": st["restart_count"]})
		} else {
			errs = append(errs, err)
		}
		var buf bytes.Buffer
		if err := d.logs(ctx, c.ID, o.since, o.tail, &buf); err != nil {
			errs = append(errs, fmt.Errorf("logs %s: %w", name, err))
		}
		b.add("logs/"+name+".log", buf.Bytes())
	}
	b.addJSON("docker/containers.json", summary)
	return errors.Join(errs...)
}

func diagLogsDir(b *bundle, dir string) error {
	var n int
	err := filepath.WalkDir(dir, func(p string, de os.DirEntry, err error) error {
		if err != nil || de.IsDir() || n >= 200 {
			return err
		}
		info, err := de.Info()
		if err != nil || info.Size() > 256<<20 {
			return nil //nolint:nilerr // se omite el archivo ilegible o enorme
		}
		data, err := os.ReadFile(p) //nolint:gosec // directorio del operador
		if err != nil {
			return nil //nolint:nilerr // se omite el archivo ilegible
		}
		rel, _ := filepath.Rel(dir, p)
		b.add("extra/"+filepath.ToSlash(rel), data)
		n++
		return nil
	})
	return err //nolint:wrapcheck // error de sistema de archivos autoexplicativo
}
