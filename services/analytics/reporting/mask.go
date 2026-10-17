package reporting

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	secapi "github.com/hcdestroyer/horus-flow/services/detection/api/securityreport"
)

// Variante enmascarada del reporte (docs/security.md §datos personales en
// reportes): quien descarga sin customers.read (alcance de todo el ISP)
// recibe un archivo sin datos personales de clientes. El worker genera las
// dos variantes de cada formato en la misma ejecución (programada o a
// petición), así la descarga solo elige archivo y nunca re-renderiza.
//
//   - IPv4 → los tres primeros octetos y "x" (100.64.1.x);
//   - IPv6 → el /48 y "…" (2001:db8:1…);
//   - alias → "Cliente N", estable dentro de un archivo (mismo cliente,
//     misma etiqueta en todas las secciones) y sin relación entre archivos;
//   - el texto libre de los hallazgos (resumen) pasa por el mismo filtro:
//     cualquier IP se enmascara y cualquier alias conocido se sustituye.

// MaskedNote es la nota que llevan el PDF y el CSV enmascarados.
const MaskedNote = "Datos personales enmascarados por permisos: IPs truncadas y alias sustituidos por etiquetas neutras."

// MaskAddress enmascara una IP o un prefijo (`100.64.1.7`, `2001:db8::/56`).
func MaskAddress(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	host := s
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i] // zona de enlace local
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return "IP enmascarada"
	}
	return maskAddr(a)
}

func maskAddr(a netip.Addr) string {
	a = a.Unmap()
	if a.Is4() {
		b := a.As4()
		return fmt.Sprintf("%d.%d.%d.x", b[0], b[1], b[2])
	}
	b := a.As16()
	return fmt.Sprintf("%x:%x:%x…", uint16(b[0])<<8|uint16(b[1]), uint16(b[2])<<8|uint16(b[3]), uint16(b[4])<<8|uint16(b[5]))
}

var (
	// Candidatos a IPv6 en texto libre (al menos dos ':'); se validan con netip.
	reIPv6 = regexp.MustCompile(`[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,7}(?:\.\d{1,3}){0,3}(?:%[0-9A-Za-z]+)?`)
	reIPv4 = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	reTime = regexp.MustCompile(`^\d{1,2}:\d{2}(?::\d{2})?$`)
)

// MaskText enmascara las IPs de un texto libre.
func MaskText(s string) string {
	s = reIPv6.ReplaceAllStringFunc(s, func(tok string) string {
		if reTime.MatchString(tok) || strings.Count(tok, ":") < 2 {
			return tok
		}
		if a, err := netip.ParseAddr(strings.SplitN(tok, "%", 2)[0]); err == nil {
			return maskAddr(a)
		}
		// "2001:db8::1:443" (IP y puerto) ya lo resuelve ParseAddr; lo que no
		// se pueda interpretar se trata como IP por prudencia.
		for t := tok; strings.Count(t, ":") >= 2; t = t[:strings.LastIndexByte(t, ':')] {
			if a, err := netip.ParseAddr(t); err == nil {
				return maskAddr(a)
			}
		}
		return "IPv6…"
	})
	return reIPv4.ReplaceAllStringFunc(s, func(tok string) string {
		parts := strings.Split(tok, ".")
		return parts[0] + "." + parts[1] + "." + parts[2] + ".x"
	})
}

// pseudonyms asigna "Cliente N" por cliente en el orden de aparición.
type pseudonyms struct {
	byKey   map[string]string
	aliases map[string]string // alias original → etiqueta (para el texto libre)
}

func (p *pseudonyms) label(addr string, alias *string) string {
	key := strings.TrimSpace(addr)
	a := strings.TrimSpace(ptrOr(alias, ""))
	if key == "" {
		key = "alias:" + a
	}
	l, ok := p.byKey[key]
	if !ok {
		l = "Cliente " + strconv.Itoa(len(p.byKey)+1)
		p.byKey[key] = l
	}
	if a != "" {
		if _, ok := p.aliases[a]; !ok {
			p.aliases[a] = l
		}
	}
	return l
}

// text sustituye los alias conocidos (en una sola pasada, el más largo
// primero) y enmascara las IPs. Los alias de menos de 3 caracteres no se
// buscan en el texto libre (destrozarían el resumen; los resúmenes de los
// motores no incluyen alias, es solo una red de seguridad).
func (p *pseudonyms) text(s string) string {
	keys := make([]string, 0, len(p.aliases))
	for k := range p.aliases {
		if utf8.RuneCountInString(k) >= 3 {
			keys = append(keys, k)
		}
	}
	if len(keys) > 0 {
		sort.Slice(keys, func(i, j int) bool {
			if len(keys[i]) != len(keys[j]) {
				return len(keys[i]) > len(keys[j])
			}
			return keys[i] < keys[j]
		})
		quoted := make([]string, len(keys))
		for i, k := range keys {
			quoted[i] = regexp.QuoteMeta(k)
		}
		re := regexp.MustCompile(strings.Join(quoted, "|"))
		s = re.ReplaceAllStringFunc(s, func(m string) string { return p.aliases[m] })
	}
	return MaskText(s)
}

// MaskData devuelve una copia de d sin datos personales de clientes (d no se modifica).
func MaskData(d *Data) *Data {
	out := *d
	out.Masked = true
	if d.Security == nil {
		return &out
	}
	sec := *d.Security
	ps := &pseudonyms{byKey: map[string]string{}, aliases: map[string]string{}}
	// Primero se asignan todas las etiquetas para que el texto libre de
	// cualquier fila pueda sustituir cualquier alias.
	sec.TopCustomers = make([]secapi.CustomerRow, len(d.Security.TopCustomers))
	for i, c := range d.Security.TopCustomers {
		l := ps.label(c.Address, c.Alias)
		c.Alias, c.Address = &l, MaskAddress(c.Address)
		sec.TopCustomers[i] = c
	}
	sec.OpenCriticalHigh = make([]secapi.FindingRow, len(d.Security.OpenCriticalHigh))
	for i, f := range d.Security.OpenCriticalHigh {
		l := ps.label(f.Address, f.Alias)
		f.Alias, f.Address = &l, MaskAddress(f.Address)
		sec.OpenCriticalHigh[i] = f
	}
	for i := range sec.OpenCriticalHigh {
		sec.OpenCriticalHigh[i].Summary = ps.text(sec.OpenCriticalHigh[i].Summary)
	}
	out.Security = &sec
	return &out
}
