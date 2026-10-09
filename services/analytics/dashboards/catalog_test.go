package dashboards

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/archtest"
)

// Las copias embebidas no divergen del contrato C9.
func TestContractInSync(t *testing.T) {
	root, err := archtest.FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	for embedded, src := range map[string]string{
		"contract/widget-types.v0.json":    "packages/schemas/dashboard/v0/widget-types.json",
		"contract/templates/noc-isp.json":  "packages/schemas/dashboard/v0/templates/noc-isp.json",
		"contract/templates/security.json": "packages/schemas/dashboard/v0/templates/security.json",
	} {
		got, err := fs.ReadFile(ContractFS(), embedded)
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(root, src))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s diverge de %s: cópielo", embedded, src)
		}
	}
}

// I1-15 criterios 1 y 2: plantillas válidas contra el catálogo y catálogo de I1.
func TestTemplatesValidateAgainstCatalog(t *testing.T) {
	c, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Types) < 15 {
		t.Fatalf("catálogo con %d tipos", len(c.Types))
	}
	for _, typ := range c.Types {
		if typ.RequiredPermission == "" || typ.DataEndpointKind == "" {
			t.Errorf("%s sin required_permission o data_endpoint_kind", typ.Type)
		}
	}
	tpls, err := Templates()
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for i := range tpls {
		if err := validateDocument(c, &tpls[i]); err != nil {
			t.Errorf("plantilla %s: %v", tpls[i].Name, err)
		}
		keys[*tpls[i].TemplateKey] = true
	}
	if !keys["noc_isp"] || !keys["security"] {
		t.Fatalf("plantillas = %v", keys)
	}
}

// I1-15 criterio 3: tipo desconocido o config inválida → 422 con su código.
func TestWidgetValidation(t *testing.T) {
	c, _ := LoadCatalog()
	tpls, _ := Templates()
	d := tpls[0]
	d.Widgets = append([]Widget(nil), d.Widgets...)
	d.Widgets = append(d.Widgets, Widget{ID: "w-x", Type: "no_existe", Position: Position{W: 1, H: 1}, Config: json.RawMessage(`{}`)})
	var ae *apperr.Error
	if err := validateDocument(c, &d); !errors.As(err, &ae) || ae.Code != CodeWidgetTypeUnknown || ae.Status() != 422 {
		t.Fatalf("tipo desconocido: %v", err)
	}
	for _, cfg := range []string{`{"n": 1000}`, `{"direction": "sideways"}`, `{"site_ids": ["no-uuid"]}`, `{"extra": true}`, `[]`} {
		d.Widgets[len(d.Widgets)-1] = Widget{ID: "w-x", Type: "top_customers", Position: Position{W: 4, H: 4}, Config: json.RawMessage(cfg)}
		if err := validateDocument(c, &d); !errors.As(err, &ae) || ae.Code != CodeWidgetConfigInvalid {
			t.Errorf("config %s: %v", cfg, err)
		}
	}
	d.Widgets[len(d.Widgets)-1] = Widget{ID: "w-x", Type: "top_customers", Position: Position{X: 10, W: 4, H: 4}, Config: json.RawMessage(`{"n": 5}`)}
	if err := validateDocument(c, &d); err == nil {
		t.Error("posición fuera de la grilla aceptada")
	}
	d.Widgets[len(d.Widgets)-1].Position.X = 0
	if err := validateDocument(c, &d); err != nil {
		t.Errorf("widget válido rechazado: %v", err)
	}
	if topics := c.Topics([]string{"traffic_now", "findings_feed", "top_customers"}); len(topics) != 2 {
		t.Errorf("topics = %v", topics)
	}
}

func TestPlaylistValidation(t *testing.T) {
	p := &Playlist{Name: "NOC", Transition: "fade", Items: []PlaylistItem{{DurationSeconds: 5}}}
	if validatePlaylist(p) == nil {
		t.Fatal("playlist inválida aceptada")
	}
}
