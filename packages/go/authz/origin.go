package authz

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/hcdestroyer/horus-flow/packages/go/problem"
)

// HeaderRequestedWith es la cabecera anti-CSRF de las rutas con cookie.
const HeaderRequestedWith = "X-Requested-With"

// RequestedWithValue es su valor fijo.
const RequestedWithValue = "horus"

// Origins es la lista HORUS_ALLOWED_ORIGINS (D14) normalizada.
type Origins []string

// ParseOrigins normaliza una lista de orígenes (`https://host[:puerto]`).
// Si está vacía y publicBaseURL no, el origen permitido es el de
// publicBaseURL (api.md §1.11).
func ParseOrigins(list []string, publicBaseURL string) Origins {
	var out Origins
	for _, o := range list {
		if n := normalizeOrigin(o); n != "" {
			out = append(out, n)
		}
	}
	if len(out) == 0 && publicBaseURL != "" {
		if n := normalizeOrigin(publicBaseURL); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func normalizeOrigin(s string) string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// Allowed indica si origin está en la lista. Una lista vacía (desarrollo sin
// HORUS_PUBLIC_BASE_URL) acepta cualquier origen.
func (o Origins) Allowed(origin string) bool {
	if len(o) == 0 {
		return true
	}
	n := normalizeOrigin(origin)
	for _, a := range o {
		if a == n {
			return true
		}
	}
	return false
}

// CheckCookieRequest aplica la defensa CSRF de las rutas autenticadas por
// cookie (`/auth/refresh`, `/auth/logout`, `/kiosk/*`): cabecera
// `X-Requested-With: horus` y `Origin` permitido. Escribe `403
// ORIGIN_NOT_ALLOWED` y devuelve false si no se cumple.
func (o Origins) CheckCookieRequest(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get(HeaderRequestedWith) != RequestedWithValue {
		problem.Std(w, r, http.StatusForbidden, problem.CodeOriginNotAllowed,
			problem.WithDetail("Falta la cabecera X-Requested-With: horus."))
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" && len(o) > 0 {
		problem.Std(w, r, http.StatusForbidden, problem.CodeOriginNotAllowed)
		return false
	}
	if origin != "" && !o.Allowed(origin) {
		problem.Std(w, r, http.StatusForbidden, problem.CodeOriginNotAllowed)
		return false
	}
	return true
}
