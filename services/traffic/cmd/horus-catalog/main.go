// Command horus-catalog publica el catálogo semilla de clasificación de
// tráfico (I1-07) como snapshot versionado en un directorio
// (datasets.SnapshotDir, p. ej. store/catalog/catalog) que el ingester
// recarga en caliente (HORUS_CATALOG_SNAPSHOT_DIR).
//
//	horus-catalog -dir store/catalog/catalog [-def catalogo.yaml] [-check]
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/catalog"
)

func main() {
	dir := flag.String("dir", "", "directorio de snapshots del catálogo")
	def := flag.String("def", "", "definición YAML (por defecto, la semilla embebida)")
	check := flag.Bool("check", false, "solo valida la definición")
	flag.Parse()
	if err := run(*dir, *def, *check); err != nil {
		fmt.Fprintln(os.Stderr, "horus-catalog:", err)
		os.Exit(1)
	}
}

func run(dir, def string, check bool) error {
	d, err := catalog.SeedDefinition()
	if err != nil {
		return err
	}
	if def != "" {
		b, err := os.ReadFile(def) //nolint:gosec // ruta de operador
		if err != nil {
			return err
		}
		d = catalog.Definition{}
		if err := yaml.Unmarshal(b, &d); err != nil {
			return err
		}
	}
	c, err := catalog.New(d)
	if err != nil {
		return err
	}
	fmt.Printf("catálogo válido: %d categorías, %d servicios, %d organizaciones, %d rangos\n",
		len(d.Categories), len(d.Services), len(d.Organizations), len(d.Prefixes))
	if check {
		return nil
	}
	if dir == "" {
		return fmt.Errorf("falta -dir")
	}
	m, path, err := c.Publish(datasets.SnapshotDir{Root: dir}, datasets.SnapshotMeta{CreatedAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	fmt.Printf("publicado %s (%s, sha256 %s)\n", m.Version, path, m.SHA256)
	return nil
}
