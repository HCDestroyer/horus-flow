// Package remote implementa la copia remota de los backups locales (ADR-0019
// con la nota de 2026-10-12, docs/storage.md §4): destinos SFTP de plataforma
// con credenciales write-only cifradas (KEK de jobs), confirmación TOFU de la
// clave del servidor, y un motor que sube los archivos nuevos o cambiados de
// backups/postgres, backups/clickhouse y reports/ (según las rutas
// seleccionadas en cada destino) cifrados con age en Horus (el destino
// nunca ve datos en claro), con ledger en PostgreSQL y retención remota.
//
// Regla de ADR-0019 §3: nada de Horus depende del destino. Un fallo solo
// actualiza el estado del destino y las métricas.
package remote

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
)

// PermManage es el permiso de plataforma de todas las rutas de destinos.
const PermManage = "platform.storage.manage"

// Códigos de error del contrato (common.yaml ErrorCode).
const (
	CodeNotFound    = "REMOTE_DESTINATION_NOT_FOUND"
	CodeNotReady    = "REMOTE_DESTINATION_NOT_READY"
	CodeSyncRunning = "REMOTE_DESTINATION_SYNC_RUNNING"
)

// Estados (RemoteDestination.status).
const (
	StatusOK       = "ok"
	StatusLagging  = "lagging"
	StatusFailing  = "failing"
	StatusDisabled = "disabled"
	StatusPending  = "pending"
)

// Etapas de la prueba de conexión (RemoteDestinationTestResult.stage).
const (
	StageConnect = "connect"
	StageHostKey = "host_key"
	StageAuth    = "auth"
	StageWrite   = "write"
	StageDone    = "done"
)

// Límites del contrato.
const (
	MaxNameLen       = 120
	MaxHostLen       = 253
	MaxUsernameLen   = 128
	MaxPathLen       = 1024
	MinRetentionDays = 1
	MaxRetentionDays = 3650
	DefaultPort      = 22
	DefaultRetention = 30
	MaxPasswordLen   = 1024
	MaxPrivateKeyLen = 16384
)

// Rutas del almacén que un destino puede replicar (RemoteDestination.paths,
// ADR-0019 §2 "qué rutas replicar"): backups = backups/postgres y
// backups/clickhouse; reports = reports/<tenant_id>/ (PDF y CSV del rol
// reporting). Todas viajan cifradas con age igual que los backups.
const (
	PathBackups = "backups"
	PathReports = "reports"
)

// AllPaths son las rutas admitidas, en orden canónico; también el valor por
// defecto de un destino nuevo.
var AllPaths = []string{PathBackups, PathReports}

// NormalizePaths valida y ordena una selección de rutas (sin duplicados, al
// menos una). ok=false si trae algo desconocido o está vacía.
func NormalizePaths(in []string) ([]string, bool) {
	if len(in) == 0 {
		return nil, false
	}
	for _, p := range in {
		if !slices.Contains(AllPaths, p) {
			return nil, false
		}
	}
	var out []string
	for _, p := range AllPaths {
		if slices.Contains(in, p) {
			out = append(out, p)
		}
	}
	return out, true
}

func pathsError() problem.FieldError {
	return fieldErr("paths", "INVALID_VALUE", "Una o más de: "+strings.Join(AllPaths, ", ")+".")
}

// Destination es un destino remoto (fila de jobs.remote_destination, sin el
// secreto en claro).
type Destination struct {
	ID            uuid.UUID
	Kind          string
	Name          string
	Host          string
	Port          int
	Username      string
	Path          string
	RetentionDays int
	// Paths: rutas del almacén que replica (AllPaths).
	Paths              []string
	Enabled            bool
	HostKeyFingerprint *string
	// Credencial sellada (envelope); nil = sin credenciales.
	CredentialCiphertext []byte
	CredentialDEK        []byte
	CredentialKEKID      string
	CredentialKind       string
	VerifiedAt           *time.Time
	LastSuccessAt        *time.Time
	LastAttemptAt        *time.Time
	LastError            *string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	Version              int
}

// HasCredentials indica si el destino tiene credenciales guardadas.
func (d *Destination) HasCredentials() bool { return len(d.CredentialCiphertext) > 0 }

// Ready indica si el destino puede copiar: habilitado, con credenciales y
// huella confirmada.
func (d *Destination) Ready() bool {
	return d.Enabled && d.HasCredentials() && d.HostKeyFingerprint != nil && *d.HostKeyFingerprint != ""
}

