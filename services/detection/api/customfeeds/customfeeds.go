// Package customfeeds es el contrato de las listas de reputación
// personalizadas (D20): el superadmin, con permiso de plataforma, da de alta
// listas propias (URL, formato, frecuencia, categoría, confianza) que se
// descargan y entran en el snapshot de reputación junto a las del catálogo
// embebido (services/detection/internal/config/feeds.yaml).
//
// Las fuentes personalizadas llegan en tiempo de ejecución a través de un
// SourceProvider: un archivo YAML, un directorio de YAML o una función (la que
// usará el módulo con la tabla de PostgreSQL y la API de plataforma). Antes de
// usarse se validan con Validate de forma estricta y se convierten a
// datasets.Source con ToSource; Resolve hace las dos cosas y las combina con
// el catálogo.
//
// La API de plataforma debe llamar a Validate antes de guardar una fuente y
// devolver ValidationError.Problems como errores de campo.
package customfeeds

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/feeds"
)

// SourceKind es el kind de las fuentes de reputación.
const SourceKind = "reputation"

// IDPrefix es obligatorio en el id de una fuente personalizada: separa su
// espacio de nombres del catálogo (y de su directorio en el almacén de
// datasets), de modo que una fuente nueva del catálogo nunca pisa una
// personalizada existente.
const IDPrefix = "custom-"

// DefaultLicense es la licencia que se registra si el superadmin no indica
// otra: la responsabilidad del uso es de quien da de alta la lista (D20).
const DefaultLicense = "Lista personalizada dada de alta por el superadmin (D20)"

// SourceSpec es la definición de una lista personalizada tal como la da de
// alta el superadmin (API de plataforma, PostgreSQL, YAML).
type SourceSpec struct {
	ID     string `yaml:"id" json:"id"`
	Name   string `yaml:"name" json:"name"`
	URL    string `yaml:"url" json:"url"`
	Format string `yaml:"format" json:"format"`
	// Category: reputation.Category (botnet_cc, malware, spam, scanner,
	// blocklist, tor, proxy, mining, other).
	Category   string `yaml:"category" json:"category"`
	Confidence int    `yaml:"confidence" json:"confidence"`
	// Frequency: cada cuánto se descarga (mínimo Policy.MinFrequency).
	Frequency datasets.Duration `yaml:"frequency" json:"frequency"`
	// TTL: caducidad de una entrada desde su última aparición (0 = ninguna).
	TTL        datasets.Duration `yaml:"ttl,omitempty" json:"ttl,omitempty"`
	MaxBytes   int64             `yaml:"max_bytes,omitempty" json:"max_bytes,omitempty"`
	MinEntries int               `yaml:"min_entries,omitempty" json:"min_entries,omitempty"`
	MaxEntries int               `yaml:"max_entries,omitempty" json:"max_entries,omitempty"`
	// CSV: obligatorio con format "csv" y prohibido con los demás.
	CSV *datasets.CSVOptions `yaml:"csv,omitempty" json:"csv,omitempty"`
	// OnDangerous: "reject" (por defecto) rechaza la lista si incluye
	// entradas peligrosas; "warn" las descarta y avisa. Una ruta por defecto
	// o una cobertura excesiva la rechazan siempre.
	OnDangerous string `yaml:"on_dangerous,omitempty" json:"on_dangerous,omitempty"`
	Enabled     *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// License, LicenseURL y Attribution son opcionales (DefaultLicense).
	License     string `yaml:"license,omitempty" json:"license,omitempty"`
	LicenseURL  string `yaml:"license_url,omitempty" json:"license_url,omitempty"`
	Attribution string `yaml:"attribution,omitempty" json:"attribution,omitempty"`
	Notes       string `yaml:"notes,omitempty" json:"notes,omitempty"`
	// Auditoría (la rellena la API de plataforma).
	CreatedBy string    `yaml:"created_by,omitempty" json:"created_by,omitempty"`
	UpdatedAt time.Time `yaml:"updated_at,omitempty" json:"updated_at,omitzero"`
}

// Policy son los límites de validación de las fuentes personalizadas.
type Policy struct {
	MinFrequency, MaxFrequency time.Duration
	MaxTTL                     time.Duration
	// DefaultMaxBytes se aplica si la fuente no declara max_bytes; MaxBytes
	// es el máximo que puede declarar.
	DefaultMaxBytes, MaxBytes int64
	// DefaultMaxEntries / MaxEntries: ídem para max_entries.
	DefaultMaxEntries, MaxEntries int
	// Formats admitidos (nil = todos los que tienen parser).
	Formats []string
}

