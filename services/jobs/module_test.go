package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/module"
)

func TestRegisterChecksAndServes(t *testing.T) {
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"v1.4.1","prerelease":false,"html_url":"https://n/141"}]`))
	}))
	defer src.Close()
	m, err := Register(context.Background(), module.Deps{
		Role: Role, Metrics: prometheus.NewRegistry(),
		Environ: []string{"HORUS_VERSION=1.4.0", "HORUS_UPDATE_SOURCE=" + src.URL, "HORUS_UPDATE_CHECK_FIRST_DELAY=0s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	j := m.(*jobs)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- j.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for j.checker.Status().CheckedAt == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	j.getUpdates(w, httptest.NewRequest("GET", "/api/v1/platform/updates", nil))
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	latest, _ := body["latest"].(map[string]any)
	if body["status"] != "available" || latest["version"] != "1.4.1" || latest["patch_only"] != true {
		t.Fatalf("respuesta = %s", w.Body.String())
	}
}

func TestDisabledIsUnchecked(t *testing.T) {
	m, err := Register(context.Background(), module.Deps{Role: Role, Environ: []string{"HORUS_UPDATE_CHECK=false"}})
	if err != nil {
		t.Fatal(err)
	}
	if st := m.(*jobs).checker.Status(); st.Status != "unchecked" || st.Error == nil {
		t.Fatalf("estado = %+v", st)
	}
	if _, err := Register(context.Background(), module.Deps{Role: Role, Environ: []string{"HORUS_UPDATE_CHANNEL=nightly"}}); err == nil {
		t.Fatal("canal inválido aceptado")
	}
}
