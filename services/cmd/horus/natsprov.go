package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"gopkg.in/yaml.v3"
)

// natsProvision implementa `horus nats-provision`: aplica de forma idempotente
// los streams y durables de packages/events/streams/streams.yaml (contrato C4,
// docs/events.md §3) y los buckets KV de infrastructure/nats/kv.yaml. Lo
// ejecuta el servicio one-shot `nats-init` del compose de producción antes de
// los contenedores horus-* (I1-22). Los módulos no crean streams en producción.
//
//	horus nats-provision --streams /etc/horus/nats/streams.yaml --kv /etc/horus/nats/kv.yaml
//
// Flags: --url (HORUS_NATS_URL), --increment (I1: omite los streams con
// `since` posterior), --timeout. Los valores `${VAR:-defecto}` se expanden con
// el entorno (p. ej. HORUS_TLM_FLOWS_MAX_BYTES). Un stream que ya existe se
// actualiza (los límites cambian sin perder datos); si NATS rechaza la
// actualización (cambio no permitido, como el tipo de retención) falla.
func natsProvision(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("horus nats-provision", flag.ContinueOnError)
	fs.SetOutput(stderr)
	env := envMap(environ)
	url := fs.String("url", firstNonEmpty(env["HORUS_NATS_URL"], nats.DefaultURL), "NATS URL")
	streamsFile := fs.String("streams", "/etc/horus/nats/streams.yaml", "streams.yaml (C4)")
	kvFile := fs.String("kv", "", "KV buckets file (optional)")
	increment := fs.String("increment", "I1", "skip streams whose `since` is later than this increment")
	timeout := fs.Duration("timeout", 60*time.Second, "overall timeout (connect retries included)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	spec, err := loadNATSSpec(*streamsFile, *kvFile, env)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "horus nats-provision: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	nc, err := connectRetry(ctx, *url)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "horus nats-provision: connect %s: %v\n", *url, err)
		return exitFailure
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "horus nats-provision: %v\n", err)
		return exitFailure
	}
	if err := applyNATSSpec(ctx, js, spec, *increment, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "horus nats-provision: %v\n", err)
		return exitFailure
	}
	return exitOK
}

type natsSpec struct {
	Defaults  streamSpec   `yaml:"defaults"`
	Streams   []streamSpec `yaml:"streams"`
	Consumers []struct {
		Stream         string   `yaml:"stream"`
		Durable        string   `yaml:"durable"`
		FilterSubjects []string `yaml:"filter_subjects"`
		AckPolicy      string   `yaml:"ack_policy"`
		AckWait        string   `yaml:"ack_wait"`
		MaxDeliver     int      `yaml:"max_deliver"`
		MaxAckPending  int      `yaml:"max_ack_pending"`
	} `yaml:"provisioned_consumers"`
	KV []struct {
		Bucket      string `yaml:"bucket"`
		Description string `yaml:"description"`
		History     uint8  `yaml:"history"`
		TTL         string `yaml:"ttl"`
		MaxBytes    string `yaml:"max_bytes"`
		Storage     string `yaml:"storage"`
	} `yaml:"kv"`
}

type streamSpec struct {
	Name            string   `yaml:"name"`
	Subjects        []string `yaml:"subjects"`
	Retention       string   `yaml:"retention"`
	MaxAge          string   `yaml:"max_age"`
	MaxBytes        string   `yaml:"max_bytes"`
	DuplicateWindow string   `yaml:"duplicate_window"`
	MaxMsgSize      string   `yaml:"max_msg_size"`
	Storage         string   `yaml:"storage"`
	Discard         string   `yaml:"discard"`
	AllowDirect     *bool    `yaml:"allow_direct"`
	Replicas        int      `yaml:"replicas"`
	Since           string   `yaml:"since"`
}

var envRef = regexp.MustCompile(`\$\{([A-Z0-9_]+)(:-([^}]*))?\}`)

