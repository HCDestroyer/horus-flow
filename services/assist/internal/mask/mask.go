// Package mask seudonimiza los datos que el asistente de IA envía al proveedor
// (docs/assist.md §Privacidad). La IA ve comportamiento, puertos, ASN y el
// estado de routers y exportadores, nunca datos personales de un cliente:
//
//   - las IP de clientes se sustituyen por un seudónimo estable dentro de un
//     análisis: "Cliente N (10.20.x.x)" (IPv4 conserva el /16; IPv6, el /32);
//   - alias, nombres, notas, correos, teléfonos, usuarios y comandos ya
//     rellenados con la IP del cliente se eliminan;
//   - cualquier aparición posterior de una IP ya seudonimizada en un texto se
//     reemplaza por su seudónimo.
//
// El mapa de seudónimos vive solo en el servidor (un Masker por análisis o
// por llamada MCP); la respuesta de la IA se muestra tal cual, enmascarada.
package mask

import (
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Claves que nunca salen hacia el proveedor (en cualquier nivel).
var dropAlways = map[string]bool{
	"alias": true, "aliases": true, "notes": true, "note": true, "email": true, "phone": true, "contact": true,
	"rendered_commands": true, "acknowledged_by": true, "created_by": true, "updated_by": true, "actor_id": true,
	"user_id": true, "public_key": true, "customer_message_rendered": true, "username": true, "password": true,
	// Identidad en ArgosISP (ADR-0030): ids y usuario PPPoE identifican a la persona.
	"external_customer_id": true, "external_service_id": true, "user_ppp": true,
}

// Claves que, dentro de un contexto de cliente, identifican a la persona.
var dropInCustomer = map[string]bool{
	"name": true, "label": true, "display_name": true, "description": true, "hostname": true, "mac": true,
}

// Claves de la identidad en ArgosISP (bloque "external", ADR-0030) que
// identifican a la persona: se quitan como el alias.
var dropInExternal = map[string]bool{
	"customer_id": true, "service_id": true, "code": true, "display_name": true, "plan": true, "name": true,
}

// Claves que contienen la IP del cliente (en cualquier contexto).
var customerIPKeys = map[string]bool{
	"address": true, "client_ip": true, "customer_ip": true, "customer_address": true, "addresses": true,
}

// Claves que abren un contexto de cliente (su contenido es del cliente).
var customerContextKeys = map[string]bool{"customer": true, "customers": true}

// Claves que abren un bloque de identidad externa (ArgosISP).
var externalKeys = map[string]bool{"external": true, "external_identity": true, "current": true, "history": true}

var ipLike = regexp.MustCompile(`(?:\d{1,3}\.){3}\d{1,3}(?:/\d{1,2})?|[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,7}(?:/\d{1,3})?`)

// Masker seudonimiza los clientes de un análisis. Es seguro para uso concurrente.
type Masker struct {
	mu      sync.Mutex
	byIP    map[string]string // IP (texto canónico o tal cual) → seudónimo
	order   []string          // IPs en orden de alta (para reemplazos estables)
	byAlias map[string]string // seudónimo en minúsculas ("cliente 3") → IP real
	n       int
}

// New crea un Masker vacío.
func New() *Masker {
	return &Masker{byIP: map[string]string{}, byAlias: map[string]string{}}
}

// CustomerIP devuelve el seudónimo de la IP de cliente ip (lo crea si no existe).
// Acepta también direcciones ya enmascaradas por Horus ("10.20.0.•••").
func (m *Masker) CustomerIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	key := ip
	if p, err := netip.ParsePrefix(ip); err == nil && p.IsSingleIP() {
		key = p.Addr().String()
	} else if a, err := netip.ParseAddr(ip); err == nil {
		key = a.Unmap().String()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.byIP[key]; ok {
		return s
	}
	m.n++
	s := fmt.Sprintf("Cliente %d (%s)", m.n, Coarse(key))
	m.byIP[key] = s
	m.order = append(m.order, key)
	m.byAlias[strings.ToLower(fmt.Sprintf("cliente %d", m.n))] = key
	return s
}

// Resolve devuelve la IP real de un seudónimo ("Cliente 3" o "Cliente 3 (10.20.x.x)").
func (m *Masker) Resolve(ref string) (string, bool) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if i := strings.Index(ref, " ("); i > 0 {
		ref = ref[:i]
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ip, ok := m.byAlias[ref]
	return ip, ok
}

// Count devuelve cuántos clientes se seudonimizaron.
func (m *Masker) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.n
}

