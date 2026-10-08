package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("línea no JSON %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestLoggerMandatoryFields(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l, _, err := observability.NewLogger(&buf, observability.LogConfig{
		Service: "horus", Process: "horus-app", Version: "1.2.3", Env: "dev",
	})
	if err != nil {
		t.Fatal(err)
	}
	l.Info("plain")
	ctx := observability.WithRole(context.Background(), "devices")
	ctx = observability.WithTenant(ctx, "0192f1c2-0000-7000-8000-000000000001")
	ctx = observability.WithRequestID(ctx, "req-1")
	ctx = observability.WithTrace(ctx, "4bf92f3577b34da6a3ce929d0e0e4736", "00f067aa0ba902b7")
	l.InfoContext(ctx, "in request", "router_id", "r1")

	lines := decodeLines(t, &buf)
	if len(lines) != 2 {
		t.Fatalf("got %d lines", len(lines))
	}
	tsRe := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
	for _, m := range lines {
		for _, k := range []string{"ts", "level", "msg", "service", "role", "trace_id", "span_id", "request_id", "tenant_id", "process", "version", "env"} {
			if _, ok := m[k]; !ok {
				t.Errorf("falta %q en %v", k, m)
			}
		}
		if !tsRe.MatchString(fmt.Sprint(m["ts"])) {
			t.Errorf("ts con formato inválido: %v", m["ts"])
		}
		if m["level"] != "info" {
			t.Errorf("level = %v", m["level"])
		}
		if _, ok := m["time"]; ok {
			t.Error("no debe haber clave time")
		}
	}
	if lines[0]["service"] != "horus" || lines[0]["role"] != "" || lines[0]["tenant_id"] != "" {
		t.Errorf("línea sin contexto: %v", lines[0])
	}
	want := map[string]string{
		"service": "devices", "role": "devices", "tenant_id": "0192f1c2-0000-7000-8000-000000000001",
		"request_id": "req-1", "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736", "span_id": "00f067aa0ba902b7",
	}
	for k, v := range want {
		if lines[1][k] != v {
			t.Errorf("%s = %v, want %v", k, lines[1][k], v)
		}
	}
}

func TestForRoleBindsServiceWithoutDuplicates(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l, _, err := observability.NewLogger(&buf, observability.LogConfig{Service: "horus"})
	if err != nil {
		t.Fatal(err)
	}
	observability.ForRole(l, "auth").Info("hello")
	line := buf.String()
	if strings.Count(line, `"service"`) != 1 || strings.Count(line, `"role"`) != 1 {
		t.Fatalf("claves duplicadas: %s", line)
	}
	if !strings.Contains(line, `"service":"auth"`) || !strings.Contains(line, `"role":"auth"`) {
		t.Fatalf("línea: %s", line)
	}
	// Sin process/version/env vacíos.
	if strings.Contains(line, `"process"`) {
		t.Fatalf("process vacío no debería aparecer: %s", line)
	}
}

func TestLoggerLevelAndFormat(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l, lv, err := observability.NewLogger(&buf, observability.LogConfig{Level: "warn", Format: "text"})
	if err != nil {
		t.Fatal(err)
	}
	l.Info("hidden")
	l.Warn("shown")
	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), "level=warn") {
		t.Fatalf("salida: %s", buf.String())
	}
	lv.Set(slog.LevelDebug)
	l.Debug("now visible")
	if !strings.Contains(buf.String(), "now visible") {
		t.Fatal("cambio de nivel en caliente no aplicado")
	}
	if _, _, err := observability.NewLogger(&buf, observability.LogConfig{Level: "loud"}); err == nil {
		t.Fatal("nivel inválido aceptado")
	}
	if _, _, err := observability.NewLogger(&buf, observability.LogConfig{Format: "xml"}); err == nil {
		t.Fatal("formato inválido aceptado")
	}
}

func TestContextHandlerWithGroup(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l, _, err := observability.NewLogger(&buf, observability.LogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	l.WithGroup("g").Info("grouped", "k", "v")
	lines := decodeLines(t, &buf)
	g, ok := lines[0]["g"].(map[string]any)
	if !ok || g["k"] != "v" {
		t.Fatalf("grupo: %v", lines[0])
	}
}

type failingHandler struct{ slog.Handler }

func (failingHandler) Handle(context.Context, slog.Record) error { return errors.New("boom") }

func TestContextHandlerPropagatesError(t *testing.T) {
	t.Parallel()
	h := observability.NewContextHandler(failingHandler{slog.NewJSONHandler(&bytes.Buffer{}, nil)}, "x")
	if err := h.Handle(context.Background(), slog.Record{}); err == nil {
		t.Fatal("esperaba error")
	}
}

func TestSecretNeverLeaks(t *testing.T) {
	t.Parallel()
	s := observability.Secret("hunter2")
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, nil))
	l.Info("cfg", "password", s)
	j, err := json.Marshal(struct{ P observability.Secret }{s})
	if err != nil {
		t.Fatal(err)
	}
	txt, _ := s.MarshalText()
	outs := []string{buf.String(), string(j), fmt.Sprint(s), fmt.Sprintf("%v %s %#v", s, s, s), string(txt)}
	for _, o := range outs {
		if strings.Contains(o, "hunter2") || !strings.Contains(o, observability.Redacted) {
			t.Errorf("fuga de secreto: %s", o)
		}
	}
	if s.Reveal() != "hunter2" || s.IsZero() || !observability.Secret("").IsZero() {
		t.Fatal("Reveal/IsZero")
	}
}

func TestRegistryBuildInfoAndHandler(t *testing.T) {
	t.Parallel()
	reg := observability.NewRegistry(observability.BuildInfo{Service: "horus", Version: "1.0.0", Commit: "abc"})
	rec := httptest.NewRecorder()
	observability.MetricsHandler(reg).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{`horus_build_info{commit="abc",go_version="go`, `service="horus",version="1.0.0"} 1`, "go_goroutines"} {
		if !strings.Contains(body, want) {
			t.Errorf("métricas sin %q", want)
		}
	}
}

func TestValidateLabels(t *testing.T) {
	t.Parallel()
	if err := observability.ValidateLabels("service", "route", "code"); err != nil {
		t.Fatalf("labels válidos rechazados: %v", err)
	}
	err := observability.ValidateLabels("service", "client_ip", "User_ID")
	if !errors.Is(err, observability.ErrForbiddenLabel) || !strings.Contains(err.Error(), "client_ip") ||
		!strings.Contains(err.Error(), "User_ID") {
		t.Fatalf("err = %v", err)
	}
}

func TestContextGettersEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tr, sp := observability.TraceFrom(ctx)
	if observability.RoleFrom(ctx) != "" || observability.TenantFrom(ctx) != "" ||
		observability.RequestIDFrom(ctx) != "" || tr != "" || sp != "" {
		t.Fatal("getters con contexto vacío deben devolver vacío")
	}
}
