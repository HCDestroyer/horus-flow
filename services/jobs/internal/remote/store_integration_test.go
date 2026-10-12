//go:build integration

package remote

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/packages/go/tenanttest"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/jobs/migrations"
)

func newPGStore(t *testing.T) (*Store, *pgdb.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: pgtest.New(t), AppRole: "jobs_app", PlatformRole: "jobs_platform"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := pgdb.Migrate(ctx, db, migrations.Schema, migrations.Postgres(), nil); err != nil {
		t.Fatal(err)
	}
	return NewStore(db), db
}

// database.md §1.2: toda tabla con tenant_id lleva RLS forzada y p_tenant. Los
// destinos remotos son de plataforma y no tienen tenant_id (la batería accept-i1
// lo comprobó contra la instalación real y falló cuando la columna existía).
func TestJobsSchemaTenantColumnsHaveRLS(t *testing.T) {
	_, db := newPGStore(t)
	tables, err := tenanttest.InspectRLS(context.Background(), db.Pool, []string{migrations.Schema})
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range tables {
		if !tb.Enabled || !tb.Forced || tb.TenantNullable {
			t.Errorf("%s: tenant_id sin RLS forzada o admite NULL: %+v", tb.Table, tb)
		}
	}
}

func TestStorePostgres(t *testing.T) {
	st, db := newPGStore(t)
	ctx := context.Background()

	inst, err := st.InstallationID(ctx)
	if err != nil || inst == uuid.Nil {
		t.Fatalf("installation = %v %v", inst, err)
	}
	if again, _ := st.InstallationID(ctx); again != inst {
		t.Fatal("el identificador de la instalación cambió")
	}

	d := &Destination{ID: uuid.Must(uuid.NewV7()), Kind: "sftp", Name: "NAS", Host: "nas.example", Port: 22, Username: "u",
		Path: "/copias", RetentionDays: 30}
	got, err := st.Create(ctx, d)
	if err != nil || got.Version != 1 || got.Enabled || got.HasCredentials() {
		t.Fatalf("create = %+v %v", got, err)
	}
	dup := *d
	dup.ID, dup.Name = uuid.Must(uuid.NewV7()), "nas"
	if _, err := st.Create(ctx, &dup); !errors.Is(err, ErrNameInUse) {
		t.Fatalf("nombre repetido = %v", err)
	}
	// Habilitar sin credenciales lo impide también la base (CHECK).
	bad := *got
	bad.Enabled = true
	if _, err := st.Update(ctx, &bad, 1, false); err == nil {
		t.Fatal("la base aceptó un destino habilitado sin credenciales")
	}
	next := *got
	next.Name = "NAS 2"
	if got, err = st.Update(ctx, &next, 1, false); err != nil || got.Version != 2 || got.Name != "NAS 2" {
		t.Fatalf("update = %+v %v", got, err)
	}
	if _, err := st.Update(ctx, &next, 1, false); !errors.Is(err, ErrVersionChanged) {
		t.Fatalf("versión vieja = %v", err)
	}

	sealer := envelope.Ephemeral()
	ct, dek, _ := sealCredentials(sealer, d.ID, Credentials{Password: ptr("pw")})
	if got, err = st.SetCredentials(ctx, d.ID, ct, dek, sealer.KEKID(), "password"); err != nil || !got.HasCredentials() || got.Version != 3 {
		t.Fatalf("credenciales = %+v %v", got, err)
	}
	if s, err := openCredentials(sealer, got); err != nil || s.Password != "pw" {
		t.Fatalf("credencial guardada no abre: %v", err)
	}
	// La credencial no está en claro en la base.
	var raw []byte
	if err := db.Pool.QueryRow(ctx, `SELECT credential_ciphertext FROM jobs.remote_destination WHERE id = $1`, d.ID).Scan(&raw); err != nil ||
		bytes.Contains(raw, []byte(`"pw"`)) {
		t.Fatalf("credencial en claro o ilegible: %v", err)
	}

	fp := Fingerprint(newHostSigner(t).PublicKey())
	if got, err = st.RecordTest(ctx, d.ID, &fp, false); err != nil || got.HostKeyFingerprint == nil || *got.HostKeyFingerprint != fp ||
		got.VerifiedAt != nil || got.Version != 4 {
		t.Fatalf("fijar huella = %+v %v", got, err)
	}
	other := Fingerprint(newHostSigner(t).PublicKey())
	if got, err = st.RecordTest(ctx, d.ID, &other, true); err != nil || *got.HostKeyFingerprint != fp || got.VerifiedAt == nil || got.Version != 4 {
		t.Fatalf("una prueba no sustituye la huella fijada: %+v %v", got, err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	e := LedgerEntry{Rel: "backups/postgres/a", Size: 3, MTime: now, SHA256: make([]byte, 32), RemoteName: "/copias/x/a.age", UploadedAt: now}
	if err := st.RecordCopy(ctx, d.ID, 3, e); !errors.Is(err, ErrVersionChanged) {
		t.Fatalf("copia con versión vieja = %v", err)
	}
	if err := st.RecordCopy(ctx, d.ID, 4, e); err != nil {
		t.Fatal(err)
	}
	e.Size = 4
	if err := st.RecordCopy(ctx, d.ID, 4, e); err != nil {
		t.Fatal(err)
	}
	l, err := st.Ledger(ctx, d.ID)
	if err != nil || len(l) != 1 || l[e.Rel].Size != 4 || !l[e.Rel].MTime.Equal(now) {
		t.Fatalf("ledger = %+v %v", l, err)
	}
	// El estado no sube la versión.
	if err := st.RecordAttempt(ctx, d.ID, now, "boom"); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAttempt(ctx, d.ID, now.Add(time.Second), ""); err != nil {
		t.Fatal(err)
	}
	if got, _ = st.Get(ctx, d.ID); got.Version != 4 || got.LastError != nil || got.LastSuccessAt == nil {
		t.Fatalf("estado = %+v", got)
	}
	// Cambiar la ruta reinicia el ledger.
	next = *got
	next.Path = "/otra"
	if got, err = st.Update(ctx, &next, 4, true); err != nil {
		t.Fatal(err)
	}
	if l, _ = st.Ledger(ctx, d.ID); len(l) != 0 {
		t.Fatal("el ledger no se reinició")
	}
	if err := st.RecordCopy(ctx, d.ID, got.Version, e); err != nil {
		t.Fatal(err)
	}
	if err := st.ForgetCopy(ctx, d.ID, e.Rel); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordCopy(ctx, d.ID, got.Version, e); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, d.ID, got.Version-1); !errors.Is(err, ErrVersionChanged) {
		t.Fatalf("borrado con versión vieja = %v", err)
	}
	if err := st.Delete(ctx, d.ID, got.Version); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs.remote_copy`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("el ledger no se borró en cascada: %d %v", n, err)
	}
	if _, err := st.Get(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get borrado = %v", err)
	}
	if err := st.Delete(ctx, d.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("borrado repetido = %v", err)
	}
}

// TestSyncWithPostgres: el motor completo con el almacén real y un SFTP en
// proceso (ledger durable: un servicio nuevo no vuelve a subir nada).
func TestSyncWithPostgres(t *testing.T) {
	st, _ := newPGStore(t)
	keyPEM, pub := clientKeyPEM(t, "")
	srv := startSFTP(t, newHostSigner(t), "backup", "", pub)
	idn, identity := testIdentity(t)
	root := t.TempDir()
	fakeBackups(t, root, time.Now().Add(-time.Hour))
	sealer := envelope.Ephemeral()
	newSvc := func() *Service {
		svc, err := NewService(Options{Repo: st, Sealer: sealer, Recipient: idn.Recipient(), Root: root, TestTimeout: 5 * time.Second,
			Logger: slog.New(slog.DiscardHandler), Audit: func() (authapi.AuditRecorder, bool) { return &fakeAudit{}, true }})
		if err != nil {
			t.Fatal(err)
		}
		return svc
	}
	svc := newSvc()
	remoteDir := filepath.Join(t.TempDir(), "r")
	id := readyDest(t, svc, srv, keyPEM, "", remoteDir)
	ctx := platformCtx()
	d, _ := st.Get(ctx, id)
	svc.SyncDestination(ctx, d)
	d, _ = st.Get(ctx, id)
	if d.LastSuccessAt == nil || d.LastError != nil {
		t.Fatalf("copia = %v", deref(d.LastError))
	}
	l, _ := st.Ledger(ctx, id)
	if len(l) != 7 {
		t.Fatalf("ledger = %d", len(l))
	}
	inst, _ := st.InstallationID(ctx)
	ct, err := os.ReadFile(filepath.Join(remoteDir, "horus-"+inst.String(), "backups/postgres/backup/horus/backup.info.age"))
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := DecryptAll(ct, identity); err != nil || !strings.HasPrefix(string(plain), "info |") {
		t.Fatalf("descifrado = %q %v", plain, err)
	}
	// Reinicio: otro Service con el mismo almacén no sube nada.
	svc2 := newSvc()
	d, _ = st.Get(ctx, id)
	svc2.SyncDestination(ctx, d)
	if got := svc2.bytes.WithLabelValues(id.String()); got == nil {
		t.Fatal("sin métrica")
	}
	list, err := svc2.List(ctx)
	if err != nil || len(list) != 1 || list[0].Status != StatusOK || *list[0].FilesPending != 0 {
		t.Fatalf("lista = %+v %v", list, err)
	}
}
