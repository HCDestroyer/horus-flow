package domain

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/archtest"
)

var fastArgon = Argon2Params{Memory: 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

func TestPasswordHashVerify(t *testing.T) {
	t.Parallel()
	h, err := HashPassword("correct horse battery", fastArgon)
	if err != nil || !strings.HasPrefix(h, "$argon2id$v=19$m=1024,t=1,p=1$") {
		t.Fatalf("hash = %q, %v", h, err)
	}
	ok, rehash, err := VerifyPassword("correct horse battery", h, fastArgon)
	if !ok || rehash || err != nil {
		t.Fatalf("verify = %v %v %v", ok, rehash, err)
	}
	ok, _, _ = VerifyPassword("wrong", h, fastArgon)
	if ok {
		t.Fatal("contraseña incorrecta aceptada")
	}
	if _, rehash, _ := VerifyPassword("correct horse battery", h, DefaultArgon2); !rehash {
		t.Fatal("parámetros distintos deben pedir re-hash")
	}
	if _, _, err := VerifyPassword("x", "$bcrypt$foo", fastArgon); err == nil {
		t.Fatal("hash inválido aceptado")
	}
}

func TestPasswordPolicy(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"corta":                     PwTooShort,
		strings.Repeat("a", 129):    PwTooLong,
		"password1234":              PwCommon,
		"soy-maria.lopez-2026":      PwPersonal,
		"una frase larga y rara 42": "",
		"Maria Lopez es genial!!":   PwPersonal,
		"áéíóú ñandú cielo azul":    "",
	}
	for pw, want := range cases {
		if got := ValidateNewPassword(pw, "maria.lopez@isp.example", "Maria Lopez"); got != want {
			t.Errorf("%q: got %q, want %q", pw, got, want)
		}
	}
}

func TestTOTPRFC6238(t *testing.T) {
	t.Parallel()
	secret := []byte("12345678901234567890")
	// RFC 6238 apéndice B (SHA1), últimos 6 dígitos.
	vectors := map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"}
	for ts, want := range vectors {
		if got := TOTPCode(secret, TOTPStep(time.Unix(ts, 0))); got != want {
			t.Errorf("t=%d: %s, want %s", ts, got, want)
		}
	}
	now := time.Unix(1234567890, 0)
	step, ok := VerifyTOTP(secret, "005924", now, 0)
	if !ok {
		t.Fatal("código válido rechazado")
	}
	if _, ok := VerifyTOTP(secret, "005924", now, step); ok {
		t.Fatal("reutilización del mismo paso aceptada")
	}
	prev := TOTPCode(secret, TOTPStep(now)-1)
	if _, ok := VerifyTOTP(secret, prev, now, 0); !ok {
		t.Fatal("ventana -1 rechazada")
	}
	old := TOTPCode(secret, TOTPStep(now)-3)
	if _, ok := VerifyTOTP(secret, old, now, 0); ok {
		t.Fatal("código fuera de ventana aceptado")
	}
	uri := OTPAuthURI("Horus Flow", "a@b.c", secret)
	if !strings.HasPrefix(uri, "otpauth://totp/Horus%20Flow:a@b.c?") || !strings.Contains(uri, "secret="+EncodeTOTPSecret(secret)) {
		t.Fatalf("uri = %s", uri)
	}
	codes, err := NewRecoveryCodes()
	if err != nil || len(codes) != 10 || len(codes[0]) != 10 {
		t.Fatalf("recovery = %v %v", codes, err)
	}
}

func TestLockDelay(t *testing.T) {
	t.Parallel()
	want := map[int]time.Duration{0: 0, 4: 0, 5: time.Second, 6: 2 * time.Second, 7: 4 * time.Second, 30: LockMax, 100: LockMax}
	for n, d := range want {
		if got := LockDelay(n); got != d {
			t.Errorf("LockDelay(%d) = %v, want %v", n, got, d)
		}
	}
}

func TestSealer(t *testing.T) {
	t.Parallel()
	s, err := NewSealer(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ct, dek, err := s.Seal([]byte("secreto"), []byte("totp:u1"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := s.Open(ct, dek, []byte("totp:u1"))
	if err != nil || string(pt) != "secreto" {
		t.Fatalf("open = %q %v", pt, err)
	}
	if _, err := s.Open(ct, dek, []byte("totp:u2")); err == nil {
		t.Fatal("AAD de otro usuario aceptada")
	}
	if _, err := NewSealer([]byte("corta")); err == nil {
		t.Fatal("KEK corta aceptada")
	}
}

// El catálogo embebido es copia exacta del contrato C7.
func TestCatalogMatchesContract(t *testing.T) {
	t.Parallel()
	root, err := archtest.FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(root, "packages/schemas/permissions/v0/permissions.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, PermissionsYAML()) {
		t.Fatal("services/auth/internal/domain/permissions.v0.yaml difiere del contrato: cópielo de packages/schemas/permissions/v0/permissions.yaml")
	}
	c, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	admin, ok := c.TenantRole(RoleTenantAdmin)
	if !ok || !admin.RequiresMFA || !slices.Contains(admin.Permissions, "devices.credentials.reveal") {
		t.Fatalf("tenant_admin = %+v", admin)
	}
	viewer, _ := c.TenantRole("viewer")
	if slices.Contains(viewer.Permissions, "customers.read") {
		t.Fatal("viewer no debe ver clientes")
	}
	perms, mfa := c.PlatformPermissions([]string{RolePlatformAdmin})
	if !mfa || !slices.Contains(perms, "platform.tenants.manage") || slices.Contains(perms, "platform.support_access") {
		t.Fatalf("platform_admin = %v %v", perms, mfa)
	}
	if SystemRoleID("noc") != uuid.NewSHA1(roleNamespace, []byte("role:noc")) || SystemRoleID("noc") == SystemRoleID("viewer") {
		t.Fatal("IDs de rol no deterministas")
	}
}