// CreateInput es RemoteDestinationCreate.
type CreateInput struct {
	Kind          *string   `json:"kind"`
	Name          *string   `json:"name"`
	Host          *string   `json:"host"`
	Port          *int      `json:"port"`
	Username      *string   `json:"username"`
	Path          *string   `json:"path"`
	RetentionDays *int      `json:"retention_days"`
	Paths         *[]string `json:"paths"`
}

// Patch es RemoteDestinationPatch.
type Patch struct {
	Name          *string   `json:"name"`
	Host          *string   `json:"host"`
	Port          *int      `json:"port"`
	Username      *string   `json:"username"`
	Path          *string   `json:"path"`
	RetentionDays *int      `json:"retention_days"`
	Paths         *[]string `json:"paths"`
	Enabled       *bool     `json:"enabled"`
}

// Empty indica que el PATCH no trae ningún campo.
func (p *Patch) Empty() bool {
	return p.Name == nil && p.Host == nil && p.Port == nil && p.Username == nil && p.Path == nil &&
		p.RetentionDays == nil && p.Paths == nil && p.Enabled == nil
}

// Credentials es el cuerpo de PUT .../credentials (write-only).
type Credentials struct {
	Password   *string `json:"password,omitempty"`
	PrivateKey *string `json:"private_key,omitempty"`
	Passphrase *string `json:"passphrase,omitempty"`
}

// Kind resume el tipo de credencial (columna credential_kind; auditable, sin secreto).
func (c *Credentials) Kind() string {
	switch {
	case c.PrivateKey != nil && c.Password != nil:
		return "private_key+password"
	case c.PrivateKey != nil:
		return "private_key"
	default:
		return "password"
	}
}

func printable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ValidName comprueba un nombre (1–120 caracteres imprimibles).
func ValidName(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && utf8.RuneCountInString(s) <= MaxNameLen && printable(s)
}

// ValidHost acepta un nombre DNS o una IP literal (IPv4 o IPv6 sin
// corchetes), sin esquema, usuario, puerto ni ruta.
func ValidHost(s string) bool {
	if s == "" || len(s) > MaxHostLen {
		return false
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Zone() == "" && !a.IsUnspecified()
	}
	name := strings.TrimSuffix(s, ".")
	if name == "" {
		return false
	}
	labels := strings.Split(name, ".")
	allNumeric := true
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			switch {
			case c >= '0' && c <= '9':
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
				allNumeric = false
			default:
				return false
			}
		}
	}
	// "1.2.3" o "999.1.1.1" no son IPs válidas ni nombres razonables.
	return !allNumeric
}

// ValidUsername: 1–128 caracteres imprimibles sin espacios.
func ValidUsername(s string) bool {
	if s == "" || utf8.RuneCountInString(s) > MaxUsernameLen || !printable(s) {
		return false
	}
	return !strings.ContainsFunc(s, unicode.IsSpace)
}

// NormalizePath valida la ruta remota (absoluta o relativa al directorio del
// usuario, sin segmentos ".." ni caracteres de control) y quita las barras
// finales. ok=false si no es válida.
func NormalizePath(s string) (string, bool) {
	if s == "" || len(s) > MaxPathLen || !printable(s) || strings.Contains(s, "\\") {
		return "", false
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == ".." {
			return "", false
		}
	}
	if t := strings.TrimRight(s, "/"); t != "" {
		s = t
	} else {
		s = "/"
	}
	return s, true
}

func fieldErr(field, code, msg string) problem.FieldError { return apperr.Field(field, code, msg) }

