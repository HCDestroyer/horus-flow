// Package chmigrate aplica las migraciones ClickHouse de Horus con goose v3
// (docs/database.md §3, docs/conventions.md §2.1) y crea los usuarios por
// módulo a partir de los secretos del despliegue.
//
// ClickHouse no tiene DDL transaccional: las migraciones se escriben
// idempotentes (IF NOT EXISTS) y con la anotación `-- +goose NO TRANSACTION`,
// de modo que una ejecución interrumpida se puede reintentar. La tabla de
// control es flows.goose_db_version (database.md §3: una por esquema; el
// esquema ClickHouse completo lo versiona un único conjunto de migraciones).
//
// Uso típico:
//
//	db, err := chmigrate.Open(dsn, password)
//	res, err := chmigrate.Up(ctx, db, chschema.Migrations(), chmigrate.Options{Logger: log})
//	err = chmigrate.Provision(ctx, db, users)
package chmigrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/pressly/goose/v3"
)

// Valores por defecto de [Options].
const (
	DefaultVersionTable      = "flows.goose_db_version"
	DefaultBootstrapDatabase = "flows"
)

// Options configura [Up], [Down] y [Version].
type Options struct {
	// VersionTable es la tabla de control de goose (db.tabla). Por defecto
	// [DefaultVersionTable].
	VersionTable string
	// BootstrapDatabase se crea antes de goose porque aloja la tabla de
	// control. Por defecto [DefaultBootstrapDatabase].
	BootstrapDatabase string
	// Logger recibe una línea por migración aplicada. Opcional.
	Logger *slog.Logger
}

func (o Options) withDefaults() Options {
	if o.VersionTable == "" {
		o.VersionTable = DefaultVersionTable
	}
	if o.BootstrapDatabase == "" {
		o.BootstrapDatabase = DefaultBootstrapDatabase
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return o
}

// Applied describe una migración ejecutada.
type Applied struct {
	Version   int64
	Name      string
	Direction string
}

// Open abre un *sql.DB sobre el DSN de ClickHouse (clickhouse://user@host:9000/db).
// Si password no está vacío sustituye a la del DSN (se lee de un secreto
// _FILE, nunca del DSN en producción).
func Open(dsn, password string) (*sql.DB, error) {
	if dsn == "" {
		return nil, errors.New("chmigrate: empty ClickHouse DSN")
	}
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("chmigrate: parse DSN: %w", err)
	}
	if password != "" {
		opts.Auth.Password = password
	}
	return clickhouse.OpenDB(opts), nil
}

// Up aplica todas las migraciones pendientes de fsys (archivos
// <marca-de-tiempo>_<desc>.sql en su raíz) y devuelve las aplicadas. Sin
// pendientes devuelve una lista vacía: es idempotente.
func Up(ctx context.Context, db *sql.DB, fsys fs.FS, opts Options) ([]Applied, error) {
	opts = opts.withDefaults()
	p, err := newProvider(ctx, db, fsys, opts)
	if err != nil {
		return nil, err
	}
	res, err := p.Up(ctx)
	out := results(res, opts.Logger)
	if err != nil {
		return out, fmt.Errorf("chmigrate: up: %w", err)
	}
	return out, nil
}

// DownTo revierte migraciones hasta dejar la versión indicada (0 = todas).
// Solo para desarrollo y tests: en producción las migraciones son
// forward-only (database.md §3).
func DownTo(ctx context.Context, db *sql.DB, fsys fs.FS, version int64, opts Options) ([]Applied, error) {
	opts = opts.withDefaults()
	p, err := newProvider(ctx, db, fsys, opts)
	if err != nil {
		return nil, err
	}
	res, err := p.DownTo(ctx, version)
	out := results(res, opts.Logger)
	if err != nil {
		return out, fmt.Errorf("chmigrate: down: %w", err)
	}
	return out, nil
}

