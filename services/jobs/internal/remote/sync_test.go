package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
)

func platformCtx() context.Context {
	p := &authz.Principal{Type: "user", Subject: "usr_admin", Scope: authz.ScopePlatform,
		Perms: map[string][]string{PermManage: {"*"}}, AuthTime: time.Now()}
	return authz.WithPrincipal(context.Background(), p)
}

// setupDest da de alta un destino contra srv con la ruta remota remoteDir.
func setupDest(t *testing.T, svc *Service, srv *sftpServer, remoteDir string) uuid.UUID {
	t.Helper()
	v, err := svc.Create(platformCtx(), CreateInput{Kind: ptr("sftp"), Name: ptr("NAS " + uuid.NewString()[:6]), Host: ptr(srv.host),
		Port: ptr(srv.port), Username: ptr(srv.user), Path: ptr(remoteDir), RetentionDays: ptr(30)}, AuditMeta{})
	if err != nil {
		t.Fatal(err)
	}
	return v.ID
}

func TestConnectionTestTOFU(t *testing.T) {
	t.Parallel()
	signer := newHostSigner(t)
	srv := startSFTP(t, signer, "horus", "pw-correcta", nil)
	_, identity := testIdentity(t)
	r, _, _ := ParseAgeRecipient(identity)
	svc, repo, audit := newTestService(t, t.TempDir(), r)
	remoteDir := filepath.Join(t.TempDir(), "copias")
	id := setupDest(t, svc, srv, remoteDir)
	ctx := platformCtx()
	fp := Fingerprint(signer.PublicKey())

	// 1. Primera conexión: devuelve la huella y pide confirmarla.
	res, err := svc.Test(ctx, id, "", AuditMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Stage != StageHostKey || res.HostKeyFingerprint == nil || *res.HostKeyFingerprint != fp || res.HostKeyConfirmed {
		t.Fatalf("primera prueba = %+v", res)
	}
	if d, _ := repo.Get(ctx, id); d.HostKeyFingerprint != nil {
		t.Fatal("huella fijada sin confirmar")
	}
	// 2. Confirmación con otra huella: no se fija.
	if res, _ = svc.Test(ctx, id, "SHA256:"+strings.Repeat("A", 43), AuditMeta{}); res.Stage != StageHostKey || res.OK {
		t.Fatalf("confirmación errónea = %+v", res)
	}
	// 3. Confirmada pero sin credenciales: se fija la huella; falla en auth.
	res, _ = svc.Test(ctx, id, fp, AuditMeta{})
	if res.OK || res.Stage != StageAuth || !res.HostKeyConfirmed || res.Destination.HostKeyFingerprint == nil {
		t.Fatalf("sin credenciales = %+v", res)
	}
	// 4. Contraseña errónea: auth.
	if _, err := svc.SetCredentials(ctx, id, Credentials{Password: ptr("mala")}, AuditMeta{}); err != nil {
		t.Fatal(err)
	}
	if res, _ = svc.Test(ctx, id, "", AuditMeta{}); res.OK || res.Stage != StageAuth {
		t.Fatalf("contraseña mala = %+v", res)
	}
	// 5. Correcta: escribe y borra el archivo de prueba; done.
	if _, err := svc.SetCredentials(ctx, id, Credentials{Password: ptr("pw-correcta")}, AuditMeta{}); err != nil {
		t.Fatal(err)
	}
	res, _ = svc.Test(ctx, id, "", AuditMeta{})
	if !res.OK || res.Stage != StageDone || res.Error != nil {
		t.Fatalf("prueba correcta = %+v (%v)", res, deref(res.Error))
	}
	entries, err := os.ReadDir(filepath.Join(remoteDir, "horus-"+repo.inst.String()))
	if err != nil || len(entries) != 0 {
		t.Fatalf("la carpeta remota debería existir y quedar vacía: %v %v", entries, err)
	}
	// 6. La clave del servidor cambia: ok=false, host_key, sin tocar la huella.
	srv2 := startSFTP(t, newHostSigner(t), "horus", "pw-correcta", nil)
	d, _ := repo.Get(ctx, id)
	v, err := svc.Update(ctx, id, d.Version, Patch{Name: ptr("renombrado")}, AuditMeta{})
	if err != nil {
		t.Fatal(err)
	}
	repo.mu.Lock()
	repo.dests[id].Port = srv2.port // mismo destino, otro servidor detrás (suplantación)
	repo.mu.Unlock()
	res, _ = svc.Test(ctx, id, Fingerprint(srv2.signer.PublicKey()), AuditMeta{})
	if res.OK || res.Stage != StageHostKey || res.Error == nil || *res.Error != "La clave del servidor cambió" ||
		*res.Destination.HostKeyFingerprint != fp {
		t.Fatalf("clave cambiada = %+v", res)
	}
	_ = v
	// Sin servidor: connect.
	repo.mu.Lock()
	repo.dests[id].Port = 1
	repo.mu.Unlock()
	if res, _ = svc.Test(ctx, id, "", AuditMeta{}); res.OK || res.Stage != StageConnect {
		t.Fatalf("sin servidor = %+v", res)
	}
	got := audit.actions()
	if len(got) < 8 || got[0] != AuditCreated {
		t.Fatalf("auditoría = %v", got)
	}
	for _, e := range audit.entries {
		if strings.Contains(e.Action, "credentials") {
			for k := range e.Changes {
				if k != "credential_kind" {
					t.Fatalf("la auditoría de credenciales lleva %q", k)
				}
			}
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// readyDest deja un destino habilitado con clave privada cifrada.
func readyDest(t *testing.T, svc *Service, srv *sftpServer, keyPEM, pass, remoteDir string) uuid.UUID {
	t.Helper()
	ctx := platformCtx()
	id := setupDest(t, svc, srv, remoteDir)
	if _, err := svc.SetCredentials(ctx, id, Credentials{PrivateKey: ptr(keyPEM), Passphrase: ptr(pass)}, AuditMeta{}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Test(ctx, id, Fingerprint(srv.signer.PublicKey()), AuditMeta{})
	if err != nil || !res.OK {
		t.Fatalf("prueba = %+v %v (%s)", res, err, deref(res.Error))
	}
	v, err := svc.Update(ctx, id, res.Destination.Version, Patch{Enabled: ptr(true)}, AuditMeta{})
	if err != nil || !v.Enabled {
		t.Fatalf("habilitar: %v", err)
	}
	return id
}

func TestFullSyncIdempotentAndRetention(t *testing.T) {
	t.Parallel()
	keyPEM, pub := clientKeyPEM(t, "frase")
	srv := startSFTP(t, newHostSigner(t), "backup", "", pub)
	idn, identity := testIdentity(t)
	root := t.TempDir()
	old := time.Now().Add(-time.Hour)
	fakeBackups(t, root, old)
	svc, repo, _ := newTestService(t, root, idn.Recipient())
	remoteDir := filepath.Join(t.TempDir(), "nas", "horus")
	id := readyDest(t, svc, srv, keyPEM, "frase", remoteDir)
	ctx := platformCtx()

	// Antes de copiar: pendientes calculados con el ledger vacío.
	list, err := svc.List(ctx)
	if err != nil || len(list) != 1 || list[0].Status != StatusPending || list[0].FilesPending == nil || *list[0].FilesPending != 7 {
		t.Fatalf("listado previo = %+v %v", list, err)
	}

	d, _ := repo.Get(ctx, id)
	svc.SyncDestination(ctx, d)
	d, _ = repo.Get(ctx, id)
	if d.LastSuccessAt == nil || d.LastError != nil {
		t.Fatalf("copia: last_error = %v", deref(d.LastError))
	}
	prefix := "horus-" + repo.inst.String()
	ledger, _ := repo.Ledger(ctx, id)
	if len(ledger) != 7 {
		t.Fatalf("ledger = %d entradas", len(ledger))
	}
	for rel, e := range ledger {
		local, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		remotePath := filepath.Join(remoteDir, prefix, filepath.FromSlash(rel)) + ".age"
		if e.RemoteName != filepath.ToSlash(remotePath) {
			t.Fatalf("remote_name = %s, quiero %s", e.RemoteName, remotePath)
		}
		ct, err := os.ReadFile(remotePath)
		if err != nil {
			t.Fatalf("falta en el destino: %v", err)
		}
		if bytes.Contains(ct, local) {
			t.Fatalf("%s está en claro en el destino", rel)
		}
		plain, err := DecryptAll(ct, identity)
		if err != nil || !bytes.Equal(plain, local) {
			t.Fatalf("%s no descifra igual: %v", rel, err)
		}
		sum := sha256.Sum256(local)
		if !bytes.Equal(e.SHA256, sum[:]) {
			t.Fatalf("%s: sha256 del ledger distinto", rel)
		}
	}
	// Lo que está a medias no se copia; ni temporales ni .partial en el destino.
	for _, rel := range []string{"backups/clickhouse/incr_20261012T030000Z/data/flows/agg/x.bin.age",
		"backups/postgres/archive/horus/18-1/0000000100000000/x.pgbackrest.tmp.age"} {
		if _, err := os.Stat(filepath.Join(remoteDir, prefix, rel)); err == nil {
			t.Fatalf("%s no debería estar en el destino", rel)
		}
	}
	_ = filepath.WalkDir(remoteDir, func(p string, _ os.DirEntry, _ error) error {
		if strings.HasSuffix(p, ".partial") {
			t.Errorf("quedó un temporal %s", p)
		}
		return nil
	})
	bytesAfterFirst := testutil.ToFloat64(svc.bytes.WithLabelValues(id.String()))
	if bytesAfterFirst <= 0 {
		t.Fatal("horus_remote_sync_bytes_total no subió")
	}
	list, _ = svc.List(ctx)
	if list[0].Status != StatusOK || *list[0].FilesPending != 0 || *list[0].BytesPending != 0 {
		t.Fatalf("listado tras copiar = %+v", list[0])
	}

	// Re-sincronizar sin cambios no sube nada.
	svc.SyncDestination(ctx, d)
	if got := testutil.ToFloat64(svc.bytes.WithLabelValues(id.String())); got != bytesAfterFirst {
		t.Fatalf("re-sync subió %v bytes", got-bytesAfterFirst)
	}

	// Un archivo que cambia (backup.info) se vuelve a subir y reemplaza al remoto.
	writeFile(t, root, "backups/postgres/backup/horus/backup.info", "info v2 más larga", old.Add(time.Minute))
	// El backup en curso termina.
	writeFile(t, root, "backups/clickhouse/incr_20261012T030000Z/.backup", "<backup/>", old)
	d, _ = repo.Get(ctx, id)
	svc.SyncDestination(ctx, d)
	ct, _ := os.ReadFile(filepath.Join(remoteDir, prefix, "backups/postgres/backup/horus/backup.info.age"))
	if plain, _ := DecryptAll(ct, identity); string(plain) != "info v2 más larga" {
		t.Fatalf("backup.info remoto = %q", plain)
	}
	if _, err := os.Stat(filepath.Join(remoteDir, prefix, "backups/clickhouse/incr_20261012T030000Z/data/flows/agg/x.bin.age")); err != nil {
		t.Fatal("el backup terminado no se copió")
	}

	// Retención: lo vencido que ya no está en local se borra del destino; lo
	// vencido que sigue en local, no.
	walRel := "backups/postgres/archive/horus/18-1/0000000100000000/000000010000000000000001-abc.zst"
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(walRel))); err != nil {
		t.Fatal(err)
	}
	repo.mu.Lock()
	for rel, e := range repo.ledger[id] {
		e.UploadedAt = time.Now().Add(-40 * 24 * time.Hour)
		repo.ledger[id][rel] = e
	}
	repo.mu.Unlock()
	d, _ = repo.Get(ctx, id)
	svc.SyncDestination(ctx, d)
	if _, err := os.Stat(filepath.Join(remoteDir, prefix, walRel) + ".age"); !os.IsNotExist(err) {
		t.Fatalf("el WAL vencido y borrado en local sigue en el destino: %v", err)
	}
	if _, err := os.Stat(filepath.Join(remoteDir, prefix, "backups/postgres/backup/horus/backup.info.age")); err != nil {
		t.Fatal("se borró del destino algo que sigue en local")
	}
	if ledger, _ = repo.Ledger(ctx, id); len(ledger) != 8 {
		t.Fatalf("ledger tras retención = %d", len(ledger))
	}
	if d, _ = repo.Get(ctx, id); d.LastError != nil {
		t.Fatalf("error tras retención: %s", *d.LastError)
	}
}

func TestSyncFailuresOnlyUpdateStatus(t *testing.T) {
	t.Parallel()
	keyPEM, pub := clientKeyPEM(t, "")
	srv := startSFTP(t, newHostSigner(t), "backup", "", pub)
	idn, _ := testIdentity(t)
	root := t.TempDir()
	fakeBackups(t, root, time.Now().Add(-time.Hour))
	svc, repo, _ := newTestService(t, root, idn.Recipient())
	id := readyDest(t, svc, srv, keyPEM, "", filepath.Join(t.TempDir(), "r"))
	ctx := platformCtx()

	// El servidor cambia de clave: la copia falla con host_key y no sube nada.
	srv2 := startSFTP(t, newHostSigner(t), "backup", "", pub)
	repo.mu.Lock()
	repo.dests[id].Port = srv2.port
	repo.mu.Unlock()
	d, _ := repo.Get(ctx, id)
	svc.SyncDestination(ctx, d)
	d, _ = repo.Get(ctx, id)
	if d.LastError == nil || *d.LastError != "La clave del servidor cambió" || d.LastSuccessAt != nil {
		t.Fatalf("estado = %+v", d)
	}
	if st, _ := ComputeStatus(d, time.Now(), 26*time.Hour); st != StatusFailing {
		t.Fatalf("status = %s", st)
	}
	if got := testutil.ToFloat64(svc.failures.WithLabelValues(id.String(), StageHostKey)); got != 1 {
		t.Fatalf("failures{host_key} = %v", got)
	}
	if l, _ := repo.Ledger(ctx, id); len(l) != 0 {
		t.Fatal("ledger con entradas tras un fallo")
	}

	// Sin clave age: no se copia, motivo config.
	svc2, repo2, _ := newTestService(t, root, nil)
	id2 := readyDest(t, svc2, srv, keyPEM, "", filepath.Join(t.TempDir(), "r2"))
	d2, _ := repo2.Get(ctx, id2)
	svc2.SyncDestination(ctx, d2)
	if d2, _ = repo2.Get(ctx, id2); d2.LastError == nil || !strings.Contains(*d2.LastError, "age") {
		t.Fatalf("sin clave age: %+v", d2)
	}
	if _, err := svc2.RequestSync(ctx, id2, "k1", AuditMeta{}); !isCode(err, CodeNotReady) {
		t.Fatalf("sync sin clave age = %v", err)
	}
}

func isCode(err error, code string) bool {
	e, ok := apperr.As(err)
	return ok && e.Code == code
}

func TestRequestSync(t *testing.T) {
	t.Parallel()
	keyPEM, pub := clientKeyPEM(t, "")
	srv := startSFTP(t, newHostSigner(t), "backup", "", pub)
	idn, _ := testIdentity(t)
	root := t.TempDir()
	fakeBackups(t, root, time.Now().Add(-time.Hour))
	svc, repo, audit := newTestService(t, root, idn.Recipient())
	ctx := platformCtx()

	// No listo: sin credenciales, sin huella, deshabilitado.
	raw := setupDest(t, svc, srv, "r")
	if _, err := svc.RequestSync(ctx, raw, "k0", AuditMeta{}); !isCode(err, CodeNotReady) {
		t.Fatalf("sin credenciales = %v", err)
	}
	if _, err := svc.RequestSync(ctx, uuid.New(), "k0", AuditMeta{}); !isCode(err, CodeNotFound) {
		t.Fatalf("inexistente = %v", err)
	}

	id := readyDest(t, svc, srv, keyPEM, "", filepath.Join(t.TempDir(), "r"))
	if _, err := svc.RequestSync(ctx, id, "k1", AuditMeta{}); err != nil {
		t.Fatal(err)
	}
	// Repetir la misma Idempotency-Key: 202 sin encolar otra.
	if _, err := svc.RequestSync(ctx, id, "k1", AuditMeta{}); err != nil {
		t.Fatalf("misma clave = %v", err)
	}
	// Otra clave mientras sigue en cola: SYNC_RUNNING.
	if _, err := svc.RequestSync(ctx, id, "k2", AuditMeta{}); !isCode(err, CodeSyncRunning) {
		t.Fatalf("en curso = %v", err)
	}
	// El motor la atiende.
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { svc.Run(runCtx); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		d, _ := repo.Get(ctx, id)
		if d.LastSuccessAt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("la copia pedida no se hizo: %v", deref(d.LastError))
		}
		time.Sleep(20 * time.Millisecond)
	}
	for {
		svc.mu.Lock()
		busy := svc.pending[id]
		svc.mu.Unlock()
		if !busy {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := svc.RequestSync(ctx, id, "k3", AuditMeta{}); err != nil {
		t.Fatalf("tras terminar = %v", err)
	}
	cancel()
	<-done
	n := 0
	for _, a := range audit.actions() {
		if a == AuditSyncRequested {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("sync_requested auditados = %d", n)
	}
}

// TestSyncCancelled: al apagar (contexto cancelado) la copia se para sin
// marcar el destino como fallido.
func TestSyncCancelled(t *testing.T) {
	t.Parallel()
	keyPEM, pub := clientKeyPEM(t, "")
	srv := startSFTP(t, newHostSigner(t), "backup", "", pub)
	idn, _ := testIdentity(t)
	root := t.TempDir()
	fakeBackups(t, root, time.Now().Add(-time.Hour))
	svc, repo, _ := newTestService(t, root, idn.Recipient())
	id := readyDest(t, svc, srv, keyPEM, "", filepath.Join(t.TempDir(), "r"))
	ctx, cancel := context.WithCancel(platformCtx())
	cancel()
	d, _ := repo.Get(platformCtx(), id)
	svc.SyncDestination(ctx, d)
	if d, _ = repo.Get(platformCtx(), id); d.LastAttemptAt != nil || d.LastError != nil {
		t.Fatalf("una copia cancelada cambió el estado: %+v", d)
	}
}
