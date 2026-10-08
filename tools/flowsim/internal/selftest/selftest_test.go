package selftest

import (
	"bytes"
	"context"
	"flag"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "regenera los fixtures de tools/flowsim/fixtures/sim")

func fixturesDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no se pudo localizar el fichero de test")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "fixtures", "sim")
}

func TestAllCases(t *testing.T) {
	cases := DefaultCases()
	if testing.Short() {
		cases = cases[:4]
	}
	for _, c := range cases {
		t.Run(strings.ReplaceAll(c.Name(), " ", "_"), func(t *testing.T) {
			t.Parallel()
			rep, err := RunCase(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if !rep.OK {
				var b bytes.Buffer
				_ = rep.Write(&b)
				t.Fatalf("verificación fallida:\n%s", b.String())
			}
		})
	}
}

func TestFixtures(t *testing.T) {
	dir := fixturesDir(t)
	ctx := context.Background()
	if *update {
		var b bytes.Buffer
		if err := WriteFixtures(ctx, dir, &b); err != nil {
			t.Fatal(err)
		}
		t.Log(b.String())
	}
	var b bytes.Buffer
	ok, err := CheckFixtures(ctx, dir, &b, false)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("fixtures desactualizados o inválidos (regenera con go test ./tools/flowsim/internal/selftest -run TestFixtures -update):\n%s", b.String())
	}
}
