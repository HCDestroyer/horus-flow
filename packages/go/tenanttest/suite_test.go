package tenanttest

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hcdestroyer/horus-flow/packages/go/archtest"
)

const spec = `
openapi: 3.1.0
paths:
  /sites:
    get: {operationId: listSites, x-scope: tenant, x-module: devices, x-permission: sites.read}
    post: {operationId: createSite, x-scope: tenant}
  /sites/{site_id}:
    get: {operationId: getSite, x-scope: tenant}
    parameters: []
  /me:
    get: {operationId: getMe, x-scope: session}
`

// I0-08 criterio 3: un endpoint nuevo del contrato sin caso declarado hace
// fallar la suite.
func TestCheckDetectsUndeclaredOperation(t *testing.T) {
	t.Parallel()
	ops, err := ParseOperations([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 4 || ops[0].ID != "createSite" || ops[2].Path != "/api/v1/sites/{site_id}" {
		t.Fatalf("ops = %+v", ops)
	}
	if got := ops[2].PathParams(); !slices.Equal(got, []string{"site_id"}) {
		t.Fatalf("params = %v", got)
	}
	cases := map[string]Case{"listSites": {Kind: List}, "createSite": {Kind: Create}, "getSite": {Kind: ByID}}
	if p := Check(ops, cases); len(p) != 0 {
		t.Fatalf("problemas inesperados: %v", p)
	}
	delete(cases, "getSite")
	p := Check(ops, cases)
	if len(p) != 1 || !strings.Contains(p[0], "getSite") || !strings.Contains(p[0], "sin caso de aislamiento") {
		t.Fatalf("no detecta la operación nueva: %v", p)
	}
	cases["getSite"] = Case{Kind: Pending}
	cases["ghost"] = Case{Kind: List}
	if p := Check(ops, cases); len(p) != 2 {
		t.Fatalf("Pending sin responsable y caso huérfano: %v", p)
	}
}

func TestLoadContractBundle(t *testing.T) {
	t.Parallel()
	root, err := archtest.FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	ops, err := LoadOperations(filepath.Join(root, "packages/schemas/openapi/dist/horus-api.v0.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, op := range ops {
		if op.Scope == "tenant" {
			n++
		}
	}
	if n < 50 {
		t.Fatalf("operaciones de ISP en el bundle = %d", n)
	}
}