// ValidateCreate valida el alta y devuelve el destino (sin id ni fechas).
func ValidateCreate(in CreateInput) (*Destination, error) {
	var fields []problem.FieldError
	d := &Destination{Kind: "sftp", Port: DefaultPort, RetentionDays: DefaultRetention, Paths: slices.Clone(AllPaths)}
	switch {
	case in.Kind == nil:
		fields = append(fields, fieldErr("kind", "REQUIRED", "Obligatorio."))
	case *in.Kind != "sftp":
		fields = append(fields, fieldErr("kind", "INVALID_VALUE", "Por ahora solo se admiten destinos sftp."))
	}
	if in.Name == nil {
		fields = append(fields, fieldErr("name", "REQUIRED", "Obligatorio."))
	} else if !ValidName(*in.Name) {
		fields = append(fields, fieldErr("name", "INVALID_VALUE", fmt.Sprintf("Entre 1 y %d caracteres imprimibles.", MaxNameLen)))
	} else {
		d.Name = strings.TrimSpace(*in.Name)
	}
	if in.Host == nil {
		fields = append(fields, fieldErr("host", "REQUIRED", "Obligatorio."))
	} else if h := strings.TrimSpace(*in.Host); !ValidHost(h) {
		fields = append(fields, hostError())
	} else {
		d.Host = h
	}
	if in.Port != nil {
		if *in.Port < 1 || *in.Port > 65535 {
			fields = append(fields, fieldErr("port", "OUT_OF_RANGE", "Entre 1 y 65535."))
		} else {
			d.Port = *in.Port
		}
	}
	if in.Username == nil {
		fields = append(fields, fieldErr("username", "REQUIRED", "Obligatorio."))
	} else if !ValidUsername(*in.Username) {
		fields = append(fields, usernameError())
	} else {
		d.Username = *in.Username
	}
	if in.Path == nil {
		fields = append(fields, fieldErr("path", "REQUIRED", "Obligatorio."))
	} else if p, ok := NormalizePath(*in.Path); !ok {
		fields = append(fields, pathError())
	} else {
		d.Path = p
	}
	if in.RetentionDays != nil {
		if *in.RetentionDays < MinRetentionDays || *in.RetentionDays > MaxRetentionDays {
			fields = append(fields, retentionError())
		} else {
			d.RetentionDays = *in.RetentionDays
		}
	}
	if in.Paths != nil {
		if ps, ok := NormalizePaths(*in.Paths); !ok {
			fields = append(fields, pathsError())
		} else {
			d.Paths = ps
		}
	}
	if len(fields) > 0 {
		return nil, apperr.Validation(fields...)
	}
	return d, nil
}

func hostError() problem.FieldError {
	return fieldErr("host", "INVALID_FORMAT", "Nombre DNS o IP, sin esquema, usuario, puerto ni ruta (p. ej. nas.isp.example o 192.0.2.10).")
}

func usernameError() problem.FieldError {
	return fieldErr("username", "INVALID_VALUE", fmt.Sprintf("Entre 1 y %d caracteres, sin espacios.", MaxUsernameLen))
}

func pathError() problem.FieldError {
	return fieldErr("path", "INVALID_FORMAT", "Ruta absoluta o relativa a la carpeta del usuario, sin '..' ni caracteres de control.")
}

func retentionError() problem.FieldError {
	return fieldErr("retention_days", "OUT_OF_RANGE", fmt.Sprintf("Entre %d y %d días.", MinRetentionDays, MaxRetentionDays))
}

// Changes es el resultado de aplicar un PATCH.
type Changes struct {
	// Fields son los campos que cambian, con {from, to} (para la auditoría).
	Fields map[string]any
	// ResetHostKey: host o puerto cambian → se borra la huella, se deshabilita
	// y hay que volver a probar.
	ResetHostKey bool
	// ResetLedger: otro servidor, usuario o ruta → lo copiado ya no cuenta.
	ResetLedger bool
	// Unverify: hay que volver a probar antes de habilitar.
	Unverify bool
}

