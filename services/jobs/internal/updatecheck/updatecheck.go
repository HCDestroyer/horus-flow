// Package updatecheck comprueba si hay una versión nueva de Horus Flow en el
// canal de la instalación (stable o beta) consultando la fuente de versiones:
// la API de releases de GitHub o un manifiesto latest.json publicado con cada
// release (scripts/release/build-release.sh). Sin salida a Internet la
// comprobación falla en silencio y el estado queda "unchecked"
// (docs/install-debian.md, Actualizaciones).
package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Estados.
const (
	StatusUpToDate  = "up_to_date"
	StatusAvailable = "available"
	StatusUnchecked = "unchecked"
)

// MaxNotes acota las notas del cambio que se guardan.
const MaxNotes = 8 << 10

// Release es una versión publicada.
type Release struct {
	Version     string     `json:"version"`
	PublishedAt *time.Time `json:"published_at"`
	NotesURL    string     `json:"notes_url"`
	Notes       *string    `json:"notes"`
	Prerelease  bool       `json:"-"`
}

// Latest es la versión nueva disponible (contrato UpdateStatus.latest).
type Latest struct {
	Version        string     `json:"version"`
	PublishedAt    *time.Time `json:"published_at"`
	NotesURL       string     `json:"notes_url"`
	Notes          *string    `json:"notes"`
	UpgradeCommand string     `json:"upgrade_command"`
	PatchOnly      bool       `json:"patch_only"`
}

// Status es el resultado de la última comprobación (contrato UpdateStatus).
type Status struct {
	Status          string     `json:"status"`
	CurrentVersion  string     `json:"current_version"`
	Channel         string     `json:"channel"`
	UpdateAvailable bool       `json:"update_available"`
	CheckedAt       *time.Time `json:"checked_at"`
	Latest          *Latest    `json:"latest"`
	Error           *string    `json:"error"`
}

// Options configura el Checker.
type Options struct {
	Current string
	Channel string
	Source  string
	Repo    string
	Client  *http.Client
	Now     func() time.Time
}

// Checker guarda el último estado y lo recalcula con Check.
type Checker struct {
	o  Options
	mu sync.RWMutex
	st Status
}

// New crea el Checker con estado inicial "unchecked".
func New(o Options) *Checker {
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Channel != "beta" {
		o.Channel = "stable"
	}
	if o.Repo == "" {
		o.Repo = "hcdestroyer/horus-flow"
	}
	c := &Checker{o: o}
	msg := "todavía no comprobado"
	c.st = Status{Status: StatusUnchecked, CurrentVersion: o.Current, Channel: o.Channel, Error: &msg}
	return c
}

// Status devuelve el último estado.
func (c *Checker) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.st
}

// Disable deja el estado en "unchecked" con un motivo (comprobación desactivada).
func (c *Checker) Disable(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.Error = &reason
}

// Check consulta la fuente y actualiza el estado. Devuelve el error de la
// consulta (ya reflejado como "unchecked"), para el log.
func (c *Checker) Check(ctx context.Context) error {
	now := c.o.Now().UTC()
	rels, err := c.fetch(ctx)
	st := Status{CurrentVersion: c.o.Current, Channel: c.o.Channel, CheckedAt: &now}
	if err != nil {
		msg := err.Error()
		st.Status, st.Error = StatusUnchecked, &msg
		c.set(st)
		return err
	}
	best := Pick(rels, c.o.Channel, "")
	cur, curOK := ParseSemver(c.o.Current)
	st.Status = StatusUpToDate
	if best != nil {
		v, _ := ParseSemver(best.Version)
		if !curOK || v.Compare(cur) > 0 {
			st.Status, st.UpdateAvailable = StatusAvailable, true
			st.Latest = &Latest{
				Version: best.Version, PublishedAt: best.PublishedAt, NotesURL: best.NotesURL, Notes: best.Notes,
				UpgradeCommand: "sudo horus-ctl upgrade --to " + best.Version,
				PatchOnly:      curOK && v.Major == cur.Major && v.Minor == cur.Minor,
			}
			if st.Latest.NotesURL == "" {
				st.Latest.NotesURL = fmt.Sprintf("https://github.com/%s/releases/tag/v%s", c.o.Repo, best.Version)
			}
		}
	}
	c.set(st)
	return nil
}

func (c *Checker) set(st Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st = st
}

