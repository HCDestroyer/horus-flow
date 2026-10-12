//go:build integration

package jobs

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"testing"

	"filippo.io/age"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
)

// TestRegisterRemoteCopy: con PostgreSQL, KEK y clave age (entorno prod) el
// rol jobs sirve las rutas de destinos remotos y aplica sus migraciones; sin
// KEK fuera de desarrollo no arranca.
func TestRegisterRemoteCopy(t *testing.T) {
	key, err := authz.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pubPEM, err := authz.MarshalPublicKeyPEM(key.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	kek := make([]byte, 32)
	_, _ = rand.Read(kek)
	id, _ := age.GenerateX25519Identity()
	env := []string{"HORUS_POSTGRES_DSN=" + pgtest.New(t), "HORUS_JOBS_KEK=" + hex.EncodeToString(kek),
		"HORUS_BACKUP_AGE_RECIPIENT=" + id.Recipient().String(), "HORUS_JWT_PUBLIC_KEYS=" + string(pubPEM),
		"HORUS_UPDATE_CHECK=false", "HORUS_DATA_DIR=" + t.TempDir()}
	mux := httpx.NewMux(nil, nil)
	ctx := context.Background()
	m, err := Register(ctx, module.Deps{Role: Role, Common: config.Common{Env: config.EnvProd}, Environ: env,
		Routes: mux.ForService(Role), Metrics: prometheus.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	j := m.(*jobs)
	if err := j.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Stop(ctx) }()
	for _, p := range []string{
		"GET /api/v1/platform/remote-destinations", "POST /api/v1/platform/remote-destinations",
		"PATCH /api/v1/platform/remote-destinations/{destination_id}", "DELETE /api/v1/platform/remote-destinations/{destination_id}",
		"PUT /api/v1/platform/remote-destinations/{destination_id}/credentials",
		"POST /api/v1/platform/remote-destinations/{destination_id}/test", "POST /api/v1/platform/remote-destinations/{destination_id}/sync",
		"GET /api/v1/platform/updates",
	} {
		if !slices.Contains(mux.Patterns(), p) {
			t.Errorf("falta la ruta %s", p)
		}
	}
	if _, err := j.remote.db.Pool.Exec(ctx, `SELECT 1 FROM jobs.remote_copy LIMIT 1`); err != nil {
		t.Fatalf("migraciones no aplicadas: %v", err)
	}
	// Sin KEK fuera de desarrollo: error de arranque.
	if _, err := Register(ctx, module.Deps{Role: Role, Common: config.Common{Env: config.EnvProd}, Environ: env[:1]}); err == nil {
		t.Fatal("arrancó sin KEK en prod")
	}
}
