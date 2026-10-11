package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

// memRepo es SourceRepo en memoria (mismas restricciones de unicidad y
// versión que la tabla).
type memRepo struct {
	mu   sync.Mutex
	rows map[uuid.UUID]domain.ReputationSource
}

func newMemRepo() *memRepo { return &memRepo{rows: map[uuid.UUID]domain.ReputationSource{}} }

func (m *memRepo) ListReputationSources(context.Context) ([]domain.ReputationSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.ReputationSource
	for _, r := range m.rows {
		out = append(out, r)
	}
	return out, nil
}

func (m *memRepo) GetReputationSource(_ context.Context, id uuid.UUID) (*domain.ReputationSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return nil, domain.ErrSourceNotFound
	}
	return &r, nil
}

func (m *memRepo) InsertReputationSource(_ context.Context, r *domain.ReputationSource) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.rows {
		if x.Key == r.Key || x.DatasetID == r.DatasetID || x.NumericID == r.NumericID {
			return domain.ErrSourceExists
		}
	}
	m.rows[r.ID] = *r
	return nil
}

func (m *memRepo) UpdateReputationSource(_ context.Context, r *domain.ReputationSource, expect int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.rows[r.ID]
	switch {
	case !ok:
		return domain.ErrSourceNotFound
	case cur.Version != expect:
		return domain.ErrSourceStale
	}
	m.rows[r.ID] = *r
	return nil
}

func (m *memRepo) DeleteReputationSource(_ context.Context, id uuid.UUID, expect int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.rows[id]
	switch {
	case !ok:
		return domain.ErrSourceNotFound
	case cur.Version != expect:
		return domain.ErrSourceStale
	}
	delete(m.rows, id)
	return nil
}

func (m *memRepo) SyncCatalog(_ context.Context, catalog []domain.ReputationSource) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range catalog {
		if _, ok := m.rows[r.ID]; !ok {
			m.rows[r.ID] = r
		}
	}
	return nil
}

// memSync es SourceSync en memoria.
type memSync struct {
	mu          sync.Mutex
	catalog     []datasets.Source
	states      map[string]datasets.State
	refreshed   []string
	invalidated int
}

func (s *memSync) Catalog() ([]datasets.Source, error) { return s.catalog, nil }
func (s *memSync) LoadState(id string) (datasets.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[id], nil
}
func (s *memSync) Refresh(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshed = append(s.refreshed, id)
}
func (s *memSync) Pending(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.refreshed {
		if x == id {
			return true
		}
	}
	return false
}
func (s *memSync) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidated++
}

// fakeResolver resuelve nombres fijos.
type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := f[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

var testNow = time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)

func yes() *bool { b := true; return &b }

func testCatalog() []datasets.Source {
	return []datasets.Source{
		{ID: "abusech-feodo", Name: "abuse.ch Feodo Tracker", Kind: "reputation", URL: "https://feodotracker.abuse.ch/downloads/ipblocklist.csv",
			Format: "abusech-feodo-csv", License: "CC0-1.0", CommercialUse: datasets.CommercialYes, Frequency: datasets.Duration(time.Hour),
			Category: "botnet_cc", Confidence: 90, MinEntries: 1, MaxBytes: 16 << 20},
		{ID: "tor-exit", Name: "Tor", Kind: "reputation", URL: "https://check.torproject.org/torbulkexitlist", Format: "netset",
			CommercialUse: datasets.CommercialYes, Frequency: datasets.Duration(6 * time.Hour), Category: "tor", Confidence: 100},
		{ID: "firehol-level1", Name: "FireHOL", Kind: "reputation", URL: "https://iplists.firehol.org/files/firehol_level1.netset",
			Format: "netset", CommercialUse: datasets.CommercialNo, Frequency: datasets.Duration(24 * time.Hour), Category: "blocklist"},
	}
}

type fixture struct {
	svc    *ReputationSources
	repo   *memRepo
	sync   *memSync
	audits []authapi.AuditEntry
	ctx    context.Context
}

