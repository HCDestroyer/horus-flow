package customfeeds

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
)

func good() SourceSpec {
	return SourceSpec{
		ID: "custom-honeypot", Name: "Honeypot SSH", URL: "https://honeypot.example.net/ssh.txt",
		Format: "ip-list", Category: "scanner", Confidence: 70,
		Frequency: datasets.Duration(time.Hour), TTL: datasets.Duration(72 * time.Hour),
	}
}

func TestValidateGood(t *testing.T) {
	pol := DefaultPolicy()
	csv := good()
	csv.ID, csv.Format = "custom-soc_scanners", "csv"
	csv.CSV = &datasets.CSVOptions{Column: "ip_address", Delimiter: ";"}
	csv.OnDangerous = "warn"
	csv.MaxBytes, csv.MaxEntries, csv.MinEntries = 1<<20, 5000, 10
	csv.LicenseURL = "https://soc.example.net/terms"
	port := good()
	port.URL = "https://203.0.113.5:8443/list?token=abc"
	drop := good()
	drop.Format, drop.Category, drop.Frequency, drop.TTL = "spamhaus-drop-json", "blocklist", datasets.Duration(7*24*time.Hour), 0
	for _, s := range []SourceSpec{good(), csv, port, drop} {
		if err := Validate(s, pol); err != nil {
			t.Errorf("%s: %v", s.ID, err)
		}
	}
}

func TestValidateBad(t *testing.T) {
	pol := DefaultPolicy()
	cases := map[string]func(*SourceSpec){
		"id":           func(s *SourceSpec) { s.ID = "honeypot" }, // sin prefijo custom-
		"id ":          func(s *SourceSpec) { s.ID = "custom-Mayús" },
		"name":         func(s *SourceSpec) { s.Name = " " },
		"name ":        func(s *SourceSpec) { s.Name = strings.Repeat("x", 121) },
		"name  ":       func(s *SourceSpec) { s.Name = "a\nb" },
		"url":          func(s *SourceSpec) { s.URL = "http://honeypot.example.net/ssh.txt" },
		"url ":         func(s *SourceSpec) { s.URL = "ftp://example.net/x" },
		"url  ":        func(s *SourceSpec) { s.URL = "https://user:pass@example.net/x" },
		"url   ":       func(s *SourceSpec) { s.URL = "https://localhost/x" },
		"url    ":      func(s *SourceSpec) { s.URL = "https://10.0.0.5/x" },
		"url     ":     func(s *SourceSpec) { s.URL = "https://[fd00::1]/x" },
		"url      ":    func(s *SourceSpec) { s.URL = "https://169.254.169.254/latest/meta-data" },
		"url       ":   func(s *SourceSpec) { s.URL = "https://feeds.internal/x" },
		"url        ":  func(s *SourceSpec) { s.URL = "https://intranet/x" },
		"url         ": func(s *SourceSpec) { s.URL = "https://example.net/x#frag" },
		"url ⁰":        func(s *SourceSpec) { s.URL = "" },
		"url ¹":        func(s *SourceSpec) { s.URL = "https://example.net/" + strings.Repeat("a", 2048) },
		"format":       func(s *SourceSpec) { s.Format = "stix" },
		"csv":          func(s *SourceSpec) { s.Format = "csv" },                           // sin opciones csv
		"csv ":         func(s *SourceSpec) { s.CSV = &datasets.CSVOptions{Column: "ip"} }, // csv con ip-list
		"csv  ":        func(s *SourceSpec) { s.Format, s.CSV = "csv", &datasets.CSVOptions{Column: "0"} },
		"category":     func(s *SourceSpec) { s.Category = "evil" },
		"confidence":   func(s *SourceSpec) { s.Confidence = 0 },
		"confidence ":  func(s *SourceSpec) { s.Confidence = 101 },
		"frequency":    func(s *SourceSpec) { s.Frequency = datasets.Duration(5 * time.Minute) },
		"frequency ":   func(s *SourceSpec) { s.Frequency, s.TTL = datasets.Duration(30*24*time.Hour), 0 },
		"ttl":          func(s *SourceSpec) { s.TTL = datasets.Duration(30 * time.Minute) },
		"ttl ":         func(s *SourceSpec) { s.TTL = datasets.Duration(365 * 24 * time.Hour) },
		"max_bytes":    func(s *SourceSpec) { s.MaxBytes = 1 << 30 },
		"max_bytes ":   func(s *SourceSpec) { s.MaxBytes = 100 },
		"max_entries":  func(s *SourceSpec) { s.MaxEntries = 10_000_000 },
		"min_entries":  func(s *SourceSpec) { s.MinEntries = 300_000 }, // > max_entries por defecto
		"min_entries ": func(s *SourceSpec) { s.MinEntries = -1 },
		"on_dangerous": func(s *SourceSpec) { s.OnDangerous = "ignore" },
		"license_url":  func(s *SourceSpec) { s.LicenseURL = "javascript:alert(1)" },
		"notes":        func(s *SourceSpec) { s.Notes = strings.Repeat("x", 2001) },
	}
	for name, mutate := range cases {
		s := good()
		mutate(&s)
		err := Validate(s, pol)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%q: se esperaba ValidationError, error %v", name, err)
			continue
		}
		field := strings.Fields(name)[0]
		if len(ve.Problems) != 1 || ve.Problems[0].Field != field {
			t.Errorf("%q: problemas %+v", name, ve.Problems)
		}
	}
	// Varios problemas a la vez se informan juntos.
	err := Validate(SourceSpec{}, pol)
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Problems) < 6 {
		t.Fatalf("spec vacía: %v", err)
	}
}

