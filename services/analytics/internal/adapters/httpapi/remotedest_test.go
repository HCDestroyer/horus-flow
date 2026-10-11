package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
)

type fakeDestinations struct {
	list []RemoteDestination
	err  error
}

func (f fakeDestinations) ListRemoteDestinations(context.Context) ([]RemoteDestination, error) {
	return f.list, f.err
}

func remoteServer(t *testing.T, src RemoteDestinations) (http.Handler, *authz.Signer) {
	t.Helper()
	key, err := authz.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := authz.NewSigner(key, "horus-auth", time.Minute, nil)
	ks := authz.KeySet{}
	ks.Add(signer.PublicKey())
	mux := httpx.NewMux(nil, slog.New(slog.DiscardHandler))
	NewRemoteDestinations(authz.NewGuard(authz.NewVerifier(ks, "horus-auth", nil)), src, nil).Mount(mux.ForService("analytics"))
	return mux.Handler(), signer
}

func remoteToken(t *testing.T, s *authz.Signer, scope string, perms map[string][]string) string {
	t.Helper()
	c := authz.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.NewString()}, Typ: authz.TypeUser, Scope: scope, Perms: perms}
	if scope == authz.ScopeTenant {
		c.TID = uuid.NewString()
	}
	tok, _, err := s.Sign(c)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func getRemote(h http.Handler, tok string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/platform/remote-destinations", nil)
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var storageManage = map[string][]string{PermStorageManage: {"*"}}

func TestRemoteDestinationsEmptyWithoutSource(t *testing.T) {
	h, s := remoteServer(t, nil)
	w := getRemote(h, remoteToken(t, s, authz.ScopePlatform, storageManage))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["data"]) != "[]" {
		t.Fatalf("data = %s, want []", raw["data"])
	}
}

func TestRemoteDestinationsShape(t *testing.T) {
	id := uuid.New()
	at := time.Date(2026, 10, 11, 3, 0, 0, 0, time.FixedZone("COT", -5*3600))
	lag := int64(90)
	h, s := remoteServer(t, fakeDestinations{list: []RemoteDestination{
		{ID: id, Kind: "sftp", Name: "NAS oficina", Status: "ok", LastSuccessAt: &at, LagSeconds: &lag},
		{ID: uuid.New(), Kind: "gdrive", Name: "Drive", Status: "disabled"},
	}})
	w := getRemote(h, remoteToken(t, s, authz.ScopePlatform, storageManage))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Data) != 2 {
		t.Fatalf("body = %s, err %v", w.Body, err)
	}
	want := map[string]any{"id": id.String(), "kind": "sftp", "name": "NAS oficina", "status": "ok",
		"last_success_at": "2026-10-11T08:00:00.000Z", "lag_seconds": 90.0}
	if len(body.Data[0]) != len(want) {
		t.Fatalf("unexpected fields (secrets must never be serialized): %v", body.Data[0])
	}
	for k, v := range want {
		if body.Data[0][k] != v {
			t.Errorf("%s = %v, want %v", k, body.Data[0][k], v)
		}
	}
	if v, ok := body.Data[1]["last_success_at"]; !ok || v != nil {
		t.Errorf("last_success_at = %v (present %v), want null", v, ok)
	}
	if v, ok := body.Data[1]["lag_seconds"]; !ok || v != nil {
		t.Errorf("lag_seconds = %v (present %v), want null", v, ok)
	}
}

func TestRemoteDestinationsSourceError(t *testing.T) {
	h, s := remoteServer(t, fakeDestinations{err: errors.New("jobs db down")})
	if w := getRemote(h, remoteToken(t, s, authz.ScopePlatform, storageManage)); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestRemoteDestinationsRequiresPlatformPermission(t *testing.T) {
	h, s := remoteServer(t, nil)
	if w := getRemote(h, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", w.Code)
	}
	if w := getRemote(h, remoteToken(t, s, authz.ScopeTenant, storageManage)); w.Code == http.StatusOK {
		t.Fatalf("tenant token = %d", w.Code)
	}
	if w := getRemote(h, remoteToken(t, s, authz.ScopePlatform, map[string][]string{"platform.status.read": {"*"}})); w.Code != http.StatusForbidden {
		t.Fatalf("missing permission = %d", w.Code)
	}
}