// ApplyPatch valida p contra cur y devuelve el destino resultante. Habilitar
// exige credenciales, huella confirmada y una prueba correcta desde el último
// cambio de conexión o credenciales.
func ApplyPatch(cur *Destination, p Patch) (*Destination, Changes, error) {
	ch := Changes{Fields: map[string]any{}}
	if p.Empty() {
		return nil, ch, apperr.New(apperr.KindInvalid, problem.CodeValidationFailed, "Indique al menos un campo.")
	}
	next := *cur
	var fields []problem.FieldError
	diff := func(field string, from, to any) {
		if from != to {
			ch.Fields[field] = map[string]any{"from": from, "to": to}
		}
	}
	if p.Name != nil {
		if !ValidName(*p.Name) {
			fields = append(fields, fieldErr("name", "INVALID_VALUE", fmt.Sprintf("Entre 1 y %d caracteres imprimibles.", MaxNameLen)))
		} else {
			next.Name = strings.TrimSpace(*p.Name)
		}
	}
	if p.Host != nil {
		if h := strings.TrimSpace(*p.Host); !ValidHost(h) {
			fields = append(fields, hostError())
		} else {
			next.Host = h
		}
	}
	if p.Port != nil {
		if *p.Port < 1 || *p.Port > 65535 {
			fields = append(fields, fieldErr("port", "OUT_OF_RANGE", "Entre 1 y 65535."))
		} else {
			next.Port = *p.Port
		}
	}
	if p.Username != nil {
		if !ValidUsername(*p.Username) {
			fields = append(fields, usernameError())
		} else {
			next.Username = *p.Username
		}
	}
	if p.Path != nil {
		if np, ok := NormalizePath(*p.Path); !ok {
			fields = append(fields, pathError())
		} else {
			next.Path = np
		}
	}
	if p.RetentionDays != nil {
		if *p.RetentionDays < MinRetentionDays || *p.RetentionDays > MaxRetentionDays {
			fields = append(fields, retentionError())
		} else {
			next.RetentionDays = *p.RetentionDays
		}
	}
	if p.Paths != nil {
		if ps, ok := NormalizePaths(*p.Paths); !ok {
			fields = append(fields, pathsError())
		} else {
			next.Paths = ps
		}
	}
	if len(fields) > 0 {
		return nil, ch, apperr.Validation(fields...)
	}
	diff("name", cur.Name, next.Name)
	diff("host", cur.Host, next.Host)
	diff("port", cur.Port, next.Port)
	diff("username", cur.Username, next.Username)
	diff("path", cur.Path, next.Path)
	diff("retention_days", cur.RetentionDays, next.RetentionDays)
	// Quitar una ruta no borra nada del destino: sus copias siguen en el
	// ledger y solo las retira la retención remota cuando el original ya no
	// existe en local (como cualquier otra copia).
	if !slices.Equal(cur.Paths, next.Paths) {
		ch.Fields["paths"] = map[string]any{"from": cur.Paths, "to": next.Paths}
	}
	if next.Host != cur.Host || next.Port != cur.Port {
		ch.ResetHostKey, ch.ResetLedger, ch.Unverify = true, true, true
		next.HostKeyFingerprint = nil
		next.Enabled = false
		next.VerifiedAt = nil
	}
	if next.Username != cur.Username || next.Path != cur.Path {
		ch.ResetLedger = true
	}
	if next.Username != cur.Username {
		ch.Unverify = true
		next.VerifiedAt = nil
	}
	if p.Enabled != nil && !ch.ResetHostKey {
		if *p.Enabled && !cur.Enabled {
			switch {
			case !next.HasCredentials():
				return nil, ch, notReady("Ponga las credenciales (PUT .../credentials) antes de habilitar el destino.")
			case next.HostKeyFingerprint == nil:
				return nil, ch, notReady("Pruebe la conexión y confirme la huella del servidor antes de habilitar el destino.")
			case next.VerifiedAt == nil:
				return nil, ch, notReady("Pruebe la conexión (POST .../test) antes de habilitar el destino.")
			}
		}
		next.Enabled = *p.Enabled
	}
	diff("enabled", cur.Enabled, next.Enabled)
	if ch.ResetHostKey && cur.HostKeyFingerprint != nil {
		ch.Fields["host_key_fingerprint"] = map[string]any{"from": *cur.HostKeyFingerprint, "to": nil}
	}
	return &next, ch, nil
}

func notReady(detail string) error {
	return &apperr.Error{Kind: apperr.KindConflict, Code: CodeNotReady, Title: "El destino no está listo", Detail: detail}
}

// ComputeStatus calcula el estado y el retraso (segundos desde la última
// copia correcta) de un destino:
//
//	disabled  si no está habilitado;
//	failing   si el último intento falló (después de la última copia correcta);
//	pending   si aún no hay ninguna copia correcta;
//	lagging   si la última copia correcta es más antigua que lagAfter
//	          (2× el calendario de backups: 26 h por defecto);
//	ok        en otro caso.
func ComputeStatus(d *Destination, now time.Time, lagAfter time.Duration) (string, *int64) {
	var lag *int64
	if d.LastSuccessAt != nil {
		s := int64(now.Sub(*d.LastSuccessAt) / time.Second)
		if s < 0 {
			s = 0
		}
		lag = &s
	}
	switch {
	case !d.Enabled:
		return StatusDisabled, lag
	case d.LastError != nil && d.LastAttemptAt != nil && (d.LastSuccessAt == nil || d.LastAttemptAt.After(*d.LastSuccessAt)):
		return StatusFailing, lag
	case d.LastSuccessAt == nil:
		return StatusPending, lag
	case now.Sub(*d.LastSuccessAt) > lagAfter:
		return StatusLagging, lag
	default:
		return StatusOK, lag
	}
}
