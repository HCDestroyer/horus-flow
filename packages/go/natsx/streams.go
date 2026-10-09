package natsx

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"gopkg.in/yaml.v3"
)

// StreamDef es una entrada de packages/events/streams/streams.yaml.
type StreamDef struct {
	Name            string   `yaml:"name"`
	Subjects        []string `yaml:"subjects"`
	Retention       string   `yaml:"retention"`
	MaxAge          string   `yaml:"max_age"`
	MaxBytes        string   `yaml:"max_bytes"`
	DuplicateWindow string   `yaml:"duplicate_window"`
	MaxMsgSize      string   `yaml:"max_msg_size"`
}

// LoadStreams lee las definiciones de streams del contrato C4.
func LoadStreams(path string) ([]StreamDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("natsx: streams: %w", err)
	}
	var f struct {
		Streams []StreamDef `yaml:"streams"`
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("natsx: streams: %w", err)
	}
	return f.Streams, nil
}

// EnsureStreams crea o actualiza los streams (lo usan los tests y el
// aprovisionamiento de desarrollo; en despliegue los aplica infrastructure/nats).
// max_bytes parametrizados (`${…}`) se dejan sin límite; capBytes > 0 limita
// el max_bytes de cada stream (tests con poco disco).
func EnsureStreams(ctx context.Context, js jetstream.JetStream, defs []StreamDef, capBytes int64) error {
	for _, d := range defs {
		cfg := jetstream.StreamConfig{
			Name: d.Name, Subjects: d.Subjects, Storage: jetstream.FileStorage, Discard: jetstream.DiscardOld,
			AllowDirect: true, Replicas: 1, MaxBytes: -1,
		}
		if d.Retention == "workqueue" {
			cfg.Retention = jetstream.WorkQueuePolicy
		}
		cfg.MaxAge = parseDur(d.MaxAge)
		cfg.Duplicates = parseDur(d.DuplicateWindow)
		if n := parseBytes(d.MaxBytes); n > 0 {
			cfg.MaxBytes = n
		}
		if capBytes > 0 && (cfg.MaxBytes < 0 || cfg.MaxBytes > capBytes) {
			cfg.MaxBytes = capBytes
		}
		if n := parseBytes(d.MaxMsgSize); n > 0 {
			cfg.MaxMsgSize = int32(n) //nolint:gosec // ≤ 1 MiB
		}
		if _, err := js.CreateOrUpdateStream(ctx, cfg); err != nil {
			return fmt.Errorf("natsx: stream %s: %w", d.Name, err)
		}
	}
	return nil
}

func parseDur(s string) time.Duration {
	if s == "" {
		return 0
	}
	if v, ok := strings.CutSuffix(s, "d"); ok {
		n, _ := strconv.Atoi(v)
		return time.Duration(n) * 24 * time.Hour
	}
	d, _ := time.ParseDuration(s)
	return d
}

func parseBytes(s string) int64 {
	if s == "" || strings.Contains(s, "$") {
		return 0
	}
	mult := int64(1)
	for _, u := range []struct {
		suf string
		m   int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3}} {
		if v, ok := strings.CutSuffix(s, u.suf); ok {
			s, mult = v, u.m
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n * mult
}
