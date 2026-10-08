// Command horus-asn descarga, valida y versiona las fuentes prefijo → ASN →
// organización y compila el snapshot IP→ASN de plataforma (I0-17).
//
//	horus-asn sources [-config f] [-allow-unverified]
//	horus-asn fetch   [-config f] [-data-dir d] [-fixtures dir] [-allow-unverified] [-only id,…]
//	horus-asn build   [-config f] [-data-dir d] [-allow-unverified] [-accept-large-diff]
//	horus-asn sync    (fetch + build, mismas opciones)
//	horus-asn lookup  [-data-dir d | -snapshot f] IP…
//	horus-asn status  [-config f] [-data-dir d]
//
// Datasets crudos en <data-dir>/datasets/<fuente>/ y snapshots en
// <data-dir>/catalog/asn/v<N>/ (docs/storage.md §2.2). Con -fixtures no se usa
// la red (tests/fixtures/datasets/). build no publica si el cambio supera el
// 5 % de los prefijos salvo con -accept-large-diff (sale con código 3).
//
// Pendiente de integrar como subcomando del binario único `horus` (I0-04).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/asn"
	"github.com/hcdestroyer/horus-flow/services/traffic/internal/adapters/asnsources"
	"github.com/hcdestroyer/horus-flow/services/traffic/internal/app/asnbuild"
	"github.com/hcdestroyer/horus-flow/services/traffic/internal/config"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, time.Now)
	stop()
	os.Exit(code)
}

type options struct {
	config, dataDir, fixtures, snapshot, only string
	allowUnverified, acceptLargeDiff          bool
}

const usage = "uso: horus-asn <sources|fetch|build|sync|lookup|status> [opciones]"

var errSomeFailed = errors.New("alguna fuente falló (se conserva su última versión válida)")

func run(ctx context.Context, args []string, stdout, stderr io.Writer, now func() time.Time) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	cmd := args[0]
	fs := flag.NewFlagSet("horus-asn "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	fs.StringVar(&o.config, "config", os.Getenv("HORUS_ASN_DATASETS_CONFIG"), "declaración de fuentes (vacío = embebida)")
	fs.StringVar(&o.dataDir, "data-dir", datasets.DefaultDataDir(), "almacén de archivos de Horus")
	fs.StringVar(&o.fixtures, "fixtures", "", "leer las fuentes de este directorio en lugar de la red")
	fs.StringVar(&o.snapshot, "snapshot", "", "archivo de snapshot para lookup (vacío = último publicado)")
	fs.StringVar(&o.only, "only", "", "procesar solo estas fuentes (ids separados por comas)")
	fs.BoolVar(&o.allowUnverified, "allow-unverified", false, "permitir fuentes con uso comercial sin verificar (solo laboratorio)")
	fs.BoolVar(&o.acceptLargeDiff, "accept-large-diff", false, "publicar aunque cambie más del 5 % de los prefijos")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	var err error
	switch cmd {
	case "sources":
		err = cmdSources(stdout, o)
	case "fetch":
		err = cmdFetch(ctx, stdout, log, o, now)
	case "build":
		err = cmdBuild(stdout, o, now)
	case "sync":
		if err = cmdFetch(ctx, stdout, log, o, now); err == nil || errors.Is(err, errSomeFailed) {
			err = errors.Join(err, cmdBuild(stdout, o, now))
		}
	case "lookup":
		err = cmdLookup(stdout, o, fs.Args())
	case "status":
		err = cmdStatus(stdout, o, now)
	default:
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "horus-asn %s: %v\n", cmd, err)
		if errors.Is(err, asnbuild.ErrNeedsReview) {
			return 3
		}
		return 1
	}
	return 0
}

func loadSources(o options) ([]datasets.Source, error) {
	cfg, err := config.LoadDatasets(o.config)
	if err != nil {
		return nil, err
	}
	srcs := cfg.ByKind(asnbuild.SourceKind)
	for _, s := range srcs {
		// Una fuente que nunca se descarga puede declararse sin parser.
		if asnsources.RoleOf(s.Format) == 0 && s.CommercialUse != datasets.CommercialNo && s.IsEnabled() {
			return nil, fmt.Errorf("fuente %s: formato %q no soportado", s.ID, s.Format)
		}
	}
	if o.only == "" {
		return srcs, nil
	}
	want := map[string]bool{}
	for _, id := range strings.Split(o.only, ",") {
		want[strings.TrimSpace(id)] = true
	}
	var out []datasets.Source
	for _, s := range srcs {
		if want[s.ID] {
			out = append(out, s)
			delete(want, s.ID)
		}
	}
	if len(want) > 0 {
		return nil, fmt.Errorf("fuentes desconocidas en -only: %v", want)
	}
	return out, nil
}

