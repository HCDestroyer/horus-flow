// Package pgdb es la librería común de acceso a PostgreSQL de los módulos
// (docs/database.md §1.2 y §3, ADR-0017):
//
//   - [Open] abre un pool pgx desde HORUS_POSTGRES_DSN (+ contraseña de
//     HORUS_POSTGRES_PASSWORD_FILE).
//   - [DB.TenantTx] abre cada transacción de negocio con
//     `SET LOCAL ROLE <svc>_app` y `SET LOCAL horus.tenant_id` (RLS
//     fail-closed: sin tenant, cero filas). El tenant es un tipo obligatorio
//     en la firma: no existe una transacción de negocio sin tenant.
//   - [DB.PlatformTx] usa el rol `<svc>_platform` (BYPASSRLS) para los
//     métodos multi-tenant declarados y auditados del módulo.
//   - [Migrate] aplica las migraciones goose embebidas del módulo con la
//     tabla de control `<esquema>.goose_db_version`.
//
// `SET LOCAL` muere con la transacción: compatible con pgbouncer en modo
// transacción y con pools compartidos.
package pgdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config es la conexión de un módulo.
type Config struct {
	// DSN postgres://usuario@host:puerto/base?sslmode=...
	DSN string
	// Password sustituye a la del DSN si no está vacía (HORUS_POSTGRES_PASSWORD_FILE).
	Password string //nolint:gosec // se lee de un archivo de secretos; nunca se registra
	// AppRole es el rol sin BYPASSRLS de las transacciones de negocio
	// (`<svc>_app`); vacío = el usuario del DSN.
	AppRole string
	// PlatformRole es el rol con BYPASSRLS de los procesos de plataforma
	// (`<svc>_platform`); vacío = el usuario del DSN.
	PlatformRole string
	// MaxConns del pool (0 = valor por defecto de pgx).
	MaxConns int32
	// StatementTimeout por transacción (0 = sin límite propio).
	StatementTimeout time.Duration
}

// DB es el pool de un módulo con sus roles.
type DB struct {
	Pool             *pgxpool.Pool
	appRole          string
	platformRole     string
	statementTimeout time.Duration
}

var identRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// Open abre el pool (sin conectar todavía: la primera conexión se abre al
// usarlo; Ping la fuerza).
func Open(ctx context.Context, cfg Config) (*DB, error) {
	if cfg.DSN == "" {
		return nil, errors.New("pgdb: empty DSN")
	}
	for _, r := range []string{cfg.AppRole, cfg.PlatformRole} {
		if r != "" && !identRe.MatchString(r) {
			return nil, fmt.Errorf("pgdb: invalid role name %q", r)
		}
	}
	pc, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("pgdb: parse DSN: %w", err)
	}
	if cfg.Password != "" {
		pc.ConnConfig.Password = cfg.Password
	}
	if cfg.MaxConns > 0 {
		pc.MaxConns = cfg.MaxConns
	}
	if pc.ConnConfig.RuntimeParams == nil {
		pc.ConnConfig.RuntimeParams = map[string]string{}
	}
	pc.ConnConfig.RuntimeParams["application_name"] = "horus"
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("pgdb: pool: %w", err)
	}
	return &DB{Pool: pool, appRole: cfg.AppRole, platformRole: cfg.PlatformRole, statementTimeout: cfg.StatementTimeout}, nil
}

// Close cierra el pool.
func (d *DB) Close() {
	if d != nil && d.Pool != nil {
		d.Pool.Close()
	}
}

// Ping comprueba la conexión (chequeo de /readyz).
func (d *DB) Ping(ctx context.Context) error {
	if err := d.Pool.Ping(ctx); err != nil {
		return fmt.Errorf("pgdb: ping: %w", err)
	}
	return nil
}

// TenantID es el tenant de una transacción de negocio. Un valor cero no es
// válido: [DB.TenantTx] lo rechaza (fail-closed también en la aplicación).
type TenantID uuid.UUID

// UUID devuelve el uuid.
func (t TenantID) UUID() uuid.UUID { return uuid.UUID(t) }

// String implementa fmt.Stringer.
func (t TenantID) String() string { return uuid.UUID(t).String() }

// ErrNoTenant indica una transacción de negocio sin tenant.
var ErrNoTenant = errors.New("pgdb: tenant transaction without tenant")

// TenantTx ejecuta fn en una transacción con el rol de la aplicación y
// `horus.tenant_id` fijado. Si fn devuelve error, se revierte.
func (d *DB) TenantTx(ctx context.Context, tenant TenantID, fn func(pgx.Tx) error) error {
	if uuid.UUID(tenant) == uuid.Nil {
		return ErrNoTenant
	}
	return d.tx(ctx, d.appRole, tenant.String(), fn)
}

// AppTx ejecuta fn con el rol de la aplicación pero sin tenant: las tablas
// con RLS devuelven cero filas. Solo para tablas sin tenant (catálogos,
// identidad global) y para probar el comportamiento fail-closed.
func (d *DB) AppTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return d.tx(ctx, d.appRole, "", fn)
}

// PlatformTx ejecuta fn con el rol de plataforma (BYPASSRLS). Solo para
// métodos multi-tenant declarados del módulo (identidad, auditoría,
// relay del outbox, reconciliaciones).
func (d *DB) PlatformTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return d.tx(ctx, d.platformRole, "", fn)
}

func (d *DB) tx(ctx context.Context, role, tenant string, fn func(pgx.Tx) error) (err error) {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgdb: begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if role != "" {
		if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
			return fmt.Errorf("pgdb: set role: %w", err)
		}
	}
	if tenant != "" {
		if _, err = tx.Exec(ctx, "SELECT set_config('horus.tenant_id', $1, true)", tenant); err != nil {
			return fmt.Errorf("pgdb: set tenant: %w", err)
		}
	}
	if d.statementTimeout > 0 {
		ms := d.statementTimeout.Milliseconds()
		if _, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", ms)); err != nil {
			return fmt.Errorf("pgdb: statement timeout: %w", err)
		}
	}
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgdb: commit: %w", err)
	}
	return nil
}

// Códigos SQLSTATE usados por los módulos.
const (
	SQLStateUniqueViolation    = "23505"
	SQLStateExclusionViolation = "23P01"
	SQLStateForeignKey         = "23503"
	SQLStateCheckViolation     = "23514"
	SQLStateInvalidText        = "22P02"
)

// ConstraintError devuelve el SQLSTATE y el nombre de la restricción de err
// si es un error de PostgreSQL.
func ConstraintError(err error) (code, constraint string, ok bool) {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code, pe.ConstraintName, true
	}
	return "", "", false
}

// IsCode indica si err es un error de PostgreSQL con ese SQLSTATE.
func IsCode(err error, code string) bool {
	c, _, ok := ConstraintError(err)
	return ok && c == code
}

// RedactDSN quita la contraseña de un DSN para registrarlo.
func RedactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "<invalid dsn>"
	}
	if u.User != nil {
		u.User = url.User(u.User.Username())
	}
	return u.String()
}
