package updatecheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSemverCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0}, {"1.2.10", "1.2.9", 1}, {"1.10.0", "1.9.9", 1}, {"2.0.0", "1.99.99", 1},
		{"1.3.0", "1.3.0-beta.1", 1}, {"1.3.0-beta.2", "1.3.0-beta.10", -1}, {"1.3.0-rc.1", "1.3.0-beta.9", 1},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1}, {"1.0.0-alpha.beta", "1.0.0-alpha.1", 1}, {"v1.0.0+build", "1.0.0", 0},
	}
	for _, c := range cases {
		a, ok1 := ParseSemver(c.a)
		b, ok2 := ParseSemver(c.b)
		if !ok1 || !ok2 {
			t.Fatalf("no parsea %s/%s", c.a, c.b)
		}
		if got := a.Compare(b); got != c.want {
			t.Errorf("%s vs %s = %d, quiero %d", c.a, c.b, got, c.want)
		}
	}
	for _, bad := range []string{"1.2", "1.2.x", "1.2.3-", "", "dev"} {
		if _, ok := ParseSemver(bad); ok {
			t.Errorf("%q aceptado", bad)
		}
	}
}

const ghReleases = `[
 {"tag_name":"v1.3.0-beta.1","prerelease":true,"draft":false,"html_url":"https://x/b1","body":"beta"},
 {"tag_name":"v1.2.4","prerelease":false,"draft":false,"html_url":"https://x/124","body":"arreglos","published_at":"2026-10-01T10:00:00Z"},
 {"tag_name":"v1.2.3","prerelease":false,"draft":false,"html_url":"https://x/123"},
 {"tag_name":"v9.9.9","prerelease":false,"draft":true},
 {"tag_name":"nightly","prerelease":false,"draft":false}
]`

func serve(t *testing.T, code int, body string) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s.URL
}

func TestCheckGitHub(t *testing.T) {
	url := serve(t, 200, ghReleases)
	c := New(Options{Current: "1.2.3", Channel: "stable", Source: url})
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := c.Status()
	if st.Status != StatusAvailable || st.Latest == nil || st.Latest.Version != "1.2.4" || !st.Latest.PatchOnly {
		t.Fatalf("estado = %+v latest=%+v", st, st.Latest)
	}
	if st.Latest.UpgradeCommand != "sudo horus-ctl upgrade --to 1.2.4" || st.Latest.Notes == nil || *st.Latest.Notes != "arreglos" {
		t.Fatalf("latest = %+v", st.Latest)
	}
	beta := New(Options{Current: "1.2.4", Channel: "beta", Source: url})
	_ = beta.Check(context.Background())
	if bs := beta.Status(); bs.Latest == nil || bs.Latest.Version != "1.3.0-beta.1" || bs.Latest.PatchOnly {
		t.Fatalf("beta = %+v", bs.Latest)
	}
	upToDate := New(Options{Current: "1.2.4", Source: url})
	_ = upToDate.Check(context.Background())
	if s := upToDate.Status(); s.Status != StatusUpToDate || s.UpdateAvailable || s.CheckedAt == nil {
		t.Fatalf("al día = %+v", s)
	}
}

func TestCheckManifest(t *testing.T) {
	url := serve(t, 200, `{"schema":1,"channels":{"stable":{"version":"2.0.0","notes_url":"https://n"}}}`)
	c := New(Options{Current: "1.9.0", Source: url})
	_ = c.Check(context.Background())
	if s := c.Status(); s.Latest == nil || s.Latest.Version != "2.0.0" || s.Latest.PatchOnly || s.Latest.NotesURL != "https://n" {
		t.Fatalf("manifiesto = %+v", s.Latest)
	}
}

func TestUncheckedOffline(t *testing.T) {
	for _, src := range []string{"http://127.0.0.1:1/nada", serve(t, 503, "caída"), serve(t, 200, "<html>")} {
		c := New(Options{Current: "1.0.0", Source: src})
		if err := c.Check(context.Background()); err == nil {
			t.Fatalf("%s: sin error", src)
		}
		if s := c.Status(); s.Status != StatusUnchecked || s.Error == nil || s.CheckedAt == nil {
			t.Fatalf("%s: estado = %+v", src, s)
		}
	}
	if s := New(Options{Current: "1.0.0"}).Status(); s.Status != StatusUnchecked {
		t.Fatalf("inicial = %+v", s)
	}
}

func TestPickPatchOnly(t *testing.T) {
	rels, err := Parse([]byte(ghReleases))
	if err != nil {
		t.Fatal(err)
	}
	if r := Pick(rels, "stable", "1.2"); r == nil || r.Version != "1.2.4" {
		t.Fatalf("parche = %+v", r)
	}
	if r := Pick(rels, "stable", "1.1"); r != nil {
		t.Fatalf("sin parches de 1.1 = %+v", r)
	}
}
