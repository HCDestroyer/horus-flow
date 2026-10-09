package chmigrate

import (
	"context"
	"strings"
	"testing"
)

func TestUserValidate(t *testing.T) {
	t.Parallel()
	good := User{Name: "horus_ingester", Password: strings.Repeat("x", 16), Roles: []string{"horus_ingester_role"}}
	if err := good.validate(); err != nil {
		t.Fatalf("válido: %v", err)
	}
	for _, u := range []User{
		{Name: "Horus", Password: good.Password},
		{Name: "horus; DROP USER x", Password: good.Password},
		{Name: "horus_x", Password: "corta"},
		{Name: "horus_x", Password: good.Password, Roles: []string{"r'1"}},
	} {
		if err := u.validate(); err == nil {
			t.Errorf("%+v debería ser inválido", u.Name)
		}
	}
}

func TestProvisionRejectsBeforeExec(t *testing.T) {
	t.Parallel()
	// Con un usuario inválido no se llega a usar la base de datos (db nil).
	err := Provision(context.Background(), nil, []User{{Name: "bad name", Password: strings.Repeat("x", 16)}})
	if err == nil || !strings.Contains(err.Error(), "invalid user name") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpen(t *testing.T) {
	t.Parallel()
	if _, err := Open("", ""); err == nil {
		t.Fatal("DSN vacío debería fallar")
	}
	db, err := Open("clickhouse://horus@127.0.0.1:9000/horus", "secret")
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
}

func TestOptionsDefaults(t *testing.T) {
	t.Parallel()
	o := Options{}.withDefaults()
	if o.VersionTable != DefaultVersionTable || o.BootstrapDatabase != DefaultBootstrapDatabase || o.Logger == nil {
		t.Fatalf("defaults = %+v", o)
	}
}
