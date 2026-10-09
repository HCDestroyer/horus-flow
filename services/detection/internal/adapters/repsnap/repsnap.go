// Package repsnap carga el snapshot de reputación vigente de un directorio
// de snapshots (datasets.SnapshotDir: <dir>/latest → v<N>/snapshot.hsnp) y
// lo recarga cuando cambia el puntero latest.
package repsnap

import (
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
)

// Dir es un proveedor de snapshot (engine.Reputation).
type Dir struct {
	root    string
	every   time.Duration
	log     *slog.Logger
	mu      sync.Mutex
	cur     *reputation.Snapshot
	version string
	checked time.Time
}

// New crea el proveedor; every es cada cuánto se mira el puntero latest.
func New(root string, every time.Duration, log *slog.Logger) *Dir {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Dir{root: root, every: every, log: log}
}

// Static es un proveedor con un snapshot fijo (tests).
type Static struct{ S *reputation.Snapshot }

// Current implementa engine.Reputation.
func (s Static) Current() *reputation.Snapshot { return s.S }

// Current implementa engine.Reputation.
func (d *Dir) Current() *reputation.Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.root == "" || (d.cur != nil && time.Since(d.checked) < d.every) {
		return d.cur
	}
	d.checked = time.Now()
	m, path, err := datasets.SnapshotDir{Root: d.root}.Latest()
	if err != nil {
		if d.cur == nil {
			d.log.Warn("reputation snapshot unavailable", "dir", d.root, "err", err)
		}
		return d.cur
	}
	if m.Version == d.version && d.cur != nil {
		return d.cur
	}
	f, err := os.Open(path) //nolint:gosec // ruta del directorio de snapshots configurado
	if err != nil {
		d.log.Warn("reputation snapshot open", "err", err)
		return d.cur
	}
	defer func() { _ = f.Close() }()
	s, err := reputation.ReadSnapshot(f)
	if err != nil {
		d.log.Warn("reputation snapshot invalid; keeping previous", "version", m.Version, "err", err)
		return d.cur
	}
	d.cur, d.version = s, m.Version
	d.log.Info("reputation snapshot loaded", "version", m.Version, "entries", s.Len())
	return d.cur
}
