package datasets

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validYAML = `
version: 1
sources:
  - id: good
    name: Good
    kind: reputation
    url: https://example.org/list.txt
    format: netset
    license: CC0-1.0
    license_url: https://example.org/license
    commercial_use: "yes"
    frequency: 1h
  - id: nope
    kind: reputation
    url: https://example.org/nope.txt
    format: netset
    license: CC BY-NC
    license_url: https://example.org/nc
    commercial_use: "no"
    frequency: 24h
  - id: maybe
    kind: asn
    url: https://example.org/maybe.txt
    format: netset
    license: custom
    license_url: https://example.org/tos
    commercial_use: unverified
    frequency: 24h
`

func TestParseConfig(t *testing.T) {
	c, err := ParseConfig([]byte(validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Sources) != 3 || time.Duration(c.Sources[0].Frequency) != time.Hour {
		t.Fatalf("config inesperada: %+v", c)
	}
	if len(c.ByKind("reputation")) != 2 {
		t.Fatal("ByKind")
	}
	for _, tc := range []struct {
		id         string
		unverified bool
		want       bool
	}{
		{"good", false, true}, {"nope", false, false}, {"nope", true, false},
		{"maybe", false, false}, {"maybe", true, true},
	} {
		var s Source
		for _, x := range c.Sources {
			if x.ID == tc.id {
				s = x
			}
		}
		if ok, reason := s.Allowed(tc.unverified); ok != tc.want {
			t.Errorf("Allowed(%s, %v) = %v (%s)", tc.id, tc.unverified, ok, reason)
		}
	}
}

func TestParseConfigRejectsIncomplete(t *testing.T) {
	cases := map[string]string{
		"sin licencia":   "version: 1\nsources:\n  - {id: a1, kind: k, url: 'https://x.org/a', format: f, commercial_use: 'yes', frequency: 1h}\n",
		"uso comercial":  "version: 1\nsources:\n  - {id: a1, kind: k, url: 'https://x.org/a', format: f, license: L, license_url: 'https://x.org/l', commercial_use: maybe, frequency: 1h}\n",
		"sin frecuencia": "version: 1\nsources:\n  - {id: a1, kind: k, url: 'https://x.org/a', format: f, license: L, license_url: 'https://x.org/l', commercial_use: 'yes'}\n",
		"url ftp":        "version: 1\nsources:\n  - {id: a1, kind: k, url: 'ftp://x.org/a', format: f, license: L, license_url: 'https://x.org/l', commercial_use: 'yes', frequency: 1h}\n",
		"versión":        "version: 2\nsources: []\n",
		"duplicada":      "version: 1\nsources:\n  - {id: a1, kind: k, url: 'https://x.org/a', format: f, license: L, license_url: 'https://x.org/l', commercial_use: 'yes', frequency: 1h}\n  - {id: a1, kind: k, url: 'https://x.org/a', format: f, license: L, license_url: 'https://x.org/l', commercial_use: 'yes', frequency: 1h}\n",
		"duración":       "version: 1\nsources:\n  - {id: a1, kind: k, url: 'https://x.org/a', format: f, license: L, license_url: 'https://x.org/l', commercial_use: 'yes', frequency: diario}\n",
		"origen":         "version: 1\nsources:\n  - {id: a1, kind: k, url: 'https://x.org/a', format: f, license: L, license_url: 'https://x.org/l', commercial_use: 'yes', frequency: 1h, origin: ext}\n",
		"on_dangerous":   "version: 1\nsources:\n  - {id: a1, kind: k, url: 'https://x.org/a', format: f, license: L, license_url: 'https://x.org/l', commercial_use: 'yes', frequency: 1h, on_dangerous: allow}\n",
		"max_entries":    "version: 1\nsources:\n  - {id: a1, kind: k, url: 'https://x.org/a', format: f, license: L, license_url: 'https://x.org/l', commercial_use: 'yes', frequency: 1h, max_entries: -1}\n",
	}
	ok := "version: 1\nsources:\n  - {id: a1, kind: k, url: 'https://x.org/a', format: csv, license: L, license_url: 'https://x.org/l', commercial_use: 'yes', frequency: 1h, origin: custom, on_dangerous: warn, max_entries: 10, csv: {column: ip, delimiter: ';'}}\n"
	if c, err := ParseConfig([]byte(ok)); err != nil || !c.Sources[0].IsCustom() || c.Sources[0].CSV.Delimiter != ";" {
		t.Errorf("campos de D20: %v", err)
	}
	for name, y := range cases {
		if _, err := ParseConfig([]byte(y)); err == nil {
			t.Errorf("%s: se esperaba error", name)
		}
	}
}

// fakeFetcher devuelve el contenido o el error configurado por fuente.
type fakeFetcher struct {
	body map[string]string
	err  map[string]error
}

func (f *fakeFetcher) Fetch(_ context.Context, s Source) (io.ReadCloser, error) {
	if err := f.err[s.ID]; err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader(f.body[s.ID])), nil
}

