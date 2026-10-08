// Package datasets contiene la maquinaria común para descargar, validar,
// versionar y empaquetar datasets externos (IP→ASN, feeds de reputación…):
// declaración de fuentes con su licencia, descarga detrás de una interfaz,
// almacén local versionado con SHA-256 y manifiesto, conservación del último
// dataset válido cuando una fuente cae o llega corrupta, métricas de antigüedad
// y el contenedor binario versionado de los snapshots.
//
// No contiene lógica de dominio: el formato de cada fuente lo interpreta el
// módulo dueño (services/detection, services/traffic) a través de Validator.
package datasets

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

// CommercialUse indica si la licencia o los términos de la fuente permiten el
// uso comercial multi-ISP de Horus (P-14, ADR-0024 §3).
type CommercialUse string

const (
	// CommercialYes: la licencia lo permite expresamente; la fuente se descarga.
	CommercialYes CommercialUse = "yes"
	// CommercialNo: no lo permite; la fuente nunca se descarga.
	CommercialNo CommercialUse = "no"
	// CommercialUnverified: términos ambiguos o cambiantes pendientes de
	// revisión por el PO; no se descarga salvo autorización explícita
	// (p. ej. laboratorio con --allow-unverified).
	CommercialUnverified CommercialUse = "unverified"
)

// Duration es un time.Duration que se lee de YAML como "24h", "30m"…
type Duration time.Duration

// UnmarshalYAML implementa yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("duración %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML implementa yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// Source declara una fuente externa. Los campos de licencia son obligatorios:
// una fuente sin licencia trazable no se puede declarar.
type Source struct {
	ID            string        `yaml:"id"`
	Name          string        `yaml:"name"`
	Kind          string        `yaml:"kind"`
	URL           string        `yaml:"url"`
	Format        string        `yaml:"format"`
	License       string        `yaml:"license"`
	LicenseURL    string        `yaml:"license_url"`
	CommercialUse CommercialUse `yaml:"commercial_use"`
	Attribution   string        `yaml:"attribution,omitempty"`
	Frequency     Duration      `yaml:"frequency"`
	// Category y Confidence (0–100) solo aplican a feeds de reputación.
	Category   string `yaml:"category,omitempty"`
	Confidence int    `yaml:"confidence,omitempty"`
	// TTL: caducidad de una entrada desde su última aparición (0 = sin caducidad).
	TTL Duration `yaml:"ttl,omitempty"`
	// MinEntries: por debajo, el archivo se considera vacío o truncado.
	MinEntries int `yaml:"min_entries,omitempty"`
	// MaxBytes: tamaño máximo aceptado de la descarga (0 = DefaultMaxBytes).
	MaxBytes int64  `yaml:"max_bytes,omitempty"`
	Enabled  *bool  `yaml:"enabled,omitempty"`
	Notes    string `yaml:"notes,omitempty"`
}

// DefaultMaxBytes limita una descarga si la fuente no declara MaxBytes.
const DefaultMaxBytes int64 = 512 << 20

// Config es el contenido de un archivo de fuentes (p. ej. config/feeds.yaml).
type Config struct {
	Version int      `yaml:"version"`
	Sources []Source `yaml:"sources"`
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,62}$`)

// LoadConfig lee y valida un archivo de fuentes.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path) //nolint:gosec // ruta de configuración del operador
	if err != nil {
		return nil, fmt.Errorf("leer %s: %w", path, err)
	}
	c, err := ParseConfig(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// ParseConfig interpreta y valida un archivo de fuentes en YAML.
func ParseConfig(b []byte) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate comprueba que cada fuente declara URL, licencia, uso comercial,
// frecuencia y formato (criterio 1 de I0-17).
func (c *Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("versión de configuración %d no soportada (se espera 1)", c.Version)
	}
	seen := map[string]bool{}
	var errs []error
	for i, s := range c.Sources {
		if err := s.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("fuente #%d (%s): %w", i, s.ID, err))
		}
		if seen[s.ID] {
			errs = append(errs, fmt.Errorf("fuente %q duplicada", s.ID))
		}
		seen[s.ID] = true
	}
	return errors.Join(errs...)
}

// Validate comprueba los campos obligatorios de una fuente.
func (s Source) Validate() error {
	var errs []error
	if !idRe.MatchString(s.ID) {
		errs = append(errs, fmt.Errorf("id %q inválido (minúsculas, dígitos, '-' o '_')", s.ID))
	}
	if s.Kind == "" {
		errs = append(errs, errors.New("falta kind"))
	}
	if u, err := url.Parse(s.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		errs = append(errs, fmt.Errorf("url %q inválida (http/https)", s.URL))
	}
	if s.Format == "" {
		errs = append(errs, errors.New("falta format"))
	}
	if s.License == "" || s.LicenseURL == "" {
		errs = append(errs, errors.New("faltan license y/o license_url"))
	}
	switch s.CommercialUse {
	case CommercialYes, CommercialNo, CommercialUnverified:
	default:
		errs = append(errs, fmt.Errorf("commercial_use %q inválido (yes|no|unverified)", s.CommercialUse))
	}
	if s.Frequency <= 0 {
		errs = append(errs, errors.New("falta frequency"))
	}
	if s.Confidence < 0 || s.Confidence > 100 {
		errs = append(errs, fmt.Errorf("confidence %d fuera de 0–100", s.Confidence))
	}
	if s.MinEntries < 0 || s.MaxBytes < 0 || s.TTL < 0 {
		errs = append(errs, errors.New("min_entries, max_bytes y ttl no pueden ser negativos"))
	}
	return errors.Join(errs...)
}

// IsEnabled indica si la fuente está activa (por defecto sí).
func (s Source) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// Allowed decide si la fuente puede descargarse según su licencia. Las de uso
// comercial "no" nunca se descargan; las "unverified" solo con allowUnverified.
func (s Source) Allowed(allowUnverified bool) (bool, string) {
	switch {
	case !s.IsEnabled():
		return false, "desactivada en la configuración"
	case s.CommercialUse == CommercialNo:
		return false, "licencia sin uso comercial permitido"
	case s.CommercialUse == CommercialUnverified && !allowUnverified:
		return false, "uso comercial sin verificar (requiere decisión del PO, P-14)"
	}
	return true, ""
}

// MaxSize devuelve el límite de tamaño efectivo de la descarga.
func (s Source) MaxSize() int64 {
	if s.MaxBytes > 0 {
		return s.MaxBytes
	}
	return DefaultMaxBytes
}

// ByKind filtra las fuentes de un tipo.
func (c *Config) ByKind(kind string) []Source {
	var out []Source
	for _, s := range c.Sources {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}
