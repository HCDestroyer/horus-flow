package datasets

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Contenedor binario de snapshots (versión 1):
//
//	offset  tamaño  campo
//	0       4       magia "HSNP"
//	4       2       versión del contenedor (uint16 BE) = 1
//	6       2       reservado (0)
//	8       32      SHA-256 del cuerpo comprimido
//	40      …       cuerpo: zstd( uint32 BE longitud de meta | meta JSON | payload )
//
// meta (SnapshotMeta) lleva el tipo, la versión del formato del payload, las
// fuentes con licencia y checksum, la fecha y el número de entradas; el
// payload lo codifica el módulo dueño (Enc/Dec). Un lector rechaza magia,
// versión o checksum desconocidos antes de descomprimir.
const (
	snapshotMagic            = "HSNP"
	SnapshotContainerVersion = 1
	snapshotHeaderLen        = 40
	// DefaultMaxSnapshotBytes limita el tamaño descomprimido (docs/storage.md:
	// < 100 MB por versión; margen ×4).
	DefaultMaxSnapshotBytes = 400 << 20
)

// SourceRef identifica una versión concreta de una fuente usada en un
// snapshot (trazabilidad y licencias).
type SourceRef struct {
	ID            string        `json:"id"`
	License       string        `json:"license"`
	LicenseURL    string        `json:"license_url"`
	CommercialUse CommercialUse `json:"commercial_use"`
	Attribution   string        `json:"attribution,omitempty"`
	FetchedAt     time.Time     `json:"fetched_at"`
	SHA256        string        `json:"sha256"`
	Entries       int           `json:"entries"`
}

// RefFromManifest construye una SourceRef desde un manifiesto y su fuente.
func RefFromManifest(src Source, m *Manifest, entries int) SourceRef {
	return SourceRef{
		ID: m.SourceID, License: m.License, LicenseURL: m.LicenseURL, CommercialUse: m.CommercialUse,
		Attribution: src.Attribution, FetchedAt: m.FetchedAt, SHA256: m.SHA256, Entries: entries,
	}
}

// SnapshotMeta son los metadatos de un snapshot.
type SnapshotMeta struct {
	Kind          string            `json:"kind"`
	PayloadFormat int               `json:"payload_format"`
	Version       string            `json:"version"`
	CreatedAt     time.Time         `json:"created_at"`
	Entries       int               `json:"entries"`
	Sources       []SourceRef       `json:"sources"`
	Labels        map[string]string `json:"labels,omitempty"`
}

// EncodeSnapshot escribe el contenedor con meta y payload.
func EncodeSnapshot(w io.Writer, meta SnapshotMeta, payload []byte) error {
	mb, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("meta: %w", err)
	}
	var body bytes.Buffer
	zw, err := zstd.NewWriter(&body, zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
	if err != nil {
		return err
	}
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(mb))) //nolint:gosec // meta es pequeña
	for _, p := range [][]byte{l[:], mb, payload} {
		if _, err := zw.Write(p); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	sum := sha256.Sum256(body.Bytes())
	hdr := make([]byte, 0, snapshotHeaderLen)
	hdr = append(hdr, snapshotMagic...)
	hdr = binary.BigEndian.AppendUint16(hdr, SnapshotContainerVersion)
	hdr = binary.BigEndian.AppendUint16(hdr, 0)
	hdr = append(hdr, sum[:]...)
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err = w.Write(body.Bytes())
	return err
}

// ErrCorruptSnapshot indica un snapshot con magia, versión, checksum o
// contenido inválidos.
var ErrCorruptSnapshot = errors.New("datasets: snapshot corrupto")