// DefaultPolicy devuelve los límites por defecto.
func DefaultPolicy() Policy {
	return Policy{
		MinFrequency: time.Hour, MaxFrequency: 7 * 24 * time.Hour,
		MaxTTL:          90 * 24 * time.Hour,
		DefaultMaxBytes: 32 << 20, MaxBytes: 256 << 20,
		DefaultMaxEntries: 200_000, MaxEntries: 2_000_000,
	}
}

func (p Policy) formats() []string {
	if p.Formats != nil {
		return p.Formats
	}
	return feeds.Formats()
}

// FieldError es un problema de validación de un campo de SourceSpec.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationError agrupa los problemas de una fuente.
type ValidationError struct {
	ID       string
	Problems []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		parts[i] = p.Field + ": " + p.Message
	}
	return fmt.Sprintf("fuente personalizada %q inválida: %s", e.ID, strings.Join(parts, "; "))
}

var idRe = regexp.MustCompile(`^custom-[a-z0-9][a-z0-9_-]{1,55}$`)

const (
	maxNameLen  = 120
	maxURLLen   = 2048
	maxNotesLen = 2000
	minMaxBytes = 1 << 10
)

// blockedHostSuffixes son nombres que solo resuelven en redes internas.
var blockedHostSuffixes = []string{".localhost", ".local", ".internal", ".lan", ".home.arpa", ".corp", ".intranet"}

// Validate comprueba una fuente personalizada. Devuelve *ValidationError.
func Validate(s SourceSpec, pol Policy) error {
	var probs []FieldError
	bad := func(field, format string, args ...any) {
		probs = append(probs, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
	}
	if !idRe.MatchString(s.ID) {
		bad("id", "debe empezar por %q y seguir con minúsculas, dígitos, '-' o '_' (3–56 caracteres)", IDPrefix)
	}
	switch n := utf8.RuneCountInString(strings.TrimSpace(s.Name)); {
	case n == 0:
		bad("name", "obligatorio")
	case n > maxNameLen:
		bad("name", "más de %d caracteres", maxNameLen)
	case hasControl(s.Name):
		bad("name", "contiene caracteres de control")
	}
	if err := checkSourceURL(s.URL); err != nil {
		bad("url", "%v", err)
	}
	switch {
	case !slices.Contains(pol.formats(), s.Format):
		bad("format", "%q no soportado (%s)", s.Format, strings.Join(pol.formats(), ", "))
	case s.Format == feeds.FormatCSV:
		if err := feeds.CheckCSVOptions(s.CSV); err != nil {
			bad("csv", "%v", err)
		}
	case s.CSV != nil:
		bad("csv", "solo con format %q", feeds.FormatCSV)
	}
	if !reputation.Category(s.Category).Valid() {
		bad("category", "%q no es una categoría de reputación", s.Category)
	}
	if s.Confidence < 1 || s.Confidence > 100 {
		bad("confidence", "debe estar entre 1 y 100")
	}
	freq := time.Duration(s.Frequency)
	if freq < pol.MinFrequency || freq > pol.MaxFrequency {
		bad("frequency", "%s fuera de %s–%s", freq, pol.MinFrequency, pol.MaxFrequency)
	}
	if ttl := time.Duration(s.TTL); ttl != 0 && (ttl < freq || ttl > pol.MaxTTL) {
		bad("ttl", "%s: 0 (sin caducidad) o entre la frecuencia y %s", ttl, pol.MaxTTL)
	}
	if s.MaxBytes != 0 && (s.MaxBytes < minMaxBytes || s.MaxBytes > pol.MaxBytes) {
		bad("max_bytes", "%d fuera de %d–%d", s.MaxBytes, minMaxBytes, pol.MaxBytes)
	}
	if s.MaxEntries != 0 && (s.MaxEntries < 1 || s.MaxEntries > pol.MaxEntries) {
		bad("max_entries", "%d fuera de 1–%d", s.MaxEntries, pol.MaxEntries)
	}
	if maxE := cmpOr(s.MaxEntries, pol.DefaultMaxEntries); s.MinEntries < 0 || s.MinEntries > maxE {
		bad("min_entries", "%d fuera de 0–%d", s.MinEntries, maxE)
	}
	switch s.OnDangerous {
	case "", datasets.DangerReject, datasets.DangerWarn:
	default:
		bad("on_dangerous", "%q inválido (reject|warn)", s.OnDangerous)
	}
	if s.LicenseURL != "" {
		if u, err := url.Parse(s.LicenseURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			bad("license_url", "URL http(s) inválida")
		}
	}
	if len(s.License) > maxNameLen*2 || hasControl(s.License) || hasControl(s.Attribution) {
		bad("license", "demasiado larga o con caracteres de control")
	}
	if len(s.Notes) > maxNotesLen {
		bad("notes", "más de %d bytes", maxNotesLen)
	}
	if len(probs) > 0 {
		return &ValidationError{ID: s.ID, Problems: probs}
	}
	return nil
}

func cmpOr(v, def int) int {
	if v != 0 {
		return v
	}
	return def
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\t' })
}

// checkSourceURL exige https, sin credenciales ni fragmento, y un host que
// no apunte a la red interna (protección básica frente a SSRF; la
// resolución DNS a IP interna se controla en el descargador).
func checkSourceURL(raw string) error {
	if raw == "" {
		return errors.New("obligatoria")
	}
	if len(raw) > maxURLLen {
		return fmt.Errorf("más de %d caracteres", maxURLLen)
	}
	if hasControl(raw) || strings.ContainsAny(raw, " \\") {
		return errors.New("contiene espacios o caracteres no permitidos")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("ilegible: %v", err)
	}
	if u.Scheme != "https" {
		return errors.New("solo se admite https")
	}
	if u.User != nil {
		return errors.New("no puede llevar credenciales (usuario:clave@)")
	}
	if u.Fragment != "" || u.Opaque != "" {
		return errors.New("no puede llevar fragmento (#…)")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return errors.New("falta el host")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("puerto %q inválido", p)
		}
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if a.Zone() != "" || feeds.CustomPolicy("").Classify(netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen())) != "" {
			return fmt.Errorf("el host %s no es una dirección pública", host)
		}
		return nil
	}
	if host == "localhost" || !strings.Contains(host, ".") {
		return fmt.Errorf("el host %q no es un nombre público", host)
	}
	for _, suf := range blockedHostSuffixes {
		if strings.HasSuffix(host, suf) {
			return fmt.Errorf("el host %q es de red interna", host)
		}
	}
	return nil
}

