package config_test

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
)

type moduleConfig struct {
	DSN      string `env:"HORUS_DEMO_DSN,required"`
	Password string `env:"HORUS_DEMO_PASSWORD"`
	Workers  int    `env:"HORUS_DEMO_WORKERS" envDefault:"4"`
}

func (m *moduleConfig) Validate() error {
	if m.Workers < 1 {
		return errors.New("HORUS_DEMO_WORKERS must be >= 1")
	}
	return nil
}

func TestLoadCommonDefaults(t *testing.T) {
	t.Parallel()
	c, err := config.Load[config.Common](nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Env != "dev" || c.AdminAddr != ":8081" || c.HTTPAddr != ":8080" || c.LogFormat != "json" ||
		c.LogLevel != "info" || c.Process != "horus" {
		t.Fatalf("defaults inesperados: %+v", c)
	}
	if c.ShutdownDelay != 5*time.Second || c.ShutdownTimeout != 25*time.Second || c.StartTimeout != time.Minute {
		t.Fatalf("plazos inesperados: %+v", c)
	}
	if len(c.Roles) != 0 {
		t.Fatalf("roles = %v, want vacío", c.Roles)
	}
}

func TestLoadCommonFromEnv(t *testing.T) {
	t.Parallel()
	c, err := config.Load[config.Common]([]string{
		"HORUS_ENV=prod", "HORUS_ROLES=auth,devices", "HORUS_LOG_LEVEL=debug",
		"HORUS_SHUTDOWN_TIMEOUT=3s", "IGNORED", "=x",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Env != "prod" || !slices.Equal(c.Roles, []string{"auth", "devices"}) || c.LogLevel != "debug" ||
		c.ShutdownTimeout != 3*time.Second {
		t.Fatalf("config inesperada: %+v", c)
	}
}

func TestCommonValidate(t *testing.T) {
	t.Parallel()
	tests := map[string][]string{
		"env":              {"HORUS_ENV=qa"},
		"format":           {"HORUS_LOG_FORMAT=xml"},
		"level":            {"HORUS_LOG_LEVEL=loud"},
		"negative delay":   {"HORUS_SHUTDOWN_DELAY=-1s"},
		"zero timeout":     {"HORUS_SHUTDOWN_TIMEOUT=0s"},
		"zero start":       {"HORUS_START_TIMEOUT=0s"},
		"duration invalid": {"HORUS_SHUTDOWN_DELAY=pronto"},
	}
	for name, environ := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := config.Load[config.Common](environ); err == nil {
				t.Fatalf("Load(%v) sin error", environ)
			}
		})
	}
}

func TestCommonValidateEmptyFields(t *testing.T) {
	t.Parallel()
	c, err := config.Load[config.Common](nil)
	if err != nil {
		t.Fatal(err)
	}
	c.AdminAddr, c.Process = "", ""
	err = c.Validate()
	if err == nil || !strings.Contains(err.Error(), "HORUS_ADMIN_ADDR") || !strings.Contains(err.Error(), "HORUS_PROCESS") {
		t.Fatalf("Validate = %v", err)
	}
}

func TestModuleConfigRequiredAndValidate(t *testing.T) {
	t.Parallel()
	if _, err := config.Load[moduleConfig](nil); err == nil || !strings.Contains(err.Error(), "HORUS_DEMO_DSN") {
		t.Fatalf("esperaba error nombrando HORUS_DEMO_DSN, got %v", err)
	}
	if _, err := config.Load[moduleConfig]([]string{"HORUS_DEMO_DSN=x", "HORUS_DEMO_WORKERS=0"}); err == nil {
		t.Fatal("esperaba error de Validate")
	}
	m, err := config.Load[moduleConfig]([]string{"HORUS_DEMO_DSN=x"})
	if err != nil || m.Workers != 4 {
		t.Fatalf("Load = %+v, %v", m, err)
	}
}

func TestFileVariantTakesPrecedence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "pw")
	if err := os.WriteFile(path, []byte("s3cr3t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := config.Load[moduleConfig]([]string{
		"HORUS_DEMO_DSN=x", "HORUS_DEMO_PASSWORD=plain", "HORUS_DEMO_PASSWORD_FILE=" + path,
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Password != "s3cr3t" {
		t.Fatalf("Password = %q, want contenido del archivo sin salto de línea", m.Password)
	}
}

func TestFileVariantErrorNamesVariableNotContent(t *testing.T) {
	t.Parallel()
	readFile := func(string) ([]byte, error) { return nil, fs.ErrPermission }
	var m moduleConfig
	err := config.ParseWith(&m, []string{"HORUS_DEMO_DSN=x", "HORUS_DEMO_PASSWORD_FILE=/run/secrets/pw"}, readFile)
	if err == nil || !strings.Contains(err.Error(), "HORUS_DEMO_PASSWORD_FILE") || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveIgnoresEmptyFilePathAndBareSuffix(t *testing.T) {
	t.Parallel()
	vars, err := config.Resolve([]string{"HORUS_X_FILE=", "_FILE=/nope", "HORUS__FILE=/nope", "PIP_CONFIG_FILE=/nope", "HORUS_X=1"}, func(string) ([]byte, error) {
		return nil, errors.New("no debería leerse")
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if vars["HORUS_X"] != "1" {
		t.Fatalf("HORUS_X = %q", vars["HORUS_X"])
	}
}

func TestCommonLogValueHasNoUnexpectedFields(t *testing.T) {
	t.Parallel()
	c, err := config.Load[config.Common]([]string{"HORUS_ROLES=auth"})
	if err != nil {
		t.Fatal(err)
	}
	v := c.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("kind = %v", v.Kind())
	}
	found := false
	for _, a := range v.Group() {
		if a.Key == "roles" && a.Value.String() == "auth" {
			found = true
		}
	}
	if !found {
		t.Fatal("LogValue sin roles")
	}
}