// DecodeSnapshot lee y verifica un contenedor; expectKind vacío acepta
// cualquier tipo.
func DecodeSnapshot(r io.Reader, expectKind string) (SnapshotMeta, []byte, error) {
	var meta SnapshotMeta
	raw, err := io.ReadAll(io.LimitReader(r, DefaultMaxSnapshotBytes+1))
	if err != nil {
		return meta, nil, err
	}
	if len(raw) < snapshotHeaderLen || string(raw[:4]) != snapshotMagic {
		return meta, nil, fmt.Errorf("%w: magia inválida", ErrCorruptSnapshot)
	}
	if v := binary.BigEndian.Uint16(raw[4:6]); v != SnapshotContainerVersion {
		return meta, nil, fmt.Errorf("%w: versión de contenedor %d no soportada", ErrCorruptSnapshot, v)
	}
	body := raw[snapshotHeaderLen:]
	if sum := sha256.Sum256(body); !bytes.Equal(sum[:], raw[8:40]) {
		return meta, nil, fmt.Errorf("%w: checksum no coincide", ErrCorruptSnapshot)
	}
	zr, err := zstd.NewReader(bytes.NewReader(body), zstd.WithDecoderMaxMemory(DefaultMaxSnapshotBytes))
	if err != nil {
		return meta, nil, err
	}
	defer zr.Close()
	plain, err := io.ReadAll(io.LimitReader(zr, DefaultMaxSnapshotBytes+1))
	if err != nil || len(plain) > DefaultMaxSnapshotBytes || len(plain) < 4 {
		return meta, nil, fmt.Errorf("%w: cuerpo ilegible: %v", ErrCorruptSnapshot, err)
	}
	ml := int(binary.BigEndian.Uint32(plain[:4]))
	if ml > len(plain)-4 {
		return meta, nil, fmt.Errorf("%w: meta truncada", ErrCorruptSnapshot)
	}
	if err := json.Unmarshal(plain[4:4+ml], &meta); err != nil {
		return meta, nil, fmt.Errorf("%w: meta: %v", ErrCorruptSnapshot, err)
	}
	if expectKind != "" && meta.Kind != expectKind {
		return meta, nil, fmt.Errorf("%w: tipo %q, se esperaba %q", ErrCorruptSnapshot, meta.Kind, expectKind)
	}
	return meta, plain[4+ml:], nil
}

// SnapshotDir publica snapshots versionados en un directorio local
// (p. ej. store/catalog/reputation/, docs/storage.md §2.2):
//
//	<root>/v<N>/snapshot.hsnp      contenedor
//	<root>/v<N>/manifest.json      meta + SHA-256 y tamaño del archivo
//	<root>/latest                  "v<N>" de la última versión publicada
//
// La publicación en NATS Object Store (bucket *-snapshots) se hace sobre
// este mismo archivo cuando exista natsx (I0-04).
type SnapshotDir struct {
	Root string
}

// SnapshotManifest es el manifest.json de una versión publicada.
type SnapshotManifest struct {
	SnapshotMeta
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Latest devuelve la última versión publicada (ErrNotFound si no hay).
func (d SnapshotDir) Latest() (*SnapshotManifest, string, error) {
	b, err := os.ReadFile(filepath.Join(d.Root, "latest"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("%w: sin snapshots en %s", ErrNotFound, d.Root)
	}
	if err != nil {
		return nil, "", err
	}
	v := strings.TrimSpace(string(b))
	if _, ok := parseVersion(v); !ok {
		return nil, "", fmt.Errorf("puntero latest inválido %q", v)
	}
	mb, err := os.ReadFile(filepath.Join(d.Root, v, "manifest.json"))
	if err != nil {
		return nil, "", err
	}
	var m SnapshotManifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return nil, "", fmt.Errorf("manifest de %s: %w", v, err)
	}
	return &m, filepath.Join(d.Root, v, m.File), nil
}

func parseVersion(s string) (int, bool) {
	if !strings.HasPrefix(s, "v") {
		return 0, false
	}
	n, err := strconv.Atoi(s[1:])
	return n, err == nil && n > 0
}

func (d SnapshotDir) nextVersion() (int, error) {
	entries, err := os.ReadDir(d.Root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	next := 1
	for _, e := range entries {
		if n, ok := parseVersion(e.Name()); ok && e.IsDir() && n >= next {
			next = n + 1
		}
	}
	return next, nil
}

// Publish asigna la siguiente versión, codifica y escribe el snapshot y su
// manifiesto, y mueve el puntero latest al final (de forma atómica).
func (d SnapshotDir) Publish(meta SnapshotMeta, payload []byte) (*SnapshotManifest, string, error) {
	n, err := d.nextVersion()
	if err != nil {
		return nil, "", err
	}
	meta.Version = "v" + strconv.Itoa(n)
	var buf bytes.Buffer
	if err := EncodeSnapshot(&buf, meta, payload); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	m := &SnapshotManifest{SnapshotMeta: meta, File: "snapshot.hsnp", SHA256: hex.EncodeToString(sum[:]), Size: int64(buf.Len())}
	dir := filepath.Join(d.Root, meta.Version)
	if err := writeFileAtomic(filepath.Join(dir, m.File), buf.Bytes()); err != nil {
		return nil, "", err
	}
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, "", err
	}
	if err := writeFileAtomic(filepath.Join(dir, "manifest.json"), mb); err != nil {
		return nil, "", err
	}
	if err := writeFileAtomic(filepath.Join(d.Root, "latest"), []byte(meta.Version+"\n")); err != nil {
		return nil, "", err
	}
	return m, filepath.Join(dir, m.File), nil
}