// lineValidator cuenta líneas que son IP; rechaza si hay alguna basura.
func lineValidator(_ context.Context, _ Source, path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, l := range strings.Fields(string(b)) {
		if _, err := netip.ParseAddr(l); err != nil {
			return 0, errors.New("línea inválida " + l)
		}
		n++
	}
	return n, nil
}

func testSource(id string) Source {
	return Source{ID: id, Kind: "reputation", URL: "https://example.org/" + id + ".txt", Format: "netset",
		License: "CC0-1.0", LicenseURL: "https://example.org/l", CommercialUse: CommercialYes,
		Frequency: Duration(time.Hour), MinEntries: 2}
}

func TestRunnerLifecycle(t *testing.T) {
	dir := t.TempDir()
	clock := time.Date(2026, 10, 8, 4, 0, 0, 0, time.UTC)
	ff := &fakeFetcher{body: map[string]string{"a": "192.0.2.1\n192.0.2.2\n"}, err: map[string]error{}}
	r := &Runner{Store: &Store{Root: dir, Tool: "test"}, Fetcher: ff, Validate: lineValidator, Now: func() time.Time { return clock }}
	src := testSource("a")

	// 1. Primera descarga válida.
	res := r.RunOne(context.Background(), src)
	if res.Status != StatusUpdated || res.Manifest == nil || res.Manifest.Entries != 2 {
		t.Fatalf("primera descarga: %+v", res)
	}
	first := *res.Manifest
	if err := r.Store.Verify(&first); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !strings.HasPrefix(first.File, "dt=2026-10-08/20261008T040000Z-a.txt") {
		t.Fatalf("ruta versionada inesperada %q", first.File)
	}
	if _, err := os.Stat(r.Store.Path(&first) + ".manifest.json"); err != nil {
		t.Fatalf("falta manifiesto: %v", err)
	}

	// 2. Mismo contenido: sin cambios.
	clock = clock.Add(time.Hour)
	if res := r.RunOne(context.Background(), src); res.Status != StatusUnchanged {
		t.Fatalf("sin cambios: %+v", res)
	}

	// 3. Fuente caída: se conserva la vigente y crece el contador.
	clock = clock.Add(time.Hour)
	ff.err["a"] = errors.New("connection refused")
	res = r.RunOne(context.Background(), src)
	if res.Status != StatusFailed || res.Manifest == nil || res.Manifest.SHA256 != first.SHA256 {
		t.Fatalf("fuente caída: %+v", res)
	}
	// 4. Contenido corrupto y vacío/truncado: rechazados.
	delete(ff.err, "a")
	ff.body["a"] = "192.0.2.1\n<html>error</html>\n"
	if res := r.RunOne(context.Background(), src); res.Status != StatusFailed {
		t.Fatalf("corrupto: %+v", res)
	}
	ff.body["a"] = ""
	if res := r.RunOne(context.Background(), src); res.Status != StatusFailed {
		t.Fatalf("vacío: %+v", res)
	}
	ff.body["a"] = "192.0.2.9\n" // por debajo de min_entries
	if res := r.RunOne(context.Background(), src); res.Status != StatusFailed {
		t.Fatalf("truncado: %+v", res)
	}
	st, err := r.Store.LoadState("a")
	if err != nil {
		t.Fatal(err)
	}
	if st.Current == nil || st.Current.SHA256 != first.SHA256 || st.ConsecutiveFailures != 4 || st.LastError == "" {
		t.Fatalf("estado tras fallos: %+v", st)
	}
	f, m, err := r.Store.OpenCurrent("a")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	_ = f.Close()
	if string(b) != "192.0.2.1\n192.0.2.2\n" || m.SHA256 != first.SHA256 {
		t.Fatalf("la versión vigente cambió: %q", b)
	}
	// Antigüedad desde el último éxito (paso 2).
	if age, ok := st.Age(clock); !ok || age != time.Hour {
		t.Fatalf("Age = %v %v", age, ok)
	}
	var metrics bytes.Buffer
	if err := WritePrometheus(&metrics, "reputation", []State{st}, clock); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`horus_dataset_age_seconds{kind="reputation",source="a"} 3600`,
		`horus_dataset_consecutive_failures{kind="reputation",source="a"} 4`,
		`horus_dataset_entries{kind="reputation",source="a"} 2`,
	} {
		if !strings.Contains(metrics.String(), want) {
			t.Errorf("métricas sin %q:\n%s", want, metrics.String())
		}
	}

	// 5. Nueva versión válida: se reemplaza y se reinicia el contador.
	clock = clock.Add(25 * time.Hour)
	ff.body["a"] = "192.0.2.1\n192.0.2.3\n192.0.2.4\n"
	res = r.RunOne(context.Background(), src)
	if res.Status != StatusUpdated || res.Manifest.Entries != 3 {
		t.Fatalf("nueva versión: %+v", res)
	}
	st, _ = r.Store.LoadState("a")
	if st.ConsecutiveFailures != 0 || st.LastError != "" {
		t.Fatalf("estado tras éxito: %+v", st)
	}
	// Ningún temporal huérfano.
	if m, _ := filepath.Glob(filepath.Join(dir, "a", ".incoming-*")); len(m) != 0 {
		t.Fatalf("temporales huérfanos: %v", m)
	}
	// 6. Prune: borra el día anterior, conserva el vigente.
	n, err := r.Store.Prune("a", clock)
	if err != nil || n != 1 {
		t.Fatalf("Prune = %d %v", n, err)
	}
	if err := r.Store.Verify(st.Current); err != nil {
		t.Fatalf("Prune borró la versión vigente: %v", err)
	}
}