func (c *Checker) fetch(ctx context.Context) ([]Release, error) {
	if c.o.Source == "" {
		return nil, errors.New("sin fuente de versiones")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.o.Source, nil)
	if err != nil {
		return nil, fmt.Errorf("fuente de versiones inválida: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json, application/json")
	req.Header.Set("User-Agent", "horus-flow/"+c.o.Current)
	resp, err := c.o.Client.Do(req)
	if err != nil {
		return nil, errors.New("sin acceso a la fuente de versiones (¿servidor sin salida a Internet?)")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("la fuente de versiones respondió HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, errors.New("respuesta incompleta de la fuente de versiones")
	}
	return Parse(body)
}

// Parse lee la API de releases de GitHub (array) o un latest.json
// ({"schema":1,"channels":{"stable":{...},"beta":{...}}}).
func Parse(body []byte) ([]Release, error) {
	trim := strings.TrimSpace(string(body))
	if strings.HasPrefix(trim, "[") {
		var gh []struct {
			TagName     string     `json:"tag_name"`
			Draft       bool       `json:"draft"`
			Prerelease  bool       `json:"prerelease"`
			HTMLURL     string     `json:"html_url"`
			Body        string     `json:"body"`
			PublishedAt *time.Time `json:"published_at"`
		}
		if err := json.Unmarshal(body, &gh); err != nil {
			return nil, errors.New("respuesta de releases ilegible")
		}
		out := make([]Release, 0, len(gh))
		for _, r := range gh {
			if r.Draft {
				continue
			}
			rel := Release{Version: strings.TrimPrefix(r.TagName, "v"), PublishedAt: r.PublishedAt, NotesURL: r.HTMLURL, Prerelease: r.Prerelease}
			if r.Body != "" {
				n := r.Body
				if len(n) > MaxNotes {
					n = n[:MaxNotes]
				}
				rel.Notes = &n
			}
			out = append(out, rel)
		}
		return out, nil
	}
	var m struct {
		Channels map[string]struct {
			Version     string     `json:"version"`
			PublishedAt *time.Time `json:"published_at"`
			NotesURL    string     `json:"notes_url"`
			Notes       *string    `json:"notes"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(body, &m); err != nil || m.Channels == nil {
		return nil, errors.New("manifiesto de versiones ilegible (se esperaba latest.json con channels)")
	}
	var out []Release
	for ch, v := range m.Channels {
		out = append(out, Release{Version: strings.TrimPrefix(v.Version, "v"), PublishedAt: v.PublishedAt,
			NotesURL: v.NotesURL, Notes: v.Notes, Prerelease: ch != "stable"})
	}
	return out, nil
}

// Pick elige la mayor versión semver del canal (stable: sin prerelease; beta:
// todas). Con majorMinor ("1.2") se limita a esa X.Y (parches).
func Pick(rels []Release, channel, majorMinor string) *Release {
	var best *Release
	var bestV Semver
	for i := range rels {
		v, ok := ParseSemver(rels[i].Version)
		if !ok {
			continue
		}
		if channel != "beta" && (rels[i].Prerelease || v.Pre != "") {
			continue
		}
		if majorMinor != "" && fmt.Sprintf("%d.%d", v.Major, v.Minor) != majorMinor {
			continue
		}
		if best == nil || v.Compare(bestV) > 0 {
			best, bestV = &rels[i], v
		}
	}
	return best
}

// Semver es una versión semántica X.Y.Z[-pre].
type Semver struct {
	Major, Minor, Patch int
	Pre                 string
}

// ParseSemver lee "X.Y.Z[-pre]" (con o sin "v"); ignora +build.
func ParseSemver(s string) (Semver, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var v Semver
	core := s
	if i := strings.IndexByte(s, '-'); i >= 0 {
		core, v.Pre = s[:i], s[i+1:]
		if v.Pre == "" {
			return Semver{}, false
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Semver{}, false
	}
	nums := [3]int{}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" {
			return Semver{}, false
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v, true
}

// Compare devuelve -1, 0 o 1 según la precedencia de semver.org §11.
func (a Semver) Compare(b Semver) int {
	for _, d := range [3][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if d[0] != d[1] {
			if d[0] > d[1] {
				return 1
			}
			return -1
		}
	}
	switch {
	case a.Pre == b.Pre:
		return 0
	case a.Pre == "":
		return 1
	case b.Pre == "":
		return -1
	}
	x, y := strings.Split(a.Pre, "."), strings.Split(b.Pre, ".")
	for i := 0; i < len(x) || i < len(y); i++ {
		if i >= len(x) {
			return -1
		}
		if i >= len(y) {
			return 1
		}
		nx, ex := strconv.Atoi(x[i])
		ny, ey := strconv.Atoi(y[i])
		switch {
		case ex == nil && ey == nil:
			if nx != ny {
				if nx > ny {
					return 1
				}
				return -1
			}
		case ex == nil:
			return -1
		case ey == nil:
			return 1
		case x[i] != y[i]:
			if x[i] > y[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}