func TestToSource(t *testing.T) {
	pol := DefaultPolicy()
	src := ToSource(good(), pol)
	if src.Kind != SourceKind || !src.IsCustom() || src.CommercialUse != datasets.CommercialYes ||
		src.License != DefaultLicense || src.LicenseURL != good().URL || src.MaxBytes != pol.DefaultMaxBytes ||
		src.MaxEntries != pol.DefaultMaxEntries || src.MinEntries != 1 || src.OnDangerous != datasets.DangerReject {
		t.Fatalf("ToSource = %+v", src)
	}
	if err := src.Validate(); err != nil {
		t.Fatalf("la fuente convertida no pasa la validación del catálogo: %v", err)
	}
	if ok, _ := src.Allowed(false); !ok {
		t.Fatal("una lista personalizada válida debe poder descargarse sin -allow-unverified")
	}
}

func fixturesDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "tests", "fixtures", "feeds")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no se encontró la raíz del repositorio")
		}
		dir = parent
	}
}

func TestProviders(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(fixturesDir(t), "custom")
	specs, err := Dir{Path: dir}.CustomSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 || specs[0].ID != "custom-honeypot" || specs[1].ID != "custom-scanners" ||
		specs[1].CSV == nil || specs[1].CSV.Delimiter != ";" || specs[0].CreatedBy == "" {
		t.Fatalf("Dir = %+v", specs)
	}
	for _, s := range specs {
		if err := Validate(s, DefaultPolicy()); err != nil {
			t.Errorf("fixture %s: %v", s.ID, err)
		}
	}
	p, err := PathProvider(filepath.Join(dir, "soc.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if specs, err := p.CustomSources(ctx); err != nil || len(specs) != 1 {
		t.Fatalf("File: %v %+v", err, specs)
	}
	if specs, err := (Dir{Path: filepath.Join(dir, "no-existe")}).CustomSources(ctx); err != nil || specs != nil {
		t.Fatalf("directorio inexistente: %v %v", err, specs)
	}
	called := false
	fn := ProviderFunc(func(context.Context) ([]SourceSpec, error) { called = true; return []SourceSpec{good()}, nil })
	if specs, err := (Multi{fn, Static{good()}}).CustomSources(ctx); err != nil || len(specs) != 2 || !called {
		t.Fatalf("Multi: %v %+v", err, specs)
	}
	boom := ProviderFunc(func(context.Context) ([]SourceSpec, error) { return nil, errors.New("postgres caído") })
	if _, err := (Multi{Static{good()}, boom}).CustomSources(ctx); err == nil {
		t.Fatal("Multi debería propagar el error")
	}

	// Campos desconocidos (errata) y versiones no soportadas.
	for _, doc := range []string{
		"id: custom-x\nnmae: typo\n",
		"version: 2\nsources: []\n",
		"version: 1\nsources:\n  - id: custom-x\n    colour: red\n",
		"[1, 2",
	} {
		if _, err := ParseYAML([]byte(doc)); err == nil {
			t.Errorf("ParseYAML(%q) debería fallar", doc)
		}
	}
	if specs, err := ParseYAML([]byte("# vacío\n")); err != nil || specs != nil {
		t.Fatalf("documento vacío: %v %v", err, specs)
	}
}

func TestResolve(t *testing.T) {
	ctx := context.Background()
	catalog := []datasets.Source{{ID: "tor-exit", Kind: SourceKind}}
	bad := good()
	bad.ID, bad.URL = "custom-bad", "http://example.net/x"
	dup := good()
	collide := good()
	collide.ID = "custom-tor"
	prov := Static{good(), bad, dup, collide}

	all, rejected, err := Resolve(ctx, catalog, prov, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ID != "tor-exit" || all[1].ID != "custom-honeypot" || all[2].ID != "custom-tor" || !all[1].IsCustom() {
		t.Fatalf("all = %+v", all)
	}
	if len(rejected) != 2 || rejected[0].ID != "custom-bad" || rejected[1].ID != "custom-honeypot" ||
		!strings.Contains(rejected[1].Err.Error(), "duplicado") {
		t.Fatalf("rejected = %+v", rejected)
	}
	// El catálogo no puede usar el prefijo reservado.
	if _, _, err := Resolve(ctx, []datasets.Source{{ID: "custom-x"}}, nil, DefaultPolicy()); err == nil {
		t.Fatal("prefijo reservado en el catálogo")
	}
	// Si el proveedor falla, no se devuelve una lista parcial.
	boom := ProviderFunc(func(context.Context) ([]SourceSpec, error) { return nil, errors.New("postgres caído") })
	if _, _, err := Resolve(ctx, catalog, boom, DefaultPolicy()); err == nil {
		t.Fatal("error del proveedor")
	}
}

func TestSpecJSON(t *testing.T) {
	b, err := json.Marshal(good())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"frequency":"1h0m0s"`) || strings.Contains(string(b), "updated_at") {
		t.Fatalf("json: %s", b)
	}
	var back SourceSpec
	if err := json.Unmarshal([]byte(`{"id":"custom-x","frequency":"6h","ttl":"24h"}`), &back); err != nil ||
		time.Duration(back.Frequency) != 6*time.Hour || time.Duration(back.TTL) != 24*time.Hour {
		t.Fatalf("unmarshal: %v %+v", err, back)
	}
	if err := json.Unmarshal([]byte(`{"frequency":"pronto"}`), &back); err == nil {
		t.Fatal("duración inválida")
	}
}