func newFixture(t *testing.T, sealer *envelope.Sealer) *fixture {
	t.Helper()
	f := &fixture{repo: newMemRepo(), sync: &memSync{catalog: testCatalog(), states: map[string]datasets.State{}}}
	f.svc = NewReputationSources(SourcesOptions{Repo: f.repo, Sync: f.sync, Sealer: sealer,
		Guard: &datasets.EgressGuard{Resolver: fakeResolver{
			"feeds.example.net": {netip.MustParseAddr("203.0.113.10")},
			"intranet.example":  {netip.MustParseAddr("10.1.2.3")},
		}},
		Audit: func(_ context.Context, e authapi.AuditEntry) { f.audits = append(f.audits, e) },
		Now:   func() time.Time { return testNow }})
	f.ctx = authz.WithPrincipal(context.Background(), &authz.Principal{Type: authz.TypeUser, Subject: "01926b3e-1111-7000-8000-000000000001",
		Scope: authz.ScopePlatform, Perms: map[string][]string{PermReputationSources: {"*"}}})
	return f
}

func validCreate() SourceCreate {
	conf := 70
	return SourceCreate{Key: "csirt_scanners", Name: "CSIRT · escáneres", URL: "https://feeds.example.net/scanners.netset",
		Format: "netset", Frequency: "6h", Category: "scanner", Confidence: &conf, TermsAcknowledged: yes()}
}

func code(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Code
	}
	return ""
}

func TestCatalogSeededAndListed(t *testing.T) {
	f := newFixture(t, nil)
	f.sync.states["abusech-feodo"] = datasets.State{LastAttempt: testNow, LastSuccess: testNow, Current: &datasets.Manifest{Entries: 412}}
	list, err := f.svc.List(f.ctx, SourceFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("catálogo: %d fuentes (FireHOL no debe aparecer): %+v", len(list), list)
	}
	byKey := map[string]SourceDoc{}
	for _, d := range list {
		byKey[d.Key] = d
	}
	feodo, tor := byKey["abusech_feodo"], byKey["tor_exit"]
	if feodo.Origin != "catalog" || feodo.CommercialUse != "approved" || feodo.Status != "ok" || feodo.Entries != 412 ||
		feodo.Frequency != "1h" || feodo.NumericID != int(reputation.SourceID("abusech-feodo")) || feodo.CreatedBy != nil {
		t.Fatalf("feodo: %+v", feodo)
	}
	if tor.Category != "tor_exit" || tor.Status != "pending" {
		t.Fatalf("tor: %+v", tor)
	}
	// Mismo UUID en cada instalación y entre arranques.
	if row, _ := catalogRow(testCatalog()[0], testNow); row.ID != feodo.ID {
		t.Fatal("UUID del catálogo no determinista")
	}
	off := false
	if l, _ := f.svc.List(f.ctx, SourceFilter{Origin: "custom"}); len(l) != 0 {
		t.Fatalf("filtro origin: %d", len(l))
	}
	if l, _ := f.svc.List(f.ctx, SourceFilter{Enabled: &off}); len(l) != 0 {
		t.Fatalf("filtro enabled: %d", len(l))
	}
}

func TestPlatformPermissionRequired(t *testing.T) {
	f := newFixture(t, nil)
	tenantCtx := authz.WithPrincipal(context.Background(), &authz.Principal{Type: authz.TypeUser, Subject: "u", Scope: authz.ScopeTenant,
		Perms: map[string][]string{PermReputationSources: {"*"}}})
	if _, err := f.svc.List(tenantCtx, SourceFilter{}); code(err) != problem.CodeTokenScopeInvalid {
		t.Fatalf("token de ISP: %v", err)
	}
	noPerm := authz.WithPrincipal(context.Background(), &authz.Principal{Type: authz.TypeUser, Subject: "u", Scope: authz.ScopePlatform})
	if _, err := f.svc.List(noPerm, SourceFilter{}); code(err) != problem.CodePermissionDenied {
		t.Fatalf("sin permiso: %v", err)
	}
}

