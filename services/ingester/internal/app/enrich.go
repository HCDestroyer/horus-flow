package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/asn"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/catalog"
)

// Snapshot cargado con su versión numérica (vN ⇒ N).
type loaded[T any] struct {
	v       *T
	version uint32
	tag     string
}

// Enrichment es el Enricher del ingester (I1-07 y reputación en ingesta,
// traffic-model.md §2 pasos 7–10, §11): ASN/organización, servicio y
// categoría del catálogo y marca de reputación de la IP remota. Los tres
// snapshots se recargan en caliente; uno corrupto se rechaza y sigue el anterior.
type Enrichment struct {
	asn     atomic.Pointer[loaded[asn.Snapshot]]
	catalog atomic.Pointer[loaded[catalog.Catalog]]
	rep     atomic.Pointer[loaded[reputation.Snapshot]]

	bytes, withASN, withService *prometheus.CounterVec
	reloads                     *prometheus.CounterVec
	log                         *slog.Logger
}

// NewEnrichment crea el enriquecedor con el catálogo semilla; reg puede ser nil.
func NewEnrichment(reg prometheus.Registerer, log *slog.Logger) (*Enrichment, error) {
	e := &Enrichment{log: log,
		bytes: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_ingester_remote_bytes_total",
			Help: "Bytes con IP remota pública por tenant (base de la cobertura de enriquecimiento)."}, []string{"tenant_id"}),
		withASN: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_ingester_remote_bytes_with_asn_total",
			Help: "Bytes con IP remota pública y ASN conocido por tenant."}, []string{"tenant_id"}),
		withService: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_ingester_remote_bytes_with_service_total",
			Help: "Bytes con IP remota pública clasificados en un servicio por tenant."}, []string{"tenant_id"}),
		reloads: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_ingester_snapshot_reloads_total",
			Help: "Recargas de snapshots (asn, catalog, reputation) por resultado."}, []string{"kind", "result"}),
	}
	if reg != nil {
		for _, c := range []prometheus.Collector{e.bytes, e.withASN, e.withService, e.reloads} {
			_ = reg.Register(c)
		}
	}
	c, err := catalog.Seed()
	if err != nil {
		return nil, err
	}
	e.catalog.Store(&loaded[catalog.Catalog]{v: c, version: c.Version(), tag: "seed"})
	return e, nil
}

// SetASN, SetCatalog y SetReputation sustituyen un snapshot (tests y recarga).
func (e *Enrichment) SetASN(s *asn.Snapshot) {
	e.asn.Store(&loaded[asn.Snapshot]{v: s, version: versionOf(s.Meta.Version), tag: s.Meta.Version})
}

// SetCatalog sustituye el catálogo.
func (e *Enrichment) SetCatalog(c *catalog.Catalog) {
	e.catalog.Store(&loaded[catalog.Catalog]{v: c, version: c.Version(), tag: "v" + strconv.FormatUint(uint64(c.Version()), 10)})
}

// SetReputation sustituye el snapshot de reputación.
func (e *Enrichment) SetReputation(s *reputation.Snapshot) {
	e.rep.Store(&loaded[reputation.Snapshot]{v: s, version: versionOf(s.Meta.Version), tag: s.Meta.Version})
}

// CatalogVersion devuelve la versión del catálogo vigente.
func (e *Enrichment) CatalogVersion() uint32 { return e.catalog.Load().version }

func versionOf(v string) uint32 {
	n, err := strconv.ParseUint(strings.TrimPrefix(v, "v"), 10, 32)
	if err != nil {
		return 0
	}
	return uint32(n)
}

var nat64 = netip.MustParsePrefix("64:ff9b::/96")

// lookupAddr: con NAT64 el ASN/servicio/reputación se resuelven con la IPv4
// incrustada (§4.8.3).
func lookupAddr(a netip.Addr) netip.Addr {
	if a.Is6() && nat64.Contains(a) {
		b := a.As16()
		return netip.AddrFrom4([4]byte(b[12:16]))
	}
	return a
}

func publicAddr(a netip.Addr) bool {
	return a.IsValid() && a.IsGlobalUnicast() && !a.IsPrivate() && !cgnat.Contains(a)
}

// ASN implementa Enricher.
func (e *Enrichment) ASN(ip netip.Addr) uint32 {
	ip = lookupAddr(ip)
	if l := e.asn.Load(); l != nil {
		if r, ok := l.v.Lookup(ip); ok {
			return r.ASN
		}
	}
	if _, a, ok := e.catalog.Load().v.LookupPrefix(ip); ok {
		return a
	}
	return 0
}

// SourceID convierte el id textual de una fuente de reputación en el
// UInt16 de flows_raw.reputation_source_id (FNV-1a, nunca 0). Estable entre
// versiones; detection usa la misma función para resolverlo.
func SourceID(source string) uint16 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(source))
	v := uint16(h.Sum32()%65535) + 1 //nolint:gosec // acotado a 1..65535
	return v
}

var repCategory = map[reputation.Category]string{
	reputation.CategoryBotnetCC: "botnet_cc", reputation.CategoryScanner: "scanner",
	reputation.CategoryMalware: "malware_dist", reputation.CategoryMining: "mining_pool",
	reputation.CategoryProxy: "proxy_vpn", reputation.CategoryTor: "tor_exit",
	reputation.CategoryBlocklist: "blocklist", reputation.CategorySpam: "blocklist",
	reputation.CategoryOther: "blocklist",
}

