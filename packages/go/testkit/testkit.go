// Package testkit reúne ayudas para tests de los módulos de `horus`: logger
// que captura líneas JSON, entorno sintético y espera por condición sin
// time.Sleep (docs/conventions.md §5).
package testkit

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

// LogBuffer acumula la salida de un logger de forma segura entre goroutines.
type LogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write implementa io.Writer.
func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p) //nolint:wrapcheck // bytes.Buffer nunca falla
}

// String devuelve todo lo escrito.
func (b *LogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Records decodifica cada línea JSON. Falla el test si alguna no lo es.
func (b *LogBuffer) Records(t testing.TB) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("testkit: log line is not JSON: %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// Find devuelve las líneas cuyo msg es msg.
func (b *LogBuffer) Find(t testing.TB, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, r := range b.Records(t) {
		if r["msg"] == msg {
			out = append(out, r)
		}
	}
	return out
}

// Logger devuelve un logger JSON de Horus (nivel debug) que escribe en un
// LogBuffer.
func Logger(t testing.TB) (*slog.Logger, *LogBuffer) {
	t.Helper()
	buf := &LogBuffer{}
	l, _, err := observability.NewLogger(buf, observability.LogConfig{Level: "debug", Service: "test"})
	if err != nil {
		t.Fatalf("testkit: %v", err)
	}
	return l, buf
}

// Environ convierte pares clave, valor en formato os.Environ.
func Environ(kv ...string) []string {
	out := make([]string, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, kv[i]+"="+kv[i+1])
	}
	return out
}

// Eventually comprueba cond cada 5 ms hasta que es cierta o pasa timeout.
func Eventually(t testing.TB, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("testkit: condition not met within %s: %s", timeout, msg)
		}
		<-tick.C
	}
}