// Coarse reduce una IP a su red gruesa: IPv4 /16 ("10.20.x.x"), IPv6 /32
// ("2001:db8:x:x::"). Lo que no es una IP se reduce a sus dos primeros grupos.
func Coarse(ip string) string {
	if a, err := netip.ParseAddr(ip); err == nil {
		a = a.Unmap()
		if a.Is4() {
			b := a.As4()
			return fmt.Sprintf("%d.%d.x.x", b[0], b[1])
		}
		b := a.As16()
		return fmt.Sprintf("%x:%x:x:x::", uint16(b[0])<<8|uint16(b[1]), uint16(b[2])<<8|uint16(b[3]))
	}
	sep := "."
	if strings.Contains(ip, ":") {
		sep = ":"
	}
	parts := strings.Split(ip, sep)
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return strings.Join(parts, sep) + sep + "x" + sep + "x"
}

// Scope dice a quién pertenece un valor.
type Scope int

const (
	// Mixed: documento general (hallazgo, tráfico); las claves de IP de cliente
	// y los objetos "customer" se seudonimizan.
	Mixed Scope = iota
	// Customer: todo el valor es de un cliente (ficha de cliente).
	Customer
	// Infra: routers, exportadores y túneles; sus direcciones son de la red del
	// ISP (túnel, exportador), no de clientes, y se conservan.
	Infra
)

// Value enmascara un valor JSON decodificado (map[string]any, []any, string,
// números…) según scope. Devuelve una copia; no modifica v.
func (m *Masker) Value(v any, scope Scope) any {
	out := m.walk(v, scope == Customer, scope == Infra)
	return m.replaceKnown(out)
}

func (m *Masker) walk(v any, customer, infra bool) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			lk := strings.ToLower(k)
			switch {
			case dropAlways[lk]:
				continue
			case customer && dropInCustomer[lk]:
				continue
			case customerIPKeys[lk] && (customer || !infra):
				out[k] = m.ipValue(val)
			case customerContextKeys[lk]:
				out[k] = m.walk(val, true, false)
			case externalKeys[lk] && (lk == "external" || lk == "external_identity" || customer):
				out[k] = m.walk(dropExternal(val), true, false)
			default:
				out[k] = m.walk(val, customer, infra)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = m.walk(val, customer, infra)
		}
		return out
	case string:
		if customer {
			return m.maskIPsInText(x)
		}
		return x
	default:
		return v
	}
}

// dropExternal quita de un bloque de identidad externa (o de una lista de
// ellos) las claves que identifican a la persona.
func dropExternal(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if !dropInExternal[strings.ToLower(k)] {
				out[k] = val
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = dropExternal(e)
		}
		return out
	}
	return v
}

// ipValue seudonimiza el valor de una clave de IP de cliente (texto o lista).
func (m *Masker) ipValue(v any) any {
	switch x := v.(type) {
	case string:
		return m.CustomerIP(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = m.ipValue(e)
		}
		return out
	case nil:
		return nil
	default:
		return "[enmascarado]"
	}
}

// maskIPsInText seudonimiza toda IP de un texto que pertenece a un cliente.
func (m *Masker) maskIPsInText(s string) string {
	return ipLike.ReplaceAllStringFunc(s, func(tok string) string {
		if _, err := netip.ParseAddr(tok); err == nil {
			return m.CustomerIP(tok)
		}
		if p, err := netip.ParsePrefix(tok); err == nil && p.IsSingleIP() {
			return m.CustomerIP(tok)
		}
		return tok
	})
}

// replaceKnown sustituye, en todos los textos, las IP ya seudonimizadas.
func (m *Masker) replaceKnown(v any) any {
	m.mu.Lock()
	known := slices.Clone(m.order)
	pseudo := make(map[string]string, len(known))
	for _, k := range known {
		pseudo[k] = m.byIP[k]
	}
	m.mu.Unlock()
	if len(known) == 0 {
		return v
	}
	var rep func(any) any
	rep = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				x[k] = rep(val)
			}
			return x
		case []any:
			for i, val := range x {
				x[i] = rep(val)
			}
			return x
		case string:
			if !ipLike.MatchString(x) {
				return x
			}
			return ipLike.ReplaceAllStringFunc(x, func(tok string) string {
				t := tok
				if p, err := netip.ParsePrefix(tok); err == nil && p.IsSingleIP() {
					t = p.Addr().String()
				} else if a, err := netip.ParseAddr(tok); err == nil {
					t = a.Unmap().String()
				}
				if s, ok := pseudo[t]; ok {
					return s
				}
				return tok
			})
		default:
			return v
		}
	}
	return rep(v)
}

// Text enmascara las IP de clientes ya conocidas en un texto libre.
func (m *Masker) Text(s string) string {
	out, _ := m.replaceKnown(s).(string)
	return out
}