func expandEnv(s string, env map[string]string) string {
	return envRef.ReplaceAllStringFunc(s, func(m string) string {
		p := envRef.FindStringSubmatch(m)
		if v := env[p[1]]; v != "" {
			return v
		}
		return p[3]
	})
}

func loadNATSSpec(streamsFile, kvFile string, env map[string]string) (natsSpec, error) {
	var spec natsSpec
	b, err := os.ReadFile(streamsFile) //nolint:gosec // ruta de configuración del operador
	if err != nil {
		return spec, err
	}
	if err := yaml.Unmarshal([]byte(expandEnv(string(b), env)), &spec); err != nil {
		return spec, fmt.Errorf("%s: %w", streamsFile, err)
	}
	if kvFile != "" {
		b, err := os.ReadFile(kvFile) //nolint:gosec // ruta de configuración del operador
		if err != nil {
			return spec, err
		}
		var kv natsSpec
		if err := yaml.Unmarshal([]byte(expandEnv(string(b), env)), &kv); err != nil {
			return spec, fmt.Errorf("%s: %w", kvFile, err)
		}
		spec.KV = kv.KV
	}
	if len(spec.Streams) == 0 {
		return spec, fmt.Errorf("%s: no streams", streamsFile)
	}
	return spec, nil
}

func connectRetry(ctx context.Context, url string) (*nats.Conn, error) {
	for {
		nc, err := nats.Connect(url, nats.Name("horus-nats-provision"), nats.Timeout(5*time.Second))
		if err == nil {
			return nc, nil
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(time.Second):
		}
	}
}

// incrementNumber convierte "I2" en 2 (0 si no es válido).
func incrementNumber(s string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(s)), "I"))
	return n
}

func applyNATSSpec(ctx context.Context, js jetstream.JetStream, spec natsSpec, increment string, out io.Writer) error {
	cur := incrementNumber(increment)
	for _, s := range spec.Streams {
		if s.Since != "" && incrementNumber(s.Since) > cur {
			_, _ = fmt.Fprintf(out, "stream %s: omitido (since %s > %s)\n", s.Name, s.Since, increment)
			continue
		}
		cfg, err := streamConfig(spec.Defaults, s)
		if err != nil {
			return fmt.Errorf("stream %s: %w", s.Name, err)
		}
		action := "creado"
		if _, err := js.Stream(ctx, cfg.Name); err == nil {
			action = "actualizado"
		} else if !errors.Is(err, jetstream.ErrStreamNotFound) {
			return fmt.Errorf("stream %s: %w", s.Name, err)
		}
		if _, err := js.CreateOrUpdateStream(ctx, cfg); err != nil {
			return fmt.Errorf("stream %s: %w", s.Name, err)
		}
		_, _ = fmt.Fprintf(out, "stream %s: %s\n", s.Name, action)
	}
	for _, c := range spec.Consumers {
		ackWait, err := parseDuration(c.AckWait)
		if err != nil {
			return fmt.Errorf("consumer %s: %w", c.Durable, err)
		}
		ack := jetstream.AckExplicitPolicy
		switch c.AckPolicy {
		case "", "explicit":
		case "none":
			ack = jetstream.AckNonePolicy
		case "all":
			ack = jetstream.AckAllPolicy
		default:
			return fmt.Errorf("consumer %s: ack_policy %q", c.Durable, c.AckPolicy)
		}
		if _, err := js.CreateOrUpdateConsumer(ctx, c.Stream, jetstream.ConsumerConfig{
			Durable: c.Durable, FilterSubjects: c.FilterSubjects, AckPolicy: ack, AckWait: ackWait,
			MaxDeliver: c.MaxDeliver, MaxAckPending: c.MaxAckPending, DeliverPolicy: jetstream.DeliverAllPolicy,
		}); err != nil {
			return fmt.Errorf("consumer %s/%s: %w", c.Stream, c.Durable, err)
		}
		_, _ = fmt.Fprintf(out, "consumer %s/%s: aplicado\n", c.Stream, c.Durable)
	}
	for _, k := range spec.KV {
		ttl, err := parseDuration(k.TTL)
		if err != nil {
			return fmt.Errorf("kv %s: %w", k.Bucket, err)
		}
		maxBytes, err := parseSize(k.MaxBytes)
		if err != nil {
			return fmt.Errorf("kv %s: %w", k.Bucket, err)
		}
		storage := jetstream.FileStorage
		if k.Storage == "memory" {
			storage = jetstream.MemoryStorage
		}
		history := k.History
		if history == 0 {
			history = 1
		}
		if maxBytes == 0 {
			maxBytes = -1
		}
		if _, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: k.Bucket, Description: k.Description,
			History: history, TTL: ttl, MaxBytes: maxBytes, Storage: storage}); err != nil {
			return fmt.Errorf("kv %s: %w", k.Bucket, err)
		}
		_, _ = fmt.Fprintf(out, "kv %s: aplicado\n", k.Bucket)
	}
	return nil
}

