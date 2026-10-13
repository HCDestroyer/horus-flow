// Package traffic es el módulo de el rol traffic de `horus`
// (ADR-0025, docs/services.md): mantiene el dataset de plataforma prefijo → ASN →
// organización. Al arrancar (si no hay snapshot o está viejo) y después cada
// HORUS_ASN_SYNC_INTERVAL descarga las fuentes (iptoasn, RIR; datasets.yaml) y
// publica el snapshot IP→ASN en <HORUS_DATA_DIR>/catalog/asn, que el ingester
// usa para el ASN y la organización remotos de cada flujo y analytics para los
// nombres de ASN. Mismo proceso que `horus-asn sync`. Un fallo deja vigente el
// snapshot anterior y se reintenta en la siguiente vuelta.
package traffic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/asn"
	"github.com/hcdestroyer/horus-flow/services/traffic/internal/adapters/asnsources"
	"github.com/hcdestroyer/horus-flow/services/traffic/internal/app/asnbuild"
	trafficcfg "github.com/hcdestroyer/horus-flow/services/traffic/internal/config"
)

// Role es el nombre del rol en HORUS_ROLES: catálogo de clasificación de tráfico y su snapshot.
const Role = "traffic"

// Config del rol.
type Config struct {
	// DataDir es el almacén de archivos (datasets crudos y snapshots publicados).
	DataDir string `env:"HORUS_DATA_DIR" envDefault:"/var/lib/horus/store"`
	// ASNSync activa la descarga del dataset (necesita salida HTTPS a las fuentes).
	ASNSync bool `env:"HORUS_ASN_SYNC" envDefault:"true"`
	// ASNSyncInterval: cada cuánto se renueva (las fuentes se publican a diario o semanalmente).
	ASNSyncInterval time.Duration `env:"HORUS_ASN_SYNC_INTERVAL" envDefault:"168h"`
	// ASNDatasetsConfig: archivo de fuentes; vacío = el embebido (internal/config/datasets.yaml).
	ASNDatasetsConfig string `env:"HORUS_ASN_DATASETS_CONFIG"`
	// ASNSources: ids de fuente a usar ("all" = todas). Por defecto sin la tabla BGP de RIPE RIS
	// (cientos de MB y mucha memoria al procesarla): iptoasn ya deriva de BGP y basta en un
	// servidor pequeño.
	ASNSources []string `env:"HORUS_ASN_SOURCES" envDefault:"iptoasn-v4,iptoasn-v6,rir-afrinic,rir-apnic,rir-arin,rir-lacnic,rir-ripencc,peeringdb"`
}

type mod struct {
	cfg     Config
	log     *slog.Logger
	now     func() time.Time
	fetcher datasets.Fetcher
	sync    func(ctx context.Context) error
}

// Register construye el módulo del rol traffic (firma module.Factory).
func Register(_ context.Context, deps module.Deps) (module.Module, error) {
	cfg, err := config.Load[Config](deps.Environ)
	if err != nil {
		return nil, fmt.Errorf("%s config: %w", Role, err)
	}
	log := deps.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if !cfg.ASNSync {
		log.Info("traffic: ASN dataset sync disabled (HORUS_ASN_SYNC=false)")
		return module.Idle(), nil
	}
	m := &mod{cfg: cfg, log: log, now: time.Now,
		fetcher: datasets.NewHTTPFetcher("horus-flow (+asn datasets)", 15*time.Minute)}
	m.sync = m.syncASN
	return m, nil
}

// Run renueva el dataset cuando falta o caduca; comprueba cada hora.
func (m *mod) Run(ctx context.Context) error {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if m.due() {
			if err := m.sync(ctx); err != nil && ctx.Err() == nil {
				m.log.WarnContext(ctx, "traffic: ASN dataset not refreshed (previous snapshot stays)", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// due: no hay snapshot publicado o es más viejo que el intervalo.
func (m *mod) due() bool {
	man, _, err := m.snapshots().Latest()
	if err != nil || man == nil {
		return true
	}
	return m.now().Sub(man.CreatedAt) >= m.cfg.ASNSyncInterval
}

func (m *mod) snapshots() datasets.SnapshotDir {
	return datasets.SnapshotDir{Root: datasets.CatalogDir(m.cfg.DataDir, asn.Kind)}
}

// syncASN = horus-asn sync: descarga las fuentes y publica el snapshot.
func (m *mod) syncASN(ctx context.Context) error {
	cfg, err := trafficcfg.LoadDatasets(m.cfg.ASNDatasetsConfig)
	if err != nil {
		return err
	}
	cfg.Sources = m.selected(cfg.Sources)
	if len(cfg.Sources) == 0 {
		return errors.New("HORUS_ASN_SOURCES no coincide con ninguna fuente")
	}
	svc := &asnbuild.Service{
		Store:   &datasets.Store{Root: datasets.DatasetsDir(m.cfg.DataDir), Tool: "horus/" + Role},
		Fetcher: m.fetcher,
		Parse:   asnsources.ParseFile,
		Now:     m.now,
	}
	m.log.InfoContext(ctx, "traffic: refreshing ASN dataset", "sources", len(cfg.Sources))
	var failed int
	for _, r := range svc.Fetch(ctx, cfg.Sources, m.log) {
		if r.Status == datasets.StatusFailed {
			failed++
		}
	}
	snap, _, err := svc.Build(cfg.Sources)
	if err != nil {
		return fmt.Errorf("build (fuentes fallidas: %d): %w", failed, err)
	}
	man, path, diff, err := asnbuild.Publish(m.snapshots(), snap, false, readSnapshot)
	if err != nil {
		return err
	}
	m.log.InfoContext(ctx, "traffic: ASN snapshot published", "version", man.Version, "prefixes", man.Entries,
		"asns", snap.ASCount(), "changed_ratio", diff.Ratio, "failed_sources", failed, "path", path)
	return nil
}

// selected filtra las fuentes por HORUS_ASN_SOURCES.
func (m *mod) selected(all []datasets.Source) []datasets.Source {
	if len(m.cfg.ASNSources) == 0 || slices.Contains(m.cfg.ASNSources, "all") {
		return all
	}
	var out []datasets.Source
	for _, s := range all {
		if slices.Contains(m.cfg.ASNSources, s.ID) {
			out = append(out, s)
		}
	}
	return out
}

func readSnapshot(path string) (*asn.Snapshot, error) {
	f, err := os.Open(path) //nolint:gosec // ruta del almacén
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	s, err := asn.ReadSnapshot(f)
	if err != nil {
		return nil, errors.Join(errors.New("snapshot ASN ilegible"), err)
	}
	return s, nil
}
