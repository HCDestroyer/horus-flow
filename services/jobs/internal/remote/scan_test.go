package remote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFile crea root/rel con contenido y mtime.
func writeFile(t *testing.T, root, rel, content string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// fakeBackups crea un árbol como el de horus-backup: repositorio pgBackRest
// y BACKUP de ClickHouse, con un backup de cada en curso.
func fakeBackups(t *testing.T, root string, old time.Time) {
	t.Helper()
	files := map[string]string{
		"backups/postgres/backup/horus/backup.info":                                             "info",
		"backups/postgres/backup/horus/20261011-020000F/backup.manifest":                        "manifest",
		"backups/postgres/backup/horus/20261011-020000F/pg_data/base/1/1259.zst":                "datos de clientes",
		"backups/postgres/archive/horus/18-1/0000000100000000/000000010000000000000001-abc.zst": "wal",
		"backups/clickhouse/full_20261011T030000Z/.backup":                                      "<backup/>",
		"backups/clickhouse/full_20261011T030000Z/data/flows/agg/all_1_1_0/data.bin":            "agregados",
		"backups/clickhouse/meta/full_20261011T030000Z.before":                                  "flows.agg 10",
		// En curso: sin backup.manifest / sin .backup.
		"backups/postgres/backup/horus/20261012-020000F_20261012-030000D/pg_data/x": "a medias",
		"backups/clickhouse/incr_20261012T030000Z/data/flows/agg/x.bin":             "a medias",
		// Temporal de pgBackRest y fuera de las raíces.
		"backups/postgres/archive/horus/18-1/0000000100000000/x.pgbackrest.tmp": "tmp",
		"backups/state/postgres.last_success":                                   "1",
		"reports/t/r.pdf":                                                       "pdf",
		// Temporal del render de reportes (antes del rename atómico).
		"reports/t/.tmp-123456": "tmp",
	}
	for rel, c := range files {
		// Contenido largo y reconocible: la comprobación "no está en claro" no da falsos positivos.
		writeFile(t, root, rel, c+" | contenido en claro de prueba 0123456789", old)
	}
}

func TestScanStore(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Now()
	old := now.Add(-time.Hour)
	fakeBackups(t, root, old)
	// Recién escrito: aún no se copia, pero está presente.
	writeFile(t, root, "backups/postgres/archive/horus/18-1/0000000100000000/000000010000000000000002-def.zst", "wal2", now)
	if err := os.Symlink("backup/horus/20261011-020000F", filepath.Join(root, "backups/postgres/latest")); err != nil {
		t.Fatal(err)
	}
	sc, err := ScanStore(root, now, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range sc.Files {
		got = append(got, f.Rel)
	}
	want := []string{
		"backups/clickhouse/full_20261011T030000Z/.backup",
		"backups/clickhouse/full_20261011T030000Z/data/flows/agg/all_1_1_0/data.bin",
		"backups/clickhouse/meta/full_20261011T030000Z.before",
		"backups/postgres/archive/horus/18-1/0000000100000000/000000010000000000000001-abc.zst",
		"backups/postgres/backup/horus/20261011-020000F/backup.manifest",
		"backups/postgres/backup/horus/20261011-020000F/pg_data/base/1/1259.zst",
		"backups/postgres/backup/horus/backup.info",
		"reports/t/r.pdf",
	}
	if len(got) != len(want) {
		t.Fatalf("archivos = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("archivos[%d] = %s, quiero %s (todos: %v)", i, got[i], want[i], got)
		}
	}
	for _, rel := range []string{
		"backups/postgres/archive/horus/18-1/0000000100000000/000000010000000000000002-def.zst",
		"backups/postgres/backup/horus/20261012-020000F_20261012-030000D/pg_data/x",
		"backups/clickhouse/incr_20261012T030000Z/data/flows/agg/x.bin",
	} {
		if !sc.Present[rel] {
			t.Errorf("%s debería estar presente", rel)
		}
	}
	if !sc.Present["reports/t/.tmp-123456"] {
		t.Error("el temporal de reportes no se copia pero está presente")
	}
	if sc.Present["backups/state/postgres.last_success"] {
		t.Error("se recorrió fuera de backups/postgres, backups/clickhouse y reports")
	}
	// Almacén sin backups: vacío, sin error.
	if sc, err := ScanStore(t.TempDir(), now, 0); err != nil || len(sc.Files) != 0 {
		t.Fatalf("almacén vacío: %v %v", sc, err)
	}
}

func TestDiffAndRetention(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	mt := now.Add(-time.Hour)
	sc := &Scan{
		Files: []LocalFile{
			{Rel: "backups/a", Size: 10, MTime: mt},  // igual que en el ledger
			{Rel: "backups/b", Size: 20, MTime: mt},  // nuevo
			{Rel: "backups/c", Size: 31, MTime: mt},  // cambió de tamaño
			{Rel: "backups/d", Size: 40, MTime: now}, // cambió de mtime
		},
		Present: map[string]bool{"backups/a": true, "backups/b": true, "backups/c": true, "backups/d": true, "backups/busy": true},
	}
	ledger := map[string]LedgerEntry{
		"backups/a":      {Rel: "backups/a", Size: 10, MTime: mt, UploadedAt: now.Add(-100 * 24 * time.Hour)},
		"backups/c":      {Rel: "backups/c", Size: 30, MTime: mt, UploadedAt: now},
		"backups/d":      {Rel: "backups/d", Size: 40, MTime: mt, UploadedAt: now},
		"backups/gone":   {Rel: "backups/gone", UploadedAt: now.Add(-31 * 24 * time.Hour), RemoteName: "r/gone.age"},
		"backups/recent": {Rel: "backups/recent", UploadedAt: now.Add(-29 * 24 * time.Hour)},
		"backups/busy":   {Rel: "backups/busy", UploadedAt: now.Add(-90 * 24 * time.Hour)},
	}
	p := Diff(sc, ledger, now, 30*24*time.Hour, AllPaths)
	if len(p.Upload) != 3 || p.Upload[0].Rel != "backups/b" || p.Upload[1].Rel != "backups/c" || p.Upload[2].Rel != "backups/d" || p.BytesPending != 91 {
		t.Fatalf("upload = %+v (%d B)", p.Upload, p.BytesPending)
	}
	// Solo se borra lo vencido que ya no está en local: nunca "backups/a" (local,
	// aunque vencido) ni "backups/busy" (presente aunque no copiable) ni "backups/recent".
	if len(p.Delete) != 1 || p.Delete[0].Rel != "backups/gone" {
		t.Fatalf("delete = %+v", p.Delete)
	}
}

func TestDiffSelectedPaths(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	mt := now.Add(-time.Hour)
	sc := &Scan{
		Files:   []LocalFile{{Rel: "backups/postgres/x", Size: 1, MTime: mt}, {Rel: "reports/t/r.pdf", Size: 2, MTime: mt}},
		Present: map[string]bool{"backups/postgres/x": true, "reports/t/r.pdf": true},
	}
	// Ya copiado antes de quitar reports de la selección y vencido: sigue en
	// local, así que no se borra del destino.
	ledger := map[string]LedgerEntry{"reports/t/old.pdf": {Rel: "reports/t/old.pdf", UploadedAt: now.Add(-60 * 24 * time.Hour)}}
	sc.Present["reports/t/old.pdf"] = true
	for _, c := range []struct {
		paths []string
		want  []string
	}{
		{[]string{PathBackups}, []string{"backups/postgres/x"}},
		{[]string{PathReports}, []string{"reports/t/r.pdf"}},
		{AllPaths, []string{"backups/postgres/x", "reports/t/r.pdf"}},
	} {
		p := Diff(sc, ledger, now, 30*24*time.Hour, c.paths)
		var got []string
		for _, f := range p.Upload {
			got = append(got, f.Rel)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") || len(p.Delete) != 0 {
			t.Errorf("paths %v: upload %v delete %v", c.paths, got, p.Delete)
		}
	}
	if PathOf("archive/t/x") != "" || PathOf("reports/t/x") != PathReports || PathOf("backups/clickhouse/y") != PathBackups {
		t.Error("PathOf")
	}
}

func TestRemoteName(t *testing.T) {
	t.Parallel()
	if got := RemoteName("/copias/", "horus-x", "backups/postgres/backup.info"); got != "/copias/horus-x/backups/postgres/backup.info.age" {
		t.Fatal(got)
	}
	if got := RemoteName("rel", "horus-x", "backups/a"); got != "rel/horus-x/backups/a.age" {
		t.Fatal(got)
	}
}
