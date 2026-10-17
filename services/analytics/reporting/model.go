package reporting

import (
	"slices"
	"time"

	"github.com/google/uuid"
)

// Tipos de reporte (enum abierto del contrato; I3 solo trae el semanal de seguridad).
const KindSecurityWeekly = "security_weekly"

// Kinds son los tipos admitidos.
var Kinds = []string{KindSecurityWeekly}

// Estados de una ejecución.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

// Formatos de archivo.
const (
	FormatPDF = "pdf"
	FormatCSV = "csv"
)

// Formats son los formatos admitidos, en orden canónico.
var Formats = []string{FormatPDF, FormatCSV}

// normFormats valida y ordena una lista de formatos (vacía = todos).
func normFormats(in []string) ([]string, bool) {
	if len(in) == 0 {
		return slices.Clone(Formats), true
	}
	var out []string
	for _, f := range Formats {
		if slices.Contains(in, f) {
			out = append(out, f)
		}
	}
	for _, f := range in {
		if !slices.Contains(Formats, f) {
			return nil, false
		}
	}
	return out, true
}

// Definition es la programación de un tipo de reporte para un ISP
// (analytics.report_definition): una por (tenant, kind).
type Definition struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	Kind       string
	Enabled    bool
	Weekday    int // ISO: 1 = lunes
	Hour       int
	Minute     int
	Timezone   string
	Formats    []string
	Recipients []string
	// TenantName y TenantSlug: copia del directorio de auth (portada del PDF
	// y enlace del aviso por correo cuando auth no está en el proceso).
	TenantName string
	TenantSlug string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Version    int
}

// Schedule devuelve la programación con su zona horaria.
func (d *Definition) Schedule() Schedule {
	return Schedule{Weekday: d.Weekday, Hour: d.Hour, Minute: d.Minute, Loc: LoadLocation(d.Timezone)}
}

// Valores por defecto de una definición nueva (lunes 07:00 hora del ISP, PDF y CSV).
const (
	DefaultWeekday = 1
	DefaultHour    = 7
	DefaultMinute  = 0
)

// File es un archivo generado de una ejecución.
type File struct {
	Format    string `json:"format"`
	ObjectKey string `json:"object_key"` // relativo a HORUS_DATA_DIR: reports/<tenant_id>/<run_id>.<ext>
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

// Run es una ejecución (analytics.report_run).
type Run struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	DefinitionID *uuid.UUID // nil = a petición
	Kind         string
	Status       string
	PeriodFrom   time.Time
	PeriodTo     time.Time
	Timezone     string
	Formats      []string
	Files        []File
	Error        *string
	CreatedBy    *uuid.UUID // nil = programado
	Attempts     int
	CreatedAt    time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
	ExpiresAt    *time.Time
	PurgedAt     *time.Time
}

// Period devuelve el periodo de la ejecución.
func (r *Run) Period() Period {
	return Period{From: r.PeriodFrom, To: r.PeriodTo, Loc: LoadLocation(r.Timezone)}
}

// File devuelve el archivo de un formato.
func (r *Run) File(format string) (File, bool) {
	for _, f := range r.Files {
		if f.Format == format {
			return f, true
		}
	}
	return File{}, false
}

// SizeBytes es la suma de los archivos.
func (r *Run) SizeBytes() int64 {
	var n int64
	for _, f := range r.Files {
		n += f.SizeBytes
	}
	return n
}