// ToSource convierte una fuente personalizada (ya validada) en la
// declaración que usa el motor de feeds: kind reputation, origen custom, uso
// comercial "yes" (autorizado por el superadmin con permiso de plataforma,
// D20) y los límites por defecto de la política.
func ToSource(s SourceSpec, pol Policy) datasets.Source {
	src := datasets.Source{
		ID: s.ID, Name: s.Name, Kind: SourceKind, URL: s.URL, Format: s.Format,
		License: s.License, LicenseURL: s.LicenseURL, CommercialUse: datasets.CommercialYes,
		Attribution: s.Attribution, Frequency: s.Frequency, Category: s.Category,
		Confidence: s.Confidence, TTL: s.TTL, MinEntries: max(s.MinEntries, 1),
		MaxBytes: s.MaxBytes, MaxEntries: s.MaxEntries, Enabled: s.Enabled, Notes: s.Notes,
		Origin: datasets.OriginCustom, OnDangerous: s.OnDangerous, CSV: s.CSV,
	}
	if src.License == "" {
		src.License = DefaultLicense
	}
	if src.LicenseURL == "" {
		src.LicenseURL = s.URL
	}
	if src.MaxBytes == 0 {
		src.MaxBytes = pol.DefaultMaxBytes
	}
	if src.MaxEntries == 0 {
		src.MaxEntries = pol.DefaultMaxEntries
	}
	if src.OnDangerous == "" {
		src.OnDangerous = datasets.DangerReject
	}
	return src
}

// SourceProvider entrega las fuentes personalizadas vigentes.
type SourceProvider interface {
	CustomSources(ctx context.Context) ([]SourceSpec, error)
}

// ProviderFunc adapta una función (p. ej. una consulta a PostgreSQL).
type ProviderFunc func(ctx context.Context) ([]SourceSpec, error)

// CustomSources implementa SourceProvider.
func (f ProviderFunc) CustomSources(ctx context.Context) ([]SourceSpec, error) { return f(ctx) }

