package reporting

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Files es el almacén local de reportes: <Root>/reports/<tenant_id>/
// (ADR-0019; jobs replica reports/ al destino remoto cuando se configure).
// Los archivos solo se sirven por la API (GET /reports/{id}/download).
type Files struct{ Root string }

// objectKey es la clave relativa a Root de un archivo de una ejecución.
func objectKey(tenant, run uuid.UUID, format string) string {
	name := run.String() + ".pdf"
	if format == FormatCSV {
		name = run.String() + "-csv.zip"
	}
	return "reports/" + tenant.String() + "/" + name
}

var errBadKey = errors.New("reporting: object key fuera de reports/")

// path valida la clave (siempre reports/<uuid>/<archivo>, sin ..) y la resuelve.
func (f Files) path(key string) (string, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != "reports" || strings.Contains(key, "..") || strings.Contains(key, "\\") {
		return "", errBadKey
	}
	if _, err := uuid.Parse(parts[1]); err != nil {
		return "", errBadKey
	}
	return filepath.Join(f.Root, filepath.FromSlash(key)), nil
}

// Write genera el archivo de format con render (a un temporal y rename
// atómico) y devuelve su clave, sha256 y tamaño.
func (f Files) Write(tenant, run uuid.UUID, format string, render func(io.Writer) error) (File, error) {
	key := objectKey(tenant, run, format)
	dst, err := f.path(key)
	if err != nil {
		return File{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return File{}, fmt.Errorf("reporting: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return File{}, fmt.Errorf("reporting: create: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	cw := &countWriter{w: io.MultiWriter(tmp, h)}
	if err := render(cw); err != nil {
		_ = tmp.Close()
		return File{}, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return File{}, fmt.Errorf("reporting: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return File{}, fmt.Errorf("reporting: close: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o640); err != nil {
		return File{}, fmt.Errorf("reporting: chmod: %w", err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return File{}, fmt.Errorf("reporting: rename: %w", err)
	}
	return File{Format: format, ObjectKey: key, SHA256: hex.EncodeToString(h.Sum(nil)), SizeBytes: cw.n}, nil
}

// Open abre un archivo por su clave.
func (f Files) Open(key string) (*os.File, error) {
	p, err := f.path(key)
	if err != nil {
		return nil, err
	}
	fh, err := os.Open(p) //nolint:gosec // ruta validada: reports/<uuid>/<archivo> bajo Root
	if err != nil {
		return nil, fmt.Errorf("reporting: open: %w", err)
	}
	return fh, nil
}

// Remove borra un archivo (ya borrado = sin error).
func (f Files) Remove(key string) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reporting: remove: %w", err)
	}
	return nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err //nolint:wrapcheck // escritor transparente
}