func streamConfig(def, s streamSpec) (jetstream.StreamConfig, error) {
	pick := func(v, d string) string { return firstNonEmpty(v, d) }
	cfg := jetstream.StreamConfig{Name: s.Name, Subjects: s.Subjects, Replicas: s.Replicas}
	if cfg.Replicas == 0 {
		cfg.Replicas = max(def.Replicas, 1)
	}
	switch pick(s.Retention, def.Retention) {
	case "", "limits":
		cfg.Retention = jetstream.LimitsPolicy
	case "workqueue":
		cfg.Retention = jetstream.WorkQueuePolicy
	case "interest":
		cfg.Retention = jetstream.InterestPolicy
	default:
		return cfg, fmt.Errorf("retention %q", s.Retention)
	}
	switch pick(s.Storage, def.Storage) {
	case "", "file":
		cfg.Storage = jetstream.FileStorage
	case "memory":
		cfg.Storage = jetstream.MemoryStorage
	default:
		return cfg, fmt.Errorf("storage %q", s.Storage)
	}
	switch pick(s.Discard, def.Discard) {
	case "", "old":
		cfg.Discard = jetstream.DiscardOld
	case "new":
		cfg.Discard = jetstream.DiscardNew
	default:
		return cfg, fmt.Errorf("discard %q", s.Discard)
	}
	if s.AllowDirect != nil {
		cfg.AllowDirect = *s.AllowDirect
	} else if def.AllowDirect != nil {
		cfg.AllowDirect = *def.AllowDirect
	}
	var err error
	if cfg.MaxAge, err = parseDuration(s.MaxAge); err != nil {
		return cfg, err
	}
	if cfg.Duplicates, err = parseDuration(s.DuplicateWindow); err != nil {
		return cfg, err
	}
	if cfg.MaxBytes, err = parseSize(s.MaxBytes); err != nil {
		return cfg, err
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = -1
	}
	size, err := parseSize(s.MaxMsgSize)
	if err != nil {
		return cfg, err
	}
	if size > 0 {
		cfg.MaxMsgSize = int32(min(size, 1<<31-1)) //nolint:gosec // acotado arriba
	}
	return cfg, nil
}

// parseDuration acepta las unidades de time.ParseDuration y además "d" (días).
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return 0, fmt.Errorf("duration %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("duration %q", s)
	}
	return d, nil
}

var sizeRe = regexp.MustCompile(`^(\d+)\s*([KMGT]I?B?|B)?$`)

// parseSize acepta 1024, 64KiB, 512MiB, 1GiB, 50GB (KB/MB/GB/TB = potencias de 10).
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	m := sizeRe.FindStringSubmatch(strings.ToUpper(s))
	if m == nil {
		return 0, fmt.Errorf("size %q", s)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("size %q", s)
	}
	unit := strings.TrimSuffix(m[2], "B")
	mult := map[string]int64{"": 1, "K": 1e3, "M": 1e6, "G": 1e9, "T": 1e12,
		"KI": 1 << 10, "MI": 1 << 20, "GI": 1 << 30, "TI": 1 << 40}[unit]
	return n * mult, nil
}

func envMap(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