func TestRunnerSkipsByLicenseAndLimitsSize(t *testing.T) {
	dir := t.TempDir()
	ff := &fakeFetcher{body: map[string]string{"nc": "192.0.2.1\n192.0.2.2\n", "big": strings.Repeat("192.0.2.1\n", 100)}}
	r := &Runner{Store: &Store{Root: dir}, Fetcher: ff, Validate: lineValidator}
	nc := testSource("nc")
	nc.CommercialUse = CommercialNo
	big := testSource("big")
	big.MaxBytes = 50
	res := r.Run(context.Background(), []Source{nc, big})
	if res[0].Status != StatusSkipped {
		t.Fatalf("licencia no comercial: %+v", res[0])
	}
	if _, err := os.Stat(filepath.Join(dir, "nc")); !os.IsNotExist(err) {
		t.Fatal("no debería haberse escrito nada para una fuente omitida")
	}
	if res[1].Status != StatusFailed || !strings.Contains(res[1].Err.Error(), "excede") {
		t.Fatalf("tamaño: %+v", res[1])
	}
}

func TestHTTPAndDirFetcher(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok.txt" {
			_, _ = io.WriteString(w, "192.0.2.1\n")
			return
		}
		http.Error(w, "no", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	f := NewHTTPFetcher("horus-test", time.Second)
	s := testSource("x")
	s.URL = srv.URL + "/ok.txt"
	rc, err := f.Fetch(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(b) != "192.0.2.1\n" {
		t.Fatalf("body %q", b)
	}
	s.URL = srv.URL + "/down"
	if _, err := f.Fetch(context.Background(), s); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("se esperaba HTTP 503: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.csv"), []byte("hola"), 0o600); err != nil {
		t.Fatal(err)
	}
	rc, err = DirFetcher{Dir: dir}.Fetch(context.Background(), testSource("x"))
	if err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	if _, err := (DirFetcher{Dir: dir}).Fetch(context.Background(), testSource("y")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("se esperaba ErrNotFound: %v", err)
	}
}