// Version devuelve la versión aplicada y la última disponible en fsys.
func Version(ctx context.Context, db *sql.DB, fsys fs.FS, opts Options) (current, target int64, err error) {
	opts = opts.withDefaults()
	p, err := newProvider(ctx, db, fsys, opts)
	if err != nil {
		return 0, 0, err
	}
	current, target, err = p.GetVersions(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("chmigrate: versions: %w", err)
	}
	return current, target, nil
}

func newProvider(ctx context.Context, db *sql.DB, fsys fs.FS, opts Options) (*goose.Provider, error) {
	if !identRe.MatchString(opts.BootstrapDatabase) {
		return nil, fmt.Errorf("chmigrate: invalid bootstrap database %q", opts.BootstrapDatabase)
	}
	if _, err := db.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS "+opts.BootstrapDatabase); err != nil {
		return nil, fmt.Errorf("chmigrate: create database %s: %w", opts.BootstrapDatabase, err)
	}
	p, err := goose.NewProvider(goose.DialectClickHouse, db, fsys,
		goose.WithTableName(opts.VersionTable),
		goose.WithDisableGlobalRegistry(true),
		// Sin transacciones: ClickHouse no tiene DDL transaccional.
		goose.WithIsolateDDL(true),
		goose.WithSlog(opts.Logger),
	)
	if err != nil {
		return nil, fmt.Errorf("chmigrate: goose provider: %w", err)
	}
	return p, nil
}

func results(res []*goose.MigrationResult, log *slog.Logger) []Applied {
	out := make([]Applied, 0, len(res))
	for _, r := range res {
		if r == nil || r.Source == nil {
			continue
		}
		a := Applied{Version: r.Source.Version, Name: r.Source.Path, Direction: r.Direction}
		if r.Error == nil {
			log.Info("clickhouse migration applied",
				"version", a.Version, "name", a.Name, "direction", a.Direction, "duration", r.Duration)
		}
		out = append(out, a)
	}
	return out
}

// User es un usuario de ClickHouse por módulo (database.md §5: "usuario CH por
// servicio"). Sus roles los crean las migraciones; la contraseña llega de un
// secreto del despliegue.
type User struct {
	Name     string
	Password string
	// Roles se conceden y quedan como roles por defecto del usuario.
	Roles []string
}

// identRe valida nombres de usuario, rol y base de datos antes de
// interpolarlos en DDL (ClickHouse no admite parámetros en CREATE USER).
var identRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// Provision crea o actualiza cada usuario: contraseña (solo se envía su
// SHA-256, nunca el texto plano), roles concedidos y roles por defecto. Es
// idempotente y rota la contraseña si cambió el secreto.
func Provision(ctx context.Context, db *sql.DB, users []User) error {
	for _, u := range users {
		if err := u.validate(); err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(u.Password))
		hash := hex.EncodeToString(sum[:])
		stmts := []string{
			fmt.Sprintf("CREATE USER IF NOT EXISTS %s IDENTIFIED WITH sha256_hash BY '%s'", u.Name, hash),
			fmt.Sprintf("ALTER USER %s IDENTIFIED WITH sha256_hash BY '%s'", u.Name, hash),
		}
		if len(u.Roles) > 0 {
			roles := strings.Join(u.Roles, ", ")
			stmts = append(stmts,
				fmt.Sprintf("GRANT %s TO %s", roles, u.Name),
				fmt.Sprintf("ALTER USER %s DEFAULT ROLE %s", u.Name, roles),
			)
		}
		for _, q := range stmts {
			if _, err := db.ExecContext(ctx, q); err != nil {
				// El error de ClickHouse no incluye el hash: se informa solo el usuario.
				return fmt.Errorf("chmigrate: provision user %s: %w", u.Name, err)
			}
		}
	}
	return nil
}

func (u User) validate() error {
	if !identRe.MatchString(u.Name) {
		return fmt.Errorf("chmigrate: invalid user name %q", u.Name)
	}
	if len(u.Password) < 16 {
		return fmt.Errorf("chmigrate: password for user %s must have at least 16 characters", u.Name)
	}
	for _, r := range u.Roles {
		if !identRe.MatchString(r) {
			return fmt.Errorf("chmigrate: invalid role %q for user %s", r, u.Name)
		}
	}
	return nil
}
