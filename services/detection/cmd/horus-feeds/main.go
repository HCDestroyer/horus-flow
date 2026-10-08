// Command horus-feeds descarga, valida y versiona los feeds de reputación de
// plataforma y compila el snapshot de reputación (I0-17).
//
//	horus-feeds sources [-config f] [-allow-unverified]
//	horus-feeds fetch   [-config f] [-data-dir d] [-fixtures dir] [-allow-unverified] [-only id,…]
//	horus-feeds build   [-config f] [-data-dir d] [-allow-unverified]
//	horus-feeds sync    (fetch + build, mismas opciones)
//	horus-feeds lookup  [-data-dir d | -snapshot f] IP…
//	horus-feeds status  [-config f] [-data-dir d]
//
// Datasets crudos en <data-dir>/datasets/<fuente>/ y snapshots en
// <data-dir>/catalog/reputation/v<N>/ (docs/storage.md §2.2). Con -fixtures no
// se usa la red: cada fuente se lee de <dir>/<id>.* (tests/fixtures/feeds/).
//
// Pendiente de integrar como subcomando del binario único `horus` cuando
// exista el registro de módulos (I0-04).
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
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/feeds"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/app/feedsync"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/config"
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
	allowUnverified                           bool
}

const usage = "uso: horus-feeds <sources|fetch|build|sync|lookup|status> [opciones]"

func run(ctx context.Context, args []string, stdout, stderr io.Writer, now func() time.Time) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	cmd := args[0]
	fs := flag.NewFlagSet("horus-feeds "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	fs.StringVar(&o.config, "config", os.Getenv("HORUS_FEEDS_CONFIG"), "declaración de feeds (vacío = embebida)")
	fs.StringVar(&o.dataDir, "data-dir", datasets.DefaultDataDir(), "almacén de archivos de Horus")
	fs.StringVar(&o.fixtures, "fixtures", "", "leer las fuentes de este directorio en lugar de la red")
	fs.StringVar(&o.snapshot, "snapshot", "", "archivo de snapshot para lookup (vacío = último publicado)")
	fs.StringVar(&o.only, "only", "", "procesar solo estas fuentes (ids separados por comas)")
	fs.BoolVar(&o.allowUnverified, "allow-unverified", false, "permitir fuentes con uso comercial sin verificar (solo laboratorio)")
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
			ferr := err
			err = errors.Join(ferr, cmdBuild(stdout, o, now))
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
		_, _ = fmt.Fprintf(stderr, "horus-feeds %s: %v\n", cmd, err)
		return 1
	}
	return 0
}

var errSomeFailed = errors.New("alguna fuente falló (se conserva su última versión válida)")

func loadSources(o options) ([]datasets.Source, error) {
	cfg, err := config.LoadFeeds(o.config)
	if err != nil {
		return nil, err
	}
	srcs := cfg.ByKind(feedsync.SourceKind)
	for _, s := range srcs {
		if !feeds.Supports(s.Format) {
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

func service(o options, now func() time.Time) *feedsync.Service {
	var f datasets.Fetcher = datasets.NewHTTPFetcher("horus-flow/"+version+" (+reputation feeds)", 5*time.Minute)
	if o.fixtures != "" {
		f = datasets.DirFetcher{Dir: o.fixtures}
	}
	return &feedsync.Service{
		Store:           &datasets.Store{Root: datasets.DatasetsDir(o.dataDir), Tool: "horus-feeds/" + version},
		Fetcher:         f,
		Parse:           feeds.EntriesFromFile,
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

func cmdBuild(w io.Writer, o options, now func() time.Time) error {
	srcs, err := loadSources(o)
	if err != nil {
		return err
	}
	snap, reports, err := service(o, now).Compile(srcs)
	p := &datasets.Printer{W: w}
	for _, r := range reports {
		state := "usada"
		if !r.Used {
			state = "omitida: " + r.Reason
		}
		p.Printf("  %-20s %6d entradas  %4d caducadas  %s\n", r.SourceID, r.Entries, r.Expired, state)
	}
	if err != nil {
		return fmt.Errorf("no se publica snapshot; sigue vigente el anterior: %w", err)
	}
	m, path, err := snap.Publish(datasets.SnapshotDir{Root: datasets.CatalogDir(o.dataDir, reputation.Kind)})
	if err != nil {
		return err
	}
	p.Printf("snapshot de reputación %s: %d prefijos, %d fuentes, sha256 %s\n  %s\n",
		m.Version, m.Entries, len(m.Sources), m.SHA256, path)
	return p.Err
}

func openSnapshot(o options) (*reputation.Snapshot, error) {
	path := o.snapshot
	if path == "" {
		var err error
		if _, path, err = (datasets.SnapshotDir{Root: datasets.CatalogDir(o.dataDir, reputation.Kind)}).Latest(); err != nil {
			return nil, err
		}
	}
	f, err := os.Open(path) //nolint:gosec // ruta indicada por el operador
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return reputation.ReadSnapshot(f)
}

func cmdLookup(w io.Writer, o options, ips []string) error {
	if len(ips) == 0 {
		return errors.New("indica al menos una IP")
	}
	snap, err := openSnapshot(o)
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
		hits := snap.Lookup(a)
		if len(hits) == 0 {
			p.Printf("%s\tsin coincidencias\n", a)
			continue
		}
		for _, h := range hits {
			p.Printf("%s\t%s\tfuente=%s categoría=%s confianza=%d fecha=%s", a, h.Prefix, h.Source, h.Category,
				h.Confidence, h.FirstSeen.Format("2006-01-02"))
			if h.Port != 0 {
				p.Printf(" puerto=%d", h.Port)
			}
			if h.Threat != "" {
				p.Printf(" amenaza=%q", h.Threat)
			}
			p.Printf("\n")
		}
	}
	return p.Err
}

func cmdStatus(w io.Writer, o options, now func() time.Time) error {
	srcs, err := loadSources(o)
	if err != nil {
		return err
	}
	store := &datasets.Store{Root: datasets.DatasetsDir(o.dataDir)}
	states := make([]datasets.State, 0, len(srcs))
	for _, s := range srcs {
		st, err := store.LoadState(s.ID)
		if err != nil {
			return err
		}
		states = append(states, st)
	}
	return datasets.WritePrometheus(w, feedsync.SourceKind, states, now().UTC())
}