func TestSnapshotRoundTripAndCorruption(t *testing.T) {
	var e Enc
	e.Prefix(netip.MustParsePrefix("192.0.2.0/24"))
	e.Prefix(netip.MustParsePrefix("2001:db8::/33"))
	e.Prefix(netip.MustParsePrefix("0.0.0.0/0"))
	e.String("feodo")
	ts := time.Date(2026, 10, 8, 4, 0, 0, 0, time.UTC)
	e.Time(ts)
	e.Time(time.Time{})
	e.Uvarint(300)
	meta := SnapshotMeta{Kind: "reputation", PayloadFormat: 1, CreatedAt: ts, Entries: 3,
		Sources: []SourceRef{{ID: "feodo", License: "CC0-1.0", SHA256: "ab"}}}
	var buf bytes.Buffer
	if err := EncodeSnapshot(&buf, meta, e.Bytes()); err != nil {
		t.Fatal(err)
	}
	got, payload, err := DecodeSnapshot(bytes.NewReader(buf.Bytes()), "reputation")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "reputation" || got.Entries != 3 || len(got.Sources) != 1 || !got.CreatedAt.Equal(ts) {
		t.Fatalf("meta = %+v", got)
	}
	d := NewDec(payload)
	if p := d.Prefix(); p.String() != "192.0.2.0/24" {
		t.Fatalf("prefix %v", p)
	}
	if p := d.Prefix(); p.String() != "2001:db8::/33" {
		t.Fatalf("prefix %v", p)
	}
	if p := d.Prefix(); p.String() != "0.0.0.0/0" {
		t.Fatalf("prefix %v", p)
	}
	if s, tt, z, n := d.String(), d.Time(), d.Time(), d.Uvarint(); s != "feodo" || !tt.Equal(ts) || !z.IsZero() || n != 300 || !d.Done() {
		t.Fatalf("decodificado %q %v %v %d err=%v", s, tt, z, n, d.Err())
	}
	d.Uvarint()
	if d.Err() == nil {
		t.Fatal("leer más allá del final debería fallar")
	}

	if _, _, err := DecodeSnapshot(bytes.NewReader(buf.Bytes()), "asn"); !errors.Is(err, ErrCorruptSnapshot) {
		t.Fatalf("tipo distinto: %v", err)
	}
	flipped := bytes.Clone(buf.Bytes())
	flipped[len(flipped)-3] ^= 0xff
	if _, _, err := DecodeSnapshot(bytes.NewReader(flipped), ""); !errors.Is(err, ErrCorruptSnapshot) {
		t.Fatalf("bit cambiado: %v", err)
	}
	if _, _, err := DecodeSnapshot(bytes.NewReader(buf.Bytes()[:30]), ""); !errors.Is(err, ErrCorruptSnapshot) {
		t.Fatalf("truncado: %v", err)
	}
	if _, _, err := DecodeSnapshot(strings.NewReader("PK\x03\x04 no es un snapshot ........................................"), ""); !errors.Is(err, ErrCorruptSnapshot) {
		t.Fatalf("magia: %v", err)
	}
}

func TestSnapshotDirPublish(t *testing.T) {
	d := SnapshotDir{Root: t.TempDir()}
	if _, _, err := d.Latest(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Latest vacío: %v", err)
	}
	for i := 1; i <= 2; i++ {
		m, path, err := d.Publish(SnapshotMeta{Kind: "asn", PayloadFormat: 1}, []byte{byte(i)})
		if err != nil {
			t.Fatal(err)
		}
		if want := "v" + string(rune('0'+i)); m.Version != want {
			t.Fatalf("versión %s, quiero %s", m.Version, want)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		meta, payload, err := DecodeSnapshot(f, "asn")
		_ = f.Close()
		if err != nil || meta.Version != m.Version || payload[0] != byte(i) {
			t.Fatalf("snapshot publicado ilegible: %v %+v", err, meta)
		}
	}
	m, _, err := d.Latest()
	if err != nil || m.Version != "v2" || m.SHA256 == "" {
		t.Fatalf("Latest = %+v %v", m, err)
	}
}

func TestDecompress(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte("hola gzip"))
	_ = zw.Close()
	for in, want := range map[string]string{gz.String(): "hola gzip", "plano": "plano", "": ""} {
		rc, err := Decompress(strings.NewReader(in))
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil || string(b) != want {
			t.Fatalf("Decompress = %q %v, quiero %q", b, err, want)
		}
	}
}
