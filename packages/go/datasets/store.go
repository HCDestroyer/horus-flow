package datasets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// ManifestSchemaVersion es la versión del formato de Manifest.
const ManifestSchemaVersion = 1

// Manifest describe una versión guardada de un dataset crudo
// (docs/storage.md §2.2: tipo, versión, número de filas, SHA-256, tamaño,
// herramienta y fecha).
type Manifest struct {
	SchemaVersion int           `json:"schema_version"`
	Kind          string        `json:"kind"`
	SourceID      string        `json:"source_id"`
	URL           string        `json:"url"`
	Format        string        `json:"format"`
	License       string        `json:"license"`
	LicenseURL    string        `json:"license_url"`
	CommercialUse CommercialUse `json:"commercial_use"`
	FetchedAt     time.Time     `json:"fetched_at"`
	File          string        `json:"file"` // relativo al directorio de la fuente
	SHA256        string        `json:"sha256"`
	Size          int64         `json:"size"`
	Entries       int           `json:"entries"`
	Tool          string        `json:"tool"`
}

// State es el estado operativo de una fuente: última versión válida y
// resultado del último intento.
type State struct {
	SourceID            string    `json:"source_id"`
	LastAttempt         time.Time `json:"last_attempt"`
	LastSuccess         time.Time `json:"last_success"` // último intento que dejó un dataset válido vigente
	LastError           string    `json:"last_error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	Current             *Manifest `json:"current,omitempty"`
}

// Store guarda los datasets crudos versionados en un directorio local
// (por defecto $HORUS_DATA_DIR/datasets). Estructura por fuente:
//
//	<root>/<source_id>/dt=YYYY-MM-DD/<YYYYMMDDTHHMMSSZ>-<archivo>          contenido original
//	<root>/<source_id>/dt=YYYY-MM-DD/<YYYYMMDDTHHMMSSZ>-<archivo>.manifest.json
//	<root>/<source_id>/state.json                                           estado + versión vigente
//
// Un solo escritor por directorio (docs/storage.md §2.3).
type Store struct {
	Root string
	Tool string // versión de la herramienta que escribe los manifiestos
}

func (s *Store) sourceDir(id string) string { return filepath.Join(s.Root, id) }

// LoadState devuelve el estado de una fuente (vacío si nunca se intentó).
func (s *Store) LoadState(id string) (State, error) {
	st := State{SourceID: id}
	b, err := os.ReadFile(filepath.Join(s.sourceDir(id), "state.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("leer estado de %s: %w", id, err)
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("estado de %s corrupto: %w", id, err)
	}
	return st, nil
}

func (s *Store) saveState(st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.sourceDir(st.SourceID), "state.json"), b)
}

// Path devuelve la ruta absoluta del archivo de un manifiesto.
func (s *Store) Path(m *Manifest) string {
	return filepath.Join(s.sourceDir(m.SourceID), filepath.FromSlash(m.File))
}

// Verify recalcula el SHA-256 del archivo de m y lo compara con el manifiesto.
func (s *Store) Verify(m *Manifest) error {
	f, err := os.Open(s.Path(m))
	if err != nil {
		return fmt.Errorf("abrir %s: %w", m.File, err)
	}
	defer closeQuietly(f)
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return fmt.Errorf("leer %s: %w", m.File, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != m.SHA256 || n != m.Size {
		return fmt.Errorf("%s: checksum o tamaño no coinciden (sha256 %s, %d bytes; manifiesto %s, %d bytes)",
			m.File, got, n, m.SHA256, m.Size)
	}
	return nil
}

// OpenCurrent abre la versión vigente de una fuente tras verificar su
// checksum. Devuelve ErrNotFound si no hay ninguna.
func (s *Store) OpenCurrent(id string) (*os.File, *Manifest, error) {
	st, err := s.LoadState(id)
	if err != nil {
		return nil, nil, err
	}
	if st.Current == nil {
		return nil, nil, fmt.Errorf("%w: %s sin versión válida", ErrNotFound, id)
	}
	if err := s.Verify(st.Current); err != nil {
		return nil, nil, err
	}
	f, err := os.Open(s.Path(st.Current))
	if err != nil {
		return nil, nil, err
	}
	return f, st.Current, nil
}

// incoming copia r a un archivo temporal dentro del directorio de la fuente,
// con límite de tamaño, y devuelve su ruta, SHA-256 y tamaño.
func (s *Store) incoming(id string, r io.Reader, limit int64) (string, string, int64, error) {
	dir := s.sourceDir(id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", "", 0, err
	}
	f, err := os.CreateTemp(dir, ".incoming-*")
	if err != nil {
		return "", "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > limit {
		err = fmt.Errorf("la descarga excede el máximo de %d bytes", limit)
	}
	if err == nil && n == 0 {
		err = errors.New("descarga vacía")
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", "", 0, err
	}
	return f.Name(), hex.EncodeToString(h.Sum(nil)), n, nil
}

// commit mueve un archivo temporal validado a su ruta versionada y escribe su
// manifiesto.
func (s *Store) commit(tmp string, m *Manifest) error {
	ts := m.FetchedAt.UTC()
	rel := path.Join("dt="+ts.Format("2006-01-02"), ts.Format("20060102T150405Z")+"-"+fileBase(m.URL))
	dst := filepath.Join(s.sourceDir(m.SourceID), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	m.File = rel
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(dst+".manifest.json", b)
}

// Prune borra las versiones guardadas antes de cutoff, salvo la vigente
// (docs/storage.md §3: datasets externos 90 días, sin copia remota).
func (s *Store) Prune(id string, cutoff time.Time) (int, error) {
	st, err := s.LoadState(id)
	if err != nil {
		return 0, err
	}
	keepDir := ""
	if st.Current != nil {
		keepDir = path.Dir(st.Current.File)
	}
	entries, err := os.ReadDir(s.sourceDir(id))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, "dt=") || name == keepDir {
			continue
		}
		day, err := time.Parse("2006-01-02", strings.TrimPrefix(name, "dt="))
		if err != nil || !day.Before(cutoff.UTC().Truncate(24*time.Hour)) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.sourceDir(id), name)); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// fileBase deriva un nombre de archivo seguro a partir de la URL.
func fileBase(raw string) string {
	base := "data"
	if u, err := url.Parse(raw); err == nil {
		if b := path.Base(u.Path); b != "/" && b != "." && b != "" {
			base = b
		}
	}
	var sb strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	out := strings.TrimLeft(sb.String(), ".")
	if out == "" {
		out = "data"
	}
	if len(out) > 80 {
		out = out[len(out)-80:]
	}
	return out
}

// writeFileAtomic escribe en un temporal del mismo directorio, sincroniza y
// renombra, de modo que un lector nunca ve un archivo a medias.
func writeFileAtomic(dst string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), dst)
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("escribir %s: %w", dst, err)
	}
	return nil
}

func closeQuietly(c io.Closer) { _ = c.Close() }