// Enrich implementa Enricher.
func (e *Enrichment) Enrich(tenant uuid.UUID, row *Row) {
	remote := lookupAddr(row.RemoteIP)
	cat := e.catalog.Load()
	row.CatalogVersion = cat.version
	public := publicAddr(remote)
	var asnNum uint32
	if public {
		if l := e.asn.Load(); l != nil {
			if r, ok := l.v.Lookup(remote); ok {
				asnNum = r.ASN
				row.RemotePrefix, row.RemotePrefixLen = r.Prefix.Addr(), uint8(r.Prefix.Bits()) //nolint:gosec // ≤ 128
				if len(r.Country) == 2 {
					row.RemoteCountry = r.Country
				} else if len(r.AS.Country) == 2 {
					row.RemoteCountry = r.AS.Country
				}
			}
		}
		if asnNum == 0 {
			if p, a, ok := cat.v.LookupPrefix(remote); ok {
				asnNum = a
				row.RemotePrefix, row.RemotePrefixLen = p.Addr(), uint8(p.Bits()) //nolint:gosec // ≤ 128
			}
		}
		if asnNum != 0 {
			row.RemoteASN = asnNum
			if o, ok := cat.v.Organization(asnNum); ok {
				row.RemoteOrgID = catalog.ID("org", o.Slug)
				if row.RemoteCountry == "" && len(o.Country) == 2 {
					row.RemoteCountry = o.Country
				}
			} else {
				row.RemoteOrgID = catalog.OrgIDForASN(asnNum)
			}
		}
	}
	if public || row.RemotePort != 0 {
		res := cat.v.Classify(remote, asnNum, row.Protocol, row.RemotePort)
		row.ServiceID, row.CategoryID = res.ServiceID, res.CategoryID
		row.ClassificationMethod, row.ClassificationConfidence = res.Method, res.Confidence
	}
	if public {
		if l := e.rep.Load(); l != nil {
			if hit, ok := l.v.Best(remote); ok && (hit.ExpiresAt.IsZero() || hit.ExpiresAt.After(row.TS)) {
				row.ReputationCategory = repCategory[hit.Category]
				if row.ReputationCategory == "" {
					row.ReputationCategory = "blocklist"
				}
				row.ReputationSourceID = SourceID(hit.Source)
				row.ReputationConfidence = hit.Confidence
				row.ReputationVersion = l.version
			}
		}
		t := tenant.String()
		e.bytes.WithLabelValues(t).Add(float64(row.Bytes))
		if asnNum != 0 {
			e.withASN.WithLabelValues(t).Add(float64(row.Bytes))
		}
		if row.ServiceID != uuid.Nil {
			e.withService.WithLabelValues(t).Add(float64(row.Bytes))
		}
	}
}

// ---------------------------------------------------------------- recarga

func openVerified(dir string) (*datasets.SnapshotManifest, *os.File, error) {
	m, path, err := datasets.SnapshotDir{Root: dir}.Latest()
	if err != nil {
		return nil, nil, err
	}
	b, err := os.ReadFile(path) //nolint:gosec // directorio de configuración
	if err != nil {
		return nil, nil, err
	}
	if sum := sha256.Sum256(b); m.SHA256 != "" && hex.EncodeToString(sum[:]) != m.SHA256 {
		return nil, nil, fmt.Errorf("%w: sha256 mismatch", datasets.ErrCorruptSnapshot)
	}
	f, err := os.Open(path) //nolint:gosec // directorio de configuración
	return m, f, err
}

// Reload carga la última versión de cada directorio configurado si cambió.
func (e *Enrichment) Reload(asnDir, catalogDir, repDir string) {
	type job struct {
		kind, dir string
		cur       func() string
		load      func(f *os.File) error
	}
	jobs := []job{
		{"asn", asnDir, func() string {
			if l := e.asn.Load(); l != nil {
				return l.tag
			}
			return ""
		}, func(f *os.File) error {
			s, err := asn.ReadSnapshot(f)
			if err == nil {
				e.SetASN(s)
			}
			return err
		}},
		{"catalog", catalogDir, func() string { return e.catalog.Load().tag }, func(f *os.File) error {
			c, err := catalog.ReadSnapshot(f)
			if err == nil {
				e.catalog.Store(&loaded[catalog.Catalog]{v: c, version: c.Version(), tag: "v" + strconv.FormatUint(uint64(c.Version()), 10)})
			}
			return err
		}},
		{"reputation", repDir, func() string {
			if l := e.rep.Load(); l != nil {
				return l.tag
			}
			return ""
		}, func(f *os.File) error {
			s, err := reputation.ReadSnapshot(f)
			if err == nil {
				e.SetReputation(s)
			}
			return err
		}},
	}
	for _, j := range jobs {
		if j.dir == "" {
			continue
		}
		m, _, err := datasets.SnapshotDir{Root: j.dir}.Latest()
		if err != nil || m.Version == j.cur() {
			continue
		}
		m, f, err := openVerified(j.dir)
		if err == nil {
			err = j.load(f)
			_ = f.Close()
		}
		if err != nil {
			e.reloads.WithLabelValues(j.kind, "rejected").Inc()
			e.log.Warn("snapshot rejected, keeping the previous one", "kind", j.kind, "error", err)
			continue
		}
		e.reloads.WithLabelValues(j.kind, "loaded").Inc()
		e.log.Info("snapshot loaded", "kind", j.kind, "version", m.Version)
	}
}

// Watch recarga periódicamente hasta que ctx se cancela.
func (e *Enrichment) Watch(ctx context.Context, every time.Duration, asnDir, catalogDir, repDir string) {
	e.Reload(asnDir, catalogDir, repDir)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.Reload(asnDir, catalogDir, repDir)
		}
	}
}