func TestCreateCustomSourceWithWriteOnlyAuth(t *testing.T) {
	sealer := envelope.Ephemeral()
	f := newFixture(t, sealer)
	in := validCreate()
	name, secret := "Auth-Key", "s3cr3t-value"
	in.AuthHeaderName, in.AuthHeaderValue = &name, &secret
	doc, err := f.svc.Create(f.ctx, in, AuditMeta{IP: "198.51.100.7"})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Origin != "custom" || doc.CommercialUse != "operator_acknowledged" || !doc.HasAuth || doc.Status != "pending" ||
		doc.Version != 1 || doc.CreatedBy == nil || doc.NumericID != int(reputation.SourceID("custom-csirt_scanners")) {
		t.Fatalf("doc: %+v", doc)
	}
	b, _ := json.Marshal(doc)
	if strings.Contains(string(b), secret) || strings.Contains(string(b), "auth_header_value") {
		t.Fatalf("el secreto sale en la respuesta: %s", b)
	}
	row := f.repo.rows[doc.ID]
	if row.AuthSecret == nil || strings.Contains(string(row.AuthSecret.Ciphertext), secret) || row.AuthSecret.KEKID != sealer.KEKID() {
		t.Fatalf("secreto sin cifrar: %+v", row.AuthSecret)
	}
	if len(f.sync.refreshed) != 1 || f.sync.refreshed[0] != "custom-csirt_scanners" {
		t.Fatalf("primera carga no encolada: %v", f.sync.refreshed)
	}
	if len(f.audits) != 1 || f.audits[0].Action != AuditSourceCreated || f.audits[0].Scope != "platform" ||
		f.audits[0].ResourceID != doc.ID.String() || f.audits[0].IP != "198.51.100.7" {
		t.Fatalf("auditoría: %+v", f.audits)
	}
	ab, _ := json.Marshal(f.audits[0].Changes)
	if strings.Contains(string(ab), secret) {
		t.Fatal("el secreto sale en la auditoría")
	}
	// feedsync recibe la fuente con la cabecera descifrada (solo en memoria).
	specs, err := f.svc.CustomSources(context.Background())
	if err != nil || len(specs) != 1 {
		t.Fatalf("specs: %v %v", specs, err)
	}
	if specs[0].ID != "custom-csirt_scanners" || specs[0].Header.Get("Auth-Key") != secret || specs[0].Category != "scanner" {
		t.Fatalf("spec: %+v", specs[0])
	}
	enabled, _ := f.svc.CatalogEnabled(context.Background())
	if len(enabled) != 2 || !enabled["abusech-feodo"] {
		t.Fatalf("catálogo: %v", enabled)
	}
	// Clave repetida → 409.
	if _, err := f.svc.Create(f.ctx, validCreate(), AuditMeta{}); code(err) != problem.CodeAlreadyExists {
		t.Fatalf("duplicada: %v", err)
	}
	// La clave de una fuente del catálogo tampoco se puede reutilizar.
	dup := validCreate()
	dup.Key = "tor_exit"
	if _, err := f.svc.Create(f.ctx, dup, AuditMeta{}); code(err) != problem.CodeAlreadyExists {
		t.Fatalf("clave del catálogo: %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	f := newFixture(t, nil)
	cases := map[string]struct {
		mut  func(*SourceCreate)
		code string
	}{
		"clave":            {func(c *SourceCreate) { c.Key = "Bad-Key" }, problem.CodeValidationFailed},
		"términos":         {func(c *SourceCreate) { c.TermsAcknowledged = nil }, problem.CodeValidationFailed},
		"frecuencia corta": {func(c *SourceCreate) { c.Frequency = "5m" }, problem.CodeValidationFailed},
		"frecuencia larga": {func(c *SourceCreate) { c.Frequency = "8d" }, problem.CodeValidationFailed},
		"formato":          {func(c *SourceCreate) { c.Format = "csv" }, problem.CodeValidationFailed},
		"categoría":        {func(c *SourceCreate) { c.Category = "tor" }, problem.CodeValidationFailed},
		"http":             {func(c *SourceCreate) { c.URL = "http://feeds.example.net/x" }, problem.CodeValidationFailed},
		"ip privada":       {func(c *SourceCreate) { c.URL = "https://10.0.0.5/list" }, CodeSourceURLNotAllowed},
		"cgnat":            {func(c *SourceCreate) { c.URL = "https://100.64.1.1/list" }, CodeSourceURLNotAllowed},
		"localhost":        {func(c *SourceCreate) { c.URL = "https://localhost/list" }, CodeSourceURLNotAllowed},
		"credenciales":     {func(c *SourceCreate) { c.URL = "https://u:p@feeds.example.net/x" }, CodeSourceURLNotAllowed},
		"dns privado":      {func(c *SourceCreate) { c.URL = "https://intranet.example/list" }, CodeSourceURLNotAllowed},
		"auth sin valor": {func(c *SourceCreate) {
			n := "Auth-Key"
			c.AuthHeaderName = &n
		}, problem.CodeValidationFailed},
		// Sin KEK no se guardan credenciales.
		"auth sin kek": {func(c *SourceCreate) {
			n, v := "Auth-Key", "x"
			c.AuthHeaderName, c.AuthHeaderValue = &n, &v
		}, problem.CodeValidationFailed},
	}
	for name, tc := range cases {
		in := validCreate()
		tc.mut(&in)
		if _, err := f.svc.Create(f.ctx, in, AuditMeta{}); code(err) != tc.code {
			t.Errorf("%s: %v (quiero %s)", name, err, tc.code)
		}
	}
	if len(f.repo.rows) != 2 || len(f.audits) != 0 {
		t.Fatalf("se guardó algo inválido: %d filas, %d auditorías", len(f.repo.rows), len(f.audits))
	}
	// Un nombre que hoy no resuelve no se rechaza (lo comprueba el descargador).
	in := validCreate()
	in.URL = "https://unknown.example.org/list"
	if _, err := f.svc.Create(f.ctx, in, AuditMeta{}); err != nil {
		t.Fatalf("sin DNS: %v", err)
	}
}

func catalogID(t *testing.T, f *fixture, key string) SourceDoc {
	t.Helper()
	list, err := f.svc.List(f.ctx, SourceFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range list {
		if d.Key == key {
			return d
		}
	}
	t.Fatalf("no está %s", key)
	return SourceDoc{}
}

func patch(t *testing.T, s string) SourcePatch {
	t.Helper()
	var p SourcePatch
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCatalogOnlyEnabledAndNotDeletable(t *testing.T) {
	f := newFixture(t, nil)
	feodo := catalogID(t, f, "abusech_feodo")
	if _, err := f.svc.Update(f.ctx, feodo.ID, feodo.Version, patch(t, `{"name":"otro"}`), AuditMeta{}); code(err) != CodeSourceReadOnly {
		t.Fatalf("editar catálogo: %v", err)
	}
	if err := f.svc.Delete(f.ctx, feodo.ID, feodo.Version, AuditMeta{}); code(err) != CodeSourceReadOnly {
		t.Fatalf("borrar catálogo: %v", err)
	}
	if _, err := f.svc.Update(f.ctx, feodo.ID, feodo.Version+1, patch(t, `{"enabled":false}`), AuditMeta{}); code(err) != problem.CodePreconditionFailed {
		t.Fatalf("If-Match: %v", err)
	}
	doc, err := f.svc.Update(f.ctx, feodo.ID, feodo.Version, patch(t, `{"enabled":false}`), AuditMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Enabled || doc.Status != "disabled" || doc.Version != feodo.Version+1 || f.sync.invalidated != 1 {
		t.Fatalf("desactivar: %+v (invalidated %d)", doc, f.sync.invalidated)
	}
	if en, _ := f.svc.CatalogEnabled(context.Background()); en["abusech-feodo"] {
		t.Fatal("feedsync seguiría descargándola")
	}
	if _, err := f.svc.Refresh(f.ctx, feodo.ID, AuditMeta{}); code(err) != problem.CodeValidationFailed {
		t.Fatalf("recargar desactivada: %v", err)
	}
	doc, err = f.svc.Update(f.ctx, feodo.ID, doc.Version, patch(t, `{"enabled":true}`), AuditMeta{})
	if err != nil || !doc.Enabled || len(f.sync.refreshed) != 1 {
		t.Fatalf("reactivar: %+v %v %v", doc, err, f.sync.refreshed)
	}
	if len(f.audits) != 2 || f.audits[0].Action != AuditSourceUpdated {
		t.Fatalf("auditoría: %+v", f.audits)
	}
}

func TestUpdateDeleteRefreshCustom(t *testing.T) {
	f := newFixture(t, envelope.Ephemeral())
	doc, err := f.svc.Create(f.ctx, validCreate(), AuditMeta{})
	if err != nil {
		t.Fatal(err)
	}
	f.sync.refreshed = nil
	// Confianza: sin descarga nueva, solo recompilar.
	doc, err = f.svc.Update(f.ctx, doc.ID, doc.Version, patch(t, `{"confidence":55,"license":"CC-BY","notes":null}`), AuditMeta{})
	if err != nil || doc.Confidence != 55 || doc.License == nil || *doc.License != "CC-BY" || doc.Version != 2 || len(f.sync.refreshed) != 0 {
		t.Fatalf("patch: %+v %v %v", doc, err, f.sync.refreshed)
	}
	// URL nueva → carga encolada; a una red privada → 422.
	if _, err := f.svc.Update(f.ctx, doc.ID, doc.Version, patch(t, `{"url":"https://192.168.1.1/x"}`), AuditMeta{}); code(err) != CodeSourceURLNotAllowed {
		t.Fatalf("url privada: %v", err)
	}
	doc, err = f.svc.Update(f.ctx, doc.ID, doc.Version, patch(t, `{"url":"https://feeds.example.net/v2.netset"}`), AuditMeta{})
	if err != nil || len(f.sync.refreshed) != 1 {
		t.Fatalf("url: %v %v", err, f.sync.refreshed)
	}
	// Autenticación: alta, cambio de nombre conservando el valor y baja.
	doc, err = f.svc.Update(f.ctx, doc.ID, doc.Version, patch(t, `{"auth_header_name":"X-Key","auth_header_value":"v1"}`), AuditMeta{})
	if err != nil || !doc.HasAuth {
		t.Fatalf("auth: %+v %v", doc, err)
	}
	doc, err = f.svc.Update(f.ctx, doc.ID, doc.Version, patch(t, `{"auth_header_name":"Auth-Key"}`), AuditMeta{})
	if err != nil || !doc.HasAuth {
		t.Fatalf("renombrar cabecera: %+v %v", doc, err)
	}
	specs, _ := f.svc.CustomSources(context.Background())
	if specs[0].Header.Get("Auth-Key") != "v1" {
		t.Fatalf("cabecera: %v", specs[0].Header)
	}
	doc, err = f.svc.Update(f.ctx, doc.ID, doc.Version, patch(t, `{"auth_header_value":null}`), AuditMeta{})
	if err != nil || doc.HasAuth {
		t.Fatalf("quitar auth: %+v %v", doc, err)
	}
	// Campos desconocidos o inmutables.
	if _, err := f.svc.Update(f.ctx, doc.ID, doc.Version, patch(t, `{"key":"otra"}`), AuditMeta{}); code(err) != problem.CodeValidationFailed {
		t.Fatalf("key inmutable: %v", err)
	}
	if _, err := f.svc.Update(f.ctx, doc.ID, doc.Version, patch(t, `{"frequency":"1m"}`), AuditMeta{}); code(err) != problem.CodeValidationFailed {
		t.Fatalf("frecuencia: %v", err)
	}
	// Estado de carga reflejado.
	f.sync.refreshed = nil
	f.sync.states["custom-csirt_scanners"] = datasets.State{LastAttempt: testNow, ConsecutiveFailures: 3, LastError: "descargar: HTTP 503",
		LastSuccess: testNow.Add(-time.Hour), Current: &datasets.Manifest{Entries: 2215}}
	got, err := f.svc.Get(f.ctx, doc.ID)
	if err != nil || got.Status != "failing" || got.Entries != 2215 || got.LastError == nil || got.ConsecutiveFailures != 3 {
		t.Fatalf("estado: %+v %v", got, err)
	}
	rd, err := f.svc.Refresh(f.ctx, doc.ID, AuditMeta{})
	if err != nil || rd.Status != "pending" || len(f.sync.refreshed) != 1 {
		t.Fatalf("refresh: %+v %v", rd, err)
	}
	// Borrado con If-Match.
	if err := f.svc.Delete(f.ctx, doc.ID, doc.Version-1, AuditMeta{}); code(err) != problem.CodePreconditionFailed {
		t.Fatalf("delete If-Match: %v", err)
	}
	inv := f.sync.invalidated
	if err := f.svc.Delete(f.ctx, doc.ID, doc.Version, AuditMeta{}); err != nil {
		t.Fatal(err)
	}
	if f.sync.invalidated != inv+1 {
		t.Fatal("el snapshot no se recompila tras borrar")
	}
	if _, err := f.svc.Get(f.ctx, doc.ID); code(err) != CodeSourceNotFound {
		t.Fatalf("borrada: %v", err)
	}
	if _, err := f.svc.Refresh(f.ctx, uuid.New(), AuditMeta{}); code(err) != CodeSourceNotFound {
		t.Fatalf("refresh inexistente: %v", err)
	}
}

func TestFrequencyFormat(t *testing.T) {
	for d, want := range map[time.Duration]string{time.Hour: "1h", 12 * time.Hour: "12h", 168 * time.Hour: "7d", 15 * time.Minute: "15m",
		48 * time.Hour: "2d", 90 * time.Minute: "90m"} {
		if got := FormatFrequency(d); got != want {
			t.Errorf("%s → %s, quiero %s", d, got, want)
		}
		if back, ok := ParseFrequency(want); !ok || back != d {
			t.Errorf("%s → %s", want, back)
		}
	}
	for _, bad := range []string{"", "0h", "1w", "h", "01h"} {
		if _, ok := ParseFrequency(bad); ok {
			t.Errorf("%q aceptada", bad)
		}
	}
}
