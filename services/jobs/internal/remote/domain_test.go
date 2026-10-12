package remote

import (
	"errors"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
)

func ptr[T any](v T) *T { return &v }

func TestValidHost(t *testing.T) {
	t.Parallel()
	good := []string{"nas.isp.example", "nas", "192.0.2.10", "2001:db8::1", "a-b.c", "NAS01.local."}
	bad := []string{"", "sftp://nas", "nas/path", "user@nas", "nas:22", "-nas", "nas-", "a..b", "1.2.3", "999.1.1.1",
		"[2001:db8::1]", "fe80::1%eth0", "0.0.0.0", "nas name", "ñandú.example"}
	for _, h := range good {
		if !ValidHost(h) {
			t.Errorf("ValidHost(%q) = false", h)
		}
	}
	for _, h := range bad {
		if ValidHost(h) {
			t.Errorf("ValidHost(%q) = true", h)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"/backups/horus/": "/backups/horus", "backups": "backups", "/": "/", "a/./b": "a/./b", "///": "/"} {
		got, ok := NormalizePath(in)
		if !ok || got != want {
			t.Errorf("NormalizePath(%q) = %q, %v; quiero %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "../x", "/a/../b", "a/..", "a\\b", "a\x00b", "a\nb"} {
		if _, ok := NormalizePath(in); ok {
			t.Errorf("NormalizePath(%q) aceptada", in)
		}
	}
}

func TestValidateCreate(t *testing.T) {
	t.Parallel()
	d, err := ValidateCreate(CreateInput{Kind: ptr("sftp"), Name: ptr(" NAS "), Host: ptr("nas.example"), Username: ptr("horus"),
		Path: ptr("/copias/")})
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "NAS" || d.Port != 22 || d.RetentionDays != 30 || d.Path != "/copias" || d.Enabled {
		t.Fatalf("destino = %+v", d)
	}
	_, err = ValidateCreate(CreateInput{Kind: ptr("gdrive"), Name: ptr(""), Host: ptr("ssh://x"), Port: ptr(0), Username: ptr("a b"),
		Path: ptr("../x"), RetentionDays: ptr(0)})
	e, ok := apperr.As(err)
	if !ok || len(e.Fields) != 7 {
		t.Fatalf("err = %v (%+v)", err, e)
	}
	if _, err := ValidateCreate(CreateInput{}); err == nil {
		t.Fatal("alta vacía aceptada")
	}
}

func TestApplyPatch(t *testing.T) {
	t.Parallel()
	fp := "SHA256:" + "A123456789012345678901234567890123456789012"[:43]
	now := time.Now()
	cur := &Destination{Name: "n", Host: "h.example", Port: 22, Username: "u", Path: "/p", RetentionDays: 30, Enabled: true,
		HostKeyFingerprint: &fp, CredentialCiphertext: []byte{1}, CredentialDEK: []byte{1}, VerifiedAt: &now, Version: 3}

	// Cambiar el host borra la huella, deshabilita y reinicia el ledger.
	next, ch, err := ApplyPatch(cur, Patch{Host: ptr("otro.example"), Enabled: ptr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if next.Enabled || next.HostKeyFingerprint != nil || next.VerifiedAt != nil || !ch.ResetHostKey || !ch.ResetLedger {
		t.Fatalf("next = %+v ch = %+v", next, ch)
	}
	// Cambiar la ruta reinicia el ledger pero no la huella.
	next, ch, err = ApplyPatch(cur, Patch{Path: ptr("/otra")})
	if err != nil || !ch.ResetLedger || ch.ResetHostKey || next.HostKeyFingerprint == nil || !next.Enabled {
		t.Fatalf("next = %+v ch = %+v err = %v", next, ch, err)
	}
	// Habilitar exige credenciales, huella y prueba.
	off := *cur
	off.Enabled = false
	for name, mod := range map[string]func(d *Destination){
		"sin credenciales": func(d *Destination) { d.CredentialCiphertext = nil },
		"sin huella":       func(d *Destination) { d.HostKeyFingerprint = nil },
		"sin prueba":       func(d *Destination) { d.VerifiedAt = nil },
	} {
		d := off
		mod(&d)
		_, _, err := ApplyPatch(&d, Patch{Enabled: ptr(true)})
		if e, ok := apperr.As(err); !ok || e.Code != CodeNotReady {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if next, _, err := ApplyPatch(&off, Patch{Enabled: ptr(true)}); err != nil || !next.Enabled {
		t.Fatalf("habilitar listo: %v", err)
	}
	if _, _, err := ApplyPatch(cur, Patch{}); err == nil {
		t.Fatal("PATCH vacío aceptado")
	}
	if _, _, err := ApplyPatch(cur, Patch{RetentionDays: ptr(4000)}); err == nil {
		t.Fatal("retención fuera de rango aceptada")
	}
}

func TestComputeStatus(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { v := now.Add(-d); return &v }
	e := ptr("boom")
	cases := []struct {
		name string
		d    Destination
		want string
		lag  int64
	}{
		{"deshabilitado", Destination{Enabled: false, LastSuccessAt: ago(time.Hour)}, StatusDisabled, 3600},
		{"pendiente", Destination{Enabled: true}, StatusPending, -1},
		{"falla sin éxito previo", Destination{Enabled: true, LastAttemptAt: ago(time.Minute), LastError: e}, StatusFailing, -1},
		{"falla tras éxito", Destination{Enabled: true, LastSuccessAt: ago(time.Hour), LastAttemptAt: ago(time.Minute), LastError: e}, StatusFailing, 3600},
		{"retrasado", Destination{Enabled: true, LastSuccessAt: ago(27 * time.Hour), LastAttemptAt: ago(27 * time.Hour)}, StatusLagging, 27 * 3600},
		{"ok", Destination{Enabled: true, LastSuccessAt: ago(10 * time.Minute), LastAttemptAt: ago(10 * time.Minute)}, StatusOK, 600},
	}
	for _, c := range cases {
		got, lag := ComputeStatus(&c.d, now, 26*time.Hour)
		if got != c.want {
			t.Errorf("%s: estado = %s, quiero %s", c.name, got, c.want)
		}
		if (c.lag < 0) != (lag == nil) || (lag != nil && *lag != c.lag) {
			t.Errorf("%s: lag = %v, quiero %d", c.name, lag, c.lag)
		}
	}
}

func TestNotReadyIsConflict(t *testing.T) {
	t.Parallel()
	var e *apperr.Error
	if !errors.As(notReady("x"), &e) || e.Status() != 409 {
		t.Fatal("notReady no es 409")
	}
}
