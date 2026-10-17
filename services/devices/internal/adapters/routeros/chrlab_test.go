//go:build chrlab

package routeros

// Prueba contra un RouterOS REAL: el CHR del laboratorio (infrastructure/lab/chr, `make lab-up`).
// No corre en CI: necesita el laboratorio levantado y la etiqueta `chrlab`.
//
//	HORUS_CHR_URL=https://10.255.3.17 HORUS_CHR_SECRETS=$PWD/.lab/secrets.env \
//	HORUS_CHR_CAPTURE=services/devices/internal/adapters/routeros/testdata/chr-7.12 \
//	go test -tags chrlab -run TestCHRLab -v ./services/devices/internal/adapters/routeros/
//
// Lee por el túnel con el usuario de solo lectura `horus` exactamente lo que lee Horus (mismas
// rutas y `.proplist`), comprueba que el adaptador lo entiende y, con HORUS_CHR_CAPTURE, guarda
// las respuestas tal cual como fixtures (el laboratorio no tiene secretos en estas rutas; la
// contraseña se lee del archivo ignorado por Git y nunca se escribe).

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
)

func chrLabEnv(t *testing.T) (base, user, pass string) {
	t.Helper()
	base = os.Getenv("HORUS_CHR_URL")
	secrets := os.Getenv("HORUS_CHR_SECRETS")
	if base == "" || secrets == "" {
		t.Skip("HORUS_CHR_URL y HORUS_CHR_SECRETS no definidos (laboratorio CHR apagado)")
	}
	raw, err := os.ReadFile(secrets)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && k == "HORUS_API_PASSWORD" {
			pass = strings.TrimSpace(v)
		}
	}
	if pass == "" {
		t.Fatal("HORUS_API_PASSWORD ausente en " + secrets)
	}
	return base, "horus", pass
}

// rawGet hace el mismo GET que Client.Print pero devuelve el cuerpo sin interpretar.
func rawGet(t *testing.T, base, user, pass, path string, proplist []string) []byte {
	t.Helper()
	u := strings.TrimRight(base, "/") + "/rest/" + path
	if len(proplist) > 0 {
		u += "?" + url.Values{".proplist": {strings.Join(proplist, ",")}}.Encode()
	}
	hc := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // CHR de laboratorio, huella comprobada aparte
	}}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	req.SetBasicAuth(user, pass)
	res, err := hc.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d %s", path, res.StatusCode, b)
	}
	return b
}

func TestCHRLab(t *testing.T) {
	base, user, pass := chrLabEnv(t)
	ctx := context.Background()

	// TOFU como en el alta: primer contacto sin huella, luego todo con la huella fijada.
	c := New(Target{BaseURL: base, User: user, Password: pass}, 30*time.Second, nil)
	if _, err := c.Print(ctx, "system/resource"); err != nil {
		t.Fatalf("primer contacto: %v", err)
	}
	pinned := c.ObservedFingerprint()
	t.Logf("huella observada: %s", pinned)

	tunnel := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	rd := Reader{Timeout: 30 * time.Second, BaseURL: func(string) string { return base }}
	target := app.ReadTarget{TunnelIP: tunnel, User: user, Password: pass, Pinned: pinned}
	facts, _, err := rd.Read(ctx, target)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if facts.Version == "" || !strings.HasPrefix(facts.Version, "7.") {
		t.Fatalf("versión: %q", facts.Version)
	}
	t.Logf("facts: %+v", facts)

	probes, err := rd.ReadNetwatch(ctx, target)
	if err != nil {
		t.Fatalf("ReadNetwatch: %v", err)
	}
	for _, p := range probes {
		if !strings.HasPrefix(p.Comment, "horus-uplink") {
			continue
		}
		if p.Type != "icmp" || p.Status == "" {
			t.Errorf("sonda %s: tipo/estado %q/%q", p.Host, p.Type, p.Status)
		}
		if p.Status != "unknown" && (p.SentCount == nil || p.LossPercent == nil) {
			t.Errorf("sonda %s sin contadores: %+v", p.Host, p)
		}
		if p.Status == "up" && (p.RTTAvgMs == nil || p.RTTMinMs == nil || p.RTTMaxMs == nil || p.RTTJitterMs == nil || p.RTTStdevMs == nil) {
			t.Errorf("sonda %s up sin RTT completo: %+v", p.Host, p)
		}
		t.Logf("sonda %s %s pérdida=%v avg=%v jitter=%v", p.Host, p.Status, deref(p.LossPercent), deref(p.RTTAvgMs), deref(p.RTTJitterMs))
	}

	// El usuario horus no puede escribir (aunque se salte el transporte de solo lectura).
	hc := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // CHR de laboratorio
	}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/rest/system/identity/set",
		strings.NewReader(`{"name":"pwned"}`))
	req.SetBasicAuth(user, pass)
	req.Header.Set("Content-Type", "application/json")
	if res, err := hc.Do(req); err == nil {
		_ = res.Body.Close()
		if res.StatusCode/100 == 2 {
			t.Fatalf("el usuario horus PUEDE escribir (HTTP %d)", res.StatusCode)
		}
		t.Logf("escritura con horus rechazada: HTTP %d", res.StatusCode)
	}

	dir := os.Getenv("HORUS_CHR_CAPTURE")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	reads := map[string][]string{
		"system/resource":  nil,
		"ip/pool":          {"name", "ranges"},
		"ipv6/pool":        {"name", "prefix", "prefix-length"},
		"ppp/profile":      nil,
		"ipv6/dhcp-server": nil,
		"ip/address":       {"address", "interface", "dynamic", "disabled"},
		"ipv6/address":     {"address", "interface", "dynamic", "disabled"},
		"tool/netwatch":    nil,
	}
	for path, props := range reads {
		var v any
		b := rawGet(t, base, user, pass, path, props)
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatalf("%s: JSON inválido: %v", path, err)
		}
		out, _ := json.MarshalIndent(v, "", "  ")
		name := strings.ReplaceAll(path, "/", "_") + ".json"
		if err := os.WriteFile(filepath.Join(dir, name), append(out, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