func service(o options, now func() time.Time) *asnbuild.Service {
	var f datasets.Fetcher = datasets.NewHTTPFetcher("horus-flow/"+version+" (+asn datasets)", 15*time.Minute)
	if o.fixtures != "" {
		f = datasets.DirFetcher{Dir: o.fixtures}
	}
	return &asnbuild.Service{
		Store:           &datasets.Store{Root: datasets.DatasetsDir(o.dataDir), Tool: "horus-asn/" + version},
		Fetcher:         f,
		Parse:           asnsources.ParseFile,
		AllowUnverified: o.allowUnverified,
		Now:             now,
	}
}

func cmdSources(w io.Writer, o options) error {
	srcs, err := loadSources(o)
	if err != nil {
		return err
	}
	return datasets.WriteSources(w, srcs, o.allowUnverified)
}

func cmdFetch(ctx context.Context, w io.Writer, log *slog.Logger, o options, now func() time.Time) error {
	srcs, err := loadSources(o)
	if err != nil {
		return err
	}
	results := service(o, now).Fetch(ctx, srcs, log)
	if err := datasets.WriteResults(w, results); err != nil {
		return err
	}
	for _, r := range results {
		if r.Status == datasets.StatusFailed {
			return errSomeFailed
		}
	}
	return nil
}

func readSnapshotFile(path string) (*asn.Snapshot, error) {
	f, err := os.Open(path) //nolint:gosec // ruta del almacén o indicada por el operador
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return asn.ReadSnapshot(f)
}

func cmdBuild(w io.Writer, o options, now func() time.Time) error {
	srcs, err := loadSources(o)
	if err != nil {
		return err
	}
	snap, reports, err := service(o, now).Build(srcs)
	p := &datasets.Printer{W: w}
	for _, r := range reports {
		state := "usada"
		if !r.Used {
			state = "omitida: " + r.Reason
		}
		p.Printf("  %-14s %8d prefijos  %s\n", r.SourceID, r.Routes, state)
	}
	if err != nil {
		return fmt.Errorf("no se publica snapshot; sigue vigente el anterior: %w", err)
	}
	m, path, diff, err := asnbuild.Publish(datasets.SnapshotDir{Root: datasets.CatalogDir(o.dataDir, asn.Kind)},
		snap, o.acceptLargeDiff, readSnapshotFile)
	p.Printf("diff: +%d -%d ~%d sobre %d prefijos (%.1f %%)\n", diff.Added, diff.Removed, diff.Changed, diff.Base, diff.Ratio*100)
	if err != nil {
		return err
	}
	p.Printf("snapshot IP→ASN %s: %d prefijos, %d ASN, %d fuentes, sha256 %s\n  %s\n",
		m.Version, m.Entries, snap.ASCount(), len(m.Sources), m.SHA256, path)
	return p.Err
}

func cmdLookup(w io.Writer, o options, ips []string) error {
	if len(ips) == 0 {
		return errors.New("indica al menos una IP")
	}
	path := o.snapshot
	if path == "" {
		var err error
		if _, path, err = (datasets.SnapshotDir{Root: datasets.CatalogDir(o.dataDir, asn.Kind)}).Latest(); err != nil {
			return err
		}
	}
	snap, err := readSnapshotFile(path)
	if err != nil {
		return err
	}
	p := &datasets.Printer{W: w}
	p.Printf("snapshot %s (%s, %d prefijos)\n", snap.Meta.Version, snap.Meta.CreatedAt.Format(time.RFC3339), snap.Len())
	for _, s := range ips {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return fmt.Errorf("IP inválida %q", s)
		}
		r, ok := snap.Lookup(a)
		if !ok {
			p.Printf("%s\tsin atribución\n", a)
			continue
		}
		p.Printf("%s\t%s\tAS%d país=%s org=%q tipo=%s fuente=%s\n", a, r.Prefix, r.ASN, r.Country, r.AS.Name, r.AS.NetworkType, r.Source)
	}
	return p.Err
}

func cmdStatus(w io.Writer, o options, now func() time.Time) error {
	srcs, err := loadSources(o)
	if err != nil {
		return err
	}
	store := &datasets.Store{Root: datasets.DatasetsDir(o.dataDir)}
	var states []datasets.State
	for _, s := range srcs {
		if ok, _ := s.Allowed(true); !ok {
			continue // nunca se descarga: no tiene antigüedad
		}
		st, err := store.LoadState(s.ID)
		if err != nil {
			return err
		}
		states = append(states, st)
	}
	return datasets.WritePrometheus(w, asnbuild.SourceKind, states, now().UTC())
}
