package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// redacted es el valor que sustituye a un secreto en la configuración efectiva.
const redacted = "[REDACTED]"

// secretMarkers: una variable cuyo nombre contiene alguno es secreta y su
// valor nunca sale del proceso (docs/observability.md §3.3).
var secretMarkers = []string{"PASSWORD", "SECRET", "TOKEN", "KEK", "PRIVATE_KEY", "SIGNING_KEY", "CURSOR_KEY", "API_KEY",
	"CREDENTIAL", "PASSPHRASE", "COMMUNITY", "_PSK", "SEED_ADMIN"}

// extraEnv son las variables sin prefijo HORUS_ que también describen la
// ejecución (sin secretos).
var extraEnv = []string{"GOMEMLIMIT", "GOMAXPROCS", "GOGC", "GRPC_TLS_MODE", "TZ", "OTEL_SERVICE_NAME", "OTEL_TRACES_SAMPLER",
	"OTEL_TRACES_SAMPLER_ARG", "OTEL_EXPORTER_OTLP_ENDPOINT"}

// effectiveConfig devuelve la configuración efectiva del proceso sin
// secretos: variables HORUS_* (las *_FILE muestran la ruta, nunca el
// contenido; las secretas, [REDACTED]; las URL/DSN, sin contraseña) y unas
// pocas de runtime. La usan el evento process_started (diff entre
// arranques) y `horus diagnose`.
func effectiveConfig(environ []string) map[string]string {
	out := map[string]string{}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if !strings.HasPrefix(k, "HORUS_") && !slices.Contains(extraEnv, k) {
			continue
		}
		out[k] = sanitizeValue(k, v)
	}
	return out
}

func sanitizeValue(key, v string) string {
	if strings.HasSuffix(key, "_FILE") {
		return v // ruta del secreto montado, no su contenido
	}
	upper := strings.ToUpper(key)
	for _, m := range secretMarkers {
		if strings.Contains(upper, m) {
			if v == "" {
				return ""
			}
			return redacted
		}
	}
	if strings.Contains(upper, "PUBLIC_KEYS") || strings.Contains(upper, "_PEM") || strings.HasPrefix(v, "-----BEGIN") {
		return "[" + strconv.Itoa(len(v)) + " bytes]"
	}
	if strings.Contains(v, "://") {
		v = redactURLs(v)
	}
	if len(v) > 512 {
		v = v[:512] + "…"
	}
	return v
}

// redactURLs quita la contraseña de cada URL de v (lista separada por comas).
func redactURLs(v string) string {
	parts := strings.Split(v, ",")
	for i, p := range parts {
		u, err := url.Parse(strings.TrimSpace(p))
		if err != nil || u.User == nil {
			continue
		}
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
		}
		q := u.Query()
		for k := range q {
			lk := strings.ToLower(k)
			if strings.Contains(lk, "pass") || strings.Contains(lk, "token") || strings.Contains(lk, "secret") {
				q.Set(k, "xxxxx")
			}
		}
		u.RawQuery = q.Encode()
		parts[i] = u.String()
	}
	return strings.Join(parts, ",")
}

// configHash es un resumen estable de la configuración efectiva.
func configHash(cfg map[string]string) string {
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k + "=" + cfg[k] + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// configDiff devuelve las claves añadidas, quitadas o cambiadas entre dos
// configuraciones efectivas (solo nombres: los valores están en cada evento).
func configDiff(prev, cur map[string]string) []string {
	var out []string
	for k, v := range cur {
		if pv, ok := prev[k]; !ok || pv != v {
			out = append(out, k)
		}
	}
	for k := range prev {
		if _, ok := cur[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}