// Static es una lista fija de fuentes.
type Static []SourceSpec

// CustomSources implementa SourceProvider.
func (s Static) CustomSources(context.Context) ([]SourceSpec, error) { return slices.Clone(s), nil }

// Multi concatena varios proveedores.
type Multi []SourceProvider

// CustomSources implementa SourceProvider.
func (m Multi) CustomSources(ctx context.Context) ([]SourceSpec, error) {
	var out []SourceSpec
	for _, p := range m {
		specs, err := p.CustomSources(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, specs...)
	}
	return out, nil
}

// File lee un YAML con `version: 1` y `sources:` (lista de SourceSpec), o
// una sola SourceSpec. Los campos desconocidos son un error.
type File struct{ Path string }

// CustomSources implementa SourceProvider.
func (f File) CustomSources(context.Context) ([]SourceSpec, error) {
	b, err := os.ReadFile(f.Path) //nolint:gosec // ruta de configuración del operador
	if err != nil {
		return nil, fmt.Errorf("leer %s: %w", f.Path, err)
	}
	specs, err := ParseYAML(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Path, err)
	}
	return specs, nil
}

// Dir lee todos los *.yaml / *.yml de un directorio (no recursivo, en orden
// alfabético), cada uno con el formato de File. Un directorio inexistente no
// es un error: no hay fuentes personalizadas.
type Dir struct{ Path string }

// CustomSources implementa SourceProvider.
func (d Dir) CustomSources(ctx context.Context) ([]SourceSpec, error) {
	entries, err := os.ReadDir(d.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []SourceSpec
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !e.Type().IsRegular() || (ext != ".yaml" && ext != ".yml") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		specs, err := File{Path: filepath.Join(d.Path, e.Name())}.CustomSources(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, specs...)
	}
	return out, nil
}

// PathProvider devuelve Dir si path es un directorio y File si no.
func PathProvider(path string) (SourceProvider, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return Dir{Path: path}, nil
	}
	return File{Path: path}, nil
}

type fileDoc struct {
	Version int          `yaml:"version"`
	Sources []SourceSpec `yaml:"sources"`
}

// ParseYAML interpreta un documento con `version: 1` y `sources:`, o una
// sola SourceSpec. No valida las fuentes (eso lo hace Resolve).
func ParseYAML(b []byte) ([]SourceSpec, error) {
	var probe map[string]any
	if err := yaml.Unmarshal(b, &probe); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	if len(probe) == 0 {
		return nil, nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if _, ok := probe["sources"]; ok {
		var doc fileDoc
		if err := dec.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("yaml: %w", err)
		}
		if doc.Version != 1 {
			return nil, fmt.Errorf("versión %d no soportada (se espera 1)", doc.Version)
		}
		return doc.Sources, nil
	}
	var s SourceSpec
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	return []SourceSpec{s}, nil
}

// Rejected es una fuente personalizada que no se carga.
type Rejected struct {
	ID  string
	Err error
}

// Resolve combina el catálogo con las fuentes personalizadas de p. Las que
// no pasan Validate, o repiten un id, se devuelven en rejected y no se
// cargan; el resto se añade tras el catálogo. Solo falla si p falla o si el
// catálogo usa el prefijo reservado IDPrefix.
func Resolve(ctx context.Context, catalog []datasets.Source, p SourceProvider, pol Policy) (all []datasets.Source, rejected []Rejected, err error) {
	seen := map[string]bool{}
	for _, s := range catalog {
		if strings.HasPrefix(s.ID, IDPrefix) && !s.IsCustom() {
			return nil, nil, fmt.Errorf("fuente del catálogo %q usa el prefijo reservado %q", s.ID, IDPrefix)
		}
		seen[s.ID] = true
	}
	all = slices.Clone(catalog)
	if p == nil {
		return all, nil, nil
	}
	specs, err := p.CustomSources(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("fuentes personalizadas: %w", err)
	}
	for _, s := range specs {
		if err := Validate(s, pol); err != nil {
			rejected = append(rejected, Rejected{ID: s.ID, Err: err})
			continue
		}
		if seen[s.ID] {
			rejected = append(rejected, Rejected{ID: s.ID, Err: fmt.Errorf("id %q duplicado", s.ID)})
			continue
		}
		seen[s.ID] = true
		all = append(all, ToSource(s, pol))
	}
	return all, rejected, nil
}
