package remote

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Roots son las carpetas del almacén que se recorren (relativas a
// HORUS_DATA_DIR): repositorio pgBackRest y BACKUP nativos de ClickHouse
// (docs/storage.md §2.2) y los reportes del rol reporting. Cada destino sube
// solo las de su selección (Destination.Paths, [PathOf]). El resto del
// almacén (archive/, audit/…) llega con sus productores.
var Roots = []string{"backups/postgres", "backups/clickhouse", "reports"}

// PathOf devuelve la ruta seleccionable (PathBackups, PathReports) a la que
// pertenece un archivo del almacén ("" si ninguna).
func PathOf(rel string) string {
	top, _, _ := strings.Cut(rel, "/")
	switch top {
	case "backups":
		return PathBackups
	case "reports":
		return PathReports
	}
	return ""
}

// LocalFile es un archivo del almacén candidato a copiarse.
type LocalFile struct {
	Rel   string // ruta relativa al almacén, con "/"
	Size  int64
	MTime time.Time // truncada a microsegundos (precisión de timestamptz)
}

// Scan es el resultado de recorrer el almacén.
type Scan struct {
	// Files: archivos completos que se pueden copiar.
	Files []LocalFile
	// Present: todas las rutas que existen en local (también las que aún se
	// están escribiendo): la retención remota nunca borra algo presente.
	Present map[string]bool
	At      time.Time
}

// pgBackRest: un backup en curso (o abortado) es un directorio
// backup/<stanza>/<etiqueta> sin backup.manifest.
var pgLabelRe = regexp.MustCompile(`^\d{8}-\d{6}F(_\d{8}-\d{6}[DI])?$`)

// ClickHouse: un BACKUP en curso es backups/clickhouse/<nombre> sin .backup.
var chNameRe = regexp.MustCompile(`^(full|incr)_\d{8}T\d{6}Z$`)

// ScanStore recorre las carpetas de [Roots] bajo root. settle es el tiempo
// sin cambios que se exige a un archivo (se está escribiendo si su mtime es
// más reciente). Se omiten enlaces simbólicos, temporales (*.tmp y .tmp-*,
// los del render de reportes antes del rename atómico) y los backups aún
// incompletos.
func ScanStore(root string, now time.Time, settle time.Duration) (*Scan, error) {
	sc := &Scan{Present: map[string]bool{}, At: now}
	for _, top := range Roots {
		base := filepath.Join(root, filepath.FromSlash(top))
		if _, err := os.Lstat(base); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		err := filepath.WalkDir(base, func(p string, de fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrPermission) {
					return fmt.Errorf("sin permiso de lectura en %s", relOf(root, p))
				}
				return err
			}
			rel := relOf(root, p)
			if de.IsDir() {
				if incompleteBackupDir(p, rel) {
					markPresent(sc, p, root)
					return fs.SkipDir
				}
				return nil
			}
			if !de.Type().IsRegular() {
				return nil
			}
			sc.Present[rel] = true
			if strings.HasSuffix(de.Name(), ".tmp") || strings.HasPrefix(de.Name(), ".tmp") {
				return nil
			}
			info, err := de.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					delete(sc.Present, rel)
					return nil
				}
				return err
			}
			mt := info.ModTime().Truncate(time.Microsecond)
			if now.Sub(mt) < settle {
				return nil
			}
			sc.Files = append(sc.Files, LocalFile{Rel: rel, Size: info.Size(), MTime: mt.UTC()})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	// Prioridad de storage.md §4.4: PostgreSQL antes que ClickHouse; dentro,
	// orden lexicográfico (estable y reanudable).
	sort.SliceStable(sc.Files, func(i, j int) bool { return sc.Files[i].Rel < sc.Files[j].Rel })
	return sc, nil
}

func relOf(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}

// incompleteBackupDir: directorio de un backup que aún se está escribiendo.
func incompleteBackupDir(p, rel string) bool {
	name := path.Base(rel)
	switch {
	case strings.HasPrefix(rel, "backups/postgres/backup/") && pgLabelRe.MatchString(name):
		_, err := os.Stat(filepath.Join(p, "backup.manifest"))
		return err != nil
	case path.Dir(rel) == "backups/clickhouse" && chNameRe.MatchString(name):
		_, err := os.Stat(filepath.Join(p, ".backup"))
		return err != nil
	}
	return false
}

// markPresent apunta como presentes los archivos de un directorio que no se
// copia todavía (para que la retención no los borre del destino).
func markPresent(sc *Scan, dir, root string) {
	_ = filepath.WalkDir(dir, func(p string, de fs.DirEntry, err error) error {
		if err == nil && de.Type().IsRegular() {
			sc.Present[relOf(root, p)] = true
		}
		return nil
	})
}

// LedgerEntry es una fila de jobs.remote_copy.
type LedgerEntry struct {
	Rel        string
	Size       int64
	MTime      time.Time
	SHA256     []byte
	RemoteName string
	UploadedAt time.Time
}

// Plan es lo que hay que hacer en un destino.
type Plan struct {
	Upload       []LocalFile
	Delete       []LedgerEntry
	BytesPending int64
}

// Diff compara el almacén con el ledger de un destino: sube lo que no está
// o cambió (tamaño o mtime) de las rutas seleccionadas (paths) y borra del
// destino lo que venció su retención (subido hace más de retention) y ya no
// existe en local. Nunca borra algo que sigue en local, aunque su ruta ya no
// esté seleccionada.
func Diff(sc *Scan, ledger map[string]LedgerEntry, now time.Time, retention time.Duration, paths []string) Plan {
	var p Plan
	for _, f := range sc.Files {
		if !slices.Contains(paths, PathOf(f.Rel)) {
			continue
		}
		e, ok := ledger[f.Rel]
		if ok && e.Size == f.Size && e.MTime.Equal(f.MTime) {
			continue
		}
		p.Upload = append(p.Upload, f)
		p.BytesPending += f.Size
	}
	cutoff := now.Add(-retention)
	for rel, e := range ledger {
		if sc.Present[rel] || !e.UploadedAt.Before(cutoff) {
			continue
		}
		p.Delete = append(p.Delete, e)
	}
	sort.Slice(p.Delete, func(i, j int) bool { return p.Delete[i].Rel < p.Delete[j].Rel })
	return p
}

// RemoteName es el nombre del archivo cifrado en el destino:
// <ruta>/<prefijo>/<relativa>.age.
func RemoteName(destPath, prefix, rel string) string {
	return path.Join(destPath, prefix, rel) + ".age"
}
