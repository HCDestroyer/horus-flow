package datasets

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"
)

// DefaultDataDir devuelve el almacén de archivos de Horus: $HORUS_DATA_DIR o
// /var/lib/horus/store (docs/storage.md §2.1).
func DefaultDataDir() string {
	if d := os.Getenv("HORUS_DATA_DIR"); d != "" {
		return d
	}
	return "/var/lib/horus/store"
}

// DatasetsDir es la carpeta de datasets crudos dentro del almacén.
func DatasetsDir(dataDir string) string { return filepath.Join(dataDir, "datasets") }

// CatalogDir es la carpeta de snapshots publicados de un tipo dentro del
// almacén (store/catalog/<kind>/).
func CatalogDir(dataDir, kind string) string { return filepath.Join(dataDir, "catalog", kind) }

// WriteResults imprime una tabla con el resultado de cada fuente.
func WriteResults(w io.Writer, results []Result) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	p := &Printer{W: tw}
	p.Printf("FUENTE\tESTADO\tENTRADAS\tSHA256\tDETALLE\n")
	for _, r := range results {
		entries, sum, detail := "-", "-", r.Reason
		if r.Manifest != nil {
			entries = fmt.Sprint(r.Manifest.Entries)
			sum = r.Manifest.SHA256[:12]
		}
		if r.Err != nil {
			detail = r.Err.Error()
			if r.Manifest != nil {
				detail += " (se conserva la versión " + r.Manifest.FetchedAt.Format(time.RFC3339) + ")"
			}
		}
		p.Printf("%s\t%s\t%s\t%s\t%s\n", r.SourceID, r.Status, entries, sum, detail)
	}
	if p.Err != nil {
		return p.Err
	}
	return tw.Flush()
}

// WriteSources imprime las fuentes declaradas con su licencia y si se
// descargarían.
func WriteSources(w io.Writer, srcs []Source, allowUnverified bool) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	p := &Printer{W: tw}
	p.Printf("FUENTE\tTIPO\tFORMATO\tUSO COMERCIAL\tFRECUENCIA\tDESCARGA\tLICENCIA\n")
	for _, s := range srcs {
		dl := "sí"
		if ok, reason := s.Allowed(allowUnverified); !ok {
			dl = "no: " + reason
		}
		p.Printf("%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, s.Kind, s.Format, s.CommercialUse,
			time.Duration(s.Frequency), dl, s.License)
	}
	if p.Err != nil {
		return p.Err
	}
	return tw.Flush()
}

// Printer escribe con formato y conserva el primer error de escritura, para
// no comprobar cada línea de una salida de texto.
type Printer struct {
	W   io.Writer
	Err error
}

// Printf escribe si no hubo un error previo.
func (p *Printer) Printf(format string, args ...any) {
	if p.Err == nil {
		_, p.Err = fmt.Fprintf(p.W, format, args...)
	}
}
