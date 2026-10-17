package api

import (
	"context"
	"errors"
)

// ServiceAPITokens → APITokenExchanger.
const ServiceAPITokens = "auth.APITokens"

// APITokenPrefix identifica los API tokens de integración de Horus (ayuda a
// los escáneres de secretos): `hf_pat_<32 B base62>` (docs/security.md §5.4).
const APITokenPrefix = "hf_pat_"

// ErrAPITokenInvalid: token desconocido, caducado, revocado, o su creador ya
// no puede actuar en el ISP (usuario, membresía o ISP no activos, o ningún
// permiso en común). El gateway responde 401 sin distinguir el motivo.
var ErrAPITokenInvalid = errors.New("auth: api token invalid")

// APITokenExchanger canjea un API token `hf_pat_…` por un access JWT de corta
// vida (2 min, `typ = service`, `scope = tenant`, `via = api_token`) con la
// intersección entre los permisos del token y los permisos ACTUALES de quien
// lo creó en el ISP (con sus alcances). auth cachea el canje por token hasta
// poco antes de su caducidad y lo invalida al revocar. ip es la IP del
// cliente (último uso).
type APITokenExchanger interface {
	ExchangeAPIToken(ctx context.Context, raw, ip string) (string, error)
}
