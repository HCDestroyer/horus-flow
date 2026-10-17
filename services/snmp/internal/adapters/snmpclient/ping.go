package snmpclient

import (
	"context"
	"math"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// Lectura (solo lectura, nunca SET) de los resultados de las pruebas ICMP
// que el operador configura en el router (NQA de Huawei VRP), para la
// pérdida, latencia y jitter por enlace (docs/vendors/huawei.md §4).
//
// Fuente preferida: DISMAN-PING-MIB (RFC 4560) pingResultsTable, indexada por
// pingCtlOwnerIndex (admin-name de NQA) y pingCtlTestName (test-name); según
// la documentación de Huawei guarda solo el resultado de la última prueba.
// Respaldo: NQA-MIB de Huawei (1.3.6.1.4.1.2011.5.25.111) nqaResultsTable,
// que se lee con GET en el índice de resultados 0 y salto 0 (restricción de
// acceso documentada en la propia MIB).
const (
	OIDPingCtlTable         = ".1.3.6.1.2.1.80.1.2.1"
	OIDPingResultsTable     = ".1.3.6.1.2.1.80.1.3.1"
	pingCtlColTargetAddr    = "4"
	pingResColOperStatus    = "1"
	pingResColTargetAddr    = "3"
	pingResColMinRtt        = "4"
	pingResColMaxRtt        = "5"
	pingResColAvgRtt        = "6"
	pingResColResponses     = "7"
	pingResColSent          = "8"
	pingResColSumOfSquares  = "9"
	OIDNqaAdminCtrlTable    = ".1.3.6.1.4.1.2011.5.25.111.2.1.1"
	nqaCtrlColType          = "4"
	OIDNqaResultsTable      = ".1.3.6.1.4.1.2011.5.25.111.4.1.1"
	nqaResColCompletions    = "3"
	nqaResColRttMin         = "11"
	nqaResColRttMax         = "12"
	nqaResColAddress        = "20"
	nqaResColResponses      = "21"
	nqaResColSent           = "22"
	nqaResColRttAvg         = "26"
	nqaResColLostRatio      = "27"
	nqaTypeICMP             = 1 // NqaType icmp(1)
	maxNQATests             = 64
	snmpAdminStringMaxBytes = 32
)

// Fuentes de un resultado de prueba.
const (
	PingSourceDisman = "disman-ping-mib"
	PingSourceNQA    = "nqa-mib"
)

// PingResult es el resultado de la última ejecución de una prueba ICMP.
// nil = el router no lo informa.
type PingResult struct {
	Owner     string
	Test      string
	Source    string
	Target    string // IP de destino ("" si no se conoce)
	Sent      *int
	Responses *int
	LossPct   *float64
	MinRTTMs  *float64
	MaxRTTMs  *float64
	AvgRTTMs  *float64
	// StdDevMs: desviación típica del RTT (de pingResultsRttSumOfSquares);
	// es la estimación de jitter.
	StdDevMs *float64
}

// encodeAdminString codifica un SnmpAdminString como componente de índice
// no IMPLIED (RFC 2578 §7.7): longitud seguida de un subidentificador por
// octeto.
func encodeAdminString(s string) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(len(s)))
	for i := 0; i < len(s); i++ {
		b.WriteByte('.')
		b.WriteString(strconv.Itoa(int(s[i])))
	}
	return b.String()
}

// decodeAdminStrings lee n cadenas con longitud prefijada de un sufijo de
// índice y devuelve el resto.
func decodeAdminStrings(suffix string, n int) ([]string, []string, bool) {
	parts := strings.Split(suffix, ".")
	var out []string
	for range n {
		if len(parts) == 0 {
			return nil, nil, false
		}
		l, err := strconv.Atoi(parts[0])
		if err != nil || l < 0 || l > snmpAdminStringMaxBytes || len(parts) < 1+l {
			return nil, nil, false
		}
		b := make([]byte, 0, l)
		for _, p := range parts[1 : 1+l] {
			c, err := strconv.Atoi(p)
			if err != nil || c < 0 || c > 255 {
				return nil, nil, false
			}
			b = append(b, byte(c))
		}
		out = append(out, string(b))
		parts = parts[1+l:]
	}
	return out, parts, true
}

// inetAddress interpreta un InetAddress (octetos en bruto IPv4/IPv6, o texto).
func inetAddress(v any) string {
	s, ok := v.(string)
	if !ok || s == "" {
		return ""
	}
	switch len(s) {
	case 4:
		return netip.AddrFrom4([4]byte([]byte(s))).String()
	case 16:
		return netip.AddrFrom16([16]byte([]byte(s))).Unmap().String()
	}
	if a, err := netip.ParseAddr(strings.TrimSpace(s)); err == nil {
		return a.String()
	}
	return ""
}

func intPtr(v any) *int {
	n, ok := asInt(v)
	if !ok || n < 0 || n > math.MaxInt32 {
		return nil
	}
	i := int(n)
	return &i
}

func msPtr(v any) *float64 {
	n, ok := asInt(v)
	if !ok || n < 0 {
		return nil
	}
	return f64(float64(n))
}

// ReadPingResults lee las pruebas ICMP del propietario owner (admin-name de
// NQA): DISMAN-PING-MIB y, si no publica ninguna, NQA-MIB. Una MIB ausente
// no es un error (lista vacía); solo un error de transporte falla.
func ReadPingResults(ctx context.Context, t Transport, owner string) ([]PingResult, error) {
	res, err := readDisman(ctx, t, owner)
	if err != nil || len(res) > 0 {
		return res, err
	}
	return readNQA(ctx, t, owner)
}

func readDisman(ctx context.Context, t Transport, owner string) ([]PingResult, error) {
	ownerIdx := encodeAdminString(owner)
	byTest := map[string]*PingResult{}
	var sumSq = map[string]float64{}
	for _, col := range []string{pingResColSent, pingResColResponses, pingResColOperStatus, pingResColTargetAddr, pingResColMinRtt,
		pingResColMaxRtt, pingResColAvgRtt, pingResColSumOfSquares} {
		root := OIDPingResultsTable + "." + col + "." + ownerIdx
		rows, err := t.Walk(ctx, root)
		if err != nil {
			return nil, err
		}
		for _, v := range rows {
			suffix, ok := strings.CutPrefix(normOID(v.OID), root+".")
			if !ok {
				continue
			}
			names, rest, ok := decodeAdminStrings(suffix, 1)
			if !ok || len(rest) != 0 {
				continue
			}
			test := names[0]
			r := byTest[test]
			if r == nil {
				if col != pingResColSent || len(byTest) >= maxNQATests {
					continue // solo las pruebas con pingResultsSentProbes
				}
				r = &PingResult{Owner: owner, Test: test, Source: PingSourceDisman}
				byTest[test] = r
			}
			switch col {
			case pingResColSent:
				r.Sent = intPtr(v.Value)
			case pingResColResponses:
				r.Responses = intPtr(v.Value)
			case pingResColTargetAddr:
				r.Target = inetAddress(v.Value)
			case pingResColMinRtt:
				r.MinRTTMs = msPtr(v.Value)
			case pingResColMaxRtt:
				r.MaxRTTMs = msPtr(v.Value)
			case pingResColAvgRtt:
				r.AvgRTTMs = msPtr(v.Value)
			case pingResColSumOfSquares:
				if n, ok := asUint(v.Value); ok {
					sumSq[test] = float64(n)
				}
			}
		}
	}
	if len(byTest) == 0 {
		return nil, nil
	}
	// Destino aún sin resultados: el de la definición (pingCtlTargetAddress).
	root := OIDPingCtlTable + "." + pingCtlColTargetAddr + "." + ownerIdx
	rows, err := t.Walk(ctx, root)
	if err != nil {
		return nil, err
	}
	for _, v := range rows {
		suffix, ok := strings.CutPrefix(normOID(v.OID), root+".")
		if !ok {
			continue
		}
		if names, rest, ok := decodeAdminStrings(suffix, 1); ok && len(rest) == 0 {
			if r := byTest[names[0]]; r != nil && r.Target == "" {
				r.Target = inetAddress(v.Value)
			}
		}
	}
	out := make([]PingResult, 0, len(byTest))
	for test, r := range byTest {
		finishPing(r)
		if r.Responses != nil && *r.Responses > 0 && r.AvgRTTMs != nil {
			if sq, ok := sumSq[test]; ok {
				n := float64(*r.Responses)
				if v := sq/n - *r.AvgRTTMs**r.AvgRTTMs; v >= 0 {
					r.StdDevMs = f64(math.Sqrt(v))
				} else {
					r.StdDevMs = f64(0) // RTT medio redondeado a ms
				}
			}
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Test < out[j].Test })
	return out, nil
}

// finishPing deriva la pérdida de enviados/respuestas.
func finishPing(r *PingResult) {
	if r.LossPct == nil && r.Sent != nil && *r.Sent > 0 && r.Responses != nil && *r.Responses <= *r.Sent {
		r.LossPct = f64(100 * float64(*r.Sent-*r.Responses) / float64(*r.Sent))
	}
}

func readNQA(ctx context.Context, t Transport, owner string) ([]PingResult, error) {
	ownerIdx := encodeAdminString(owner)
	root := OIDNqaAdminCtrlTable + "." + nqaCtrlColType + "." + ownerIdx
	rows, err := t.Walk(ctx, root)
	if err != nil {
		return nil, err
	}
	var tests []string
	for _, v := range rows {
		suffix, ok := strings.CutPrefix(normOID(v.OID), root+".")
		if !ok {
			continue
		}
		names, rest, ok := decodeAdminStrings(suffix, 1)
		if n, isInt := asInt(v.Value); !ok || len(rest) != 0 || !isInt || n != nqaTypeICMP {
			continue
		}
		if len(tests) < maxNQATests {
			tests = append(tests, names[0])
		}
	}
	sort.Strings(tests)
	cols := []string{nqaResColSent, nqaResColResponses, nqaResColRttMin, nqaResColRttMax, nqaResColRttAvg, nqaResColLostRatio,
		nqaResColAddress, nqaResColCompletions}
	out := make([]PingResult, 0, len(tests))
	for _, test := range tests {
		suffix := "." + ownerIdx + "." + encodeAdminString(test) + ".0.0"
		oids := make([]string, 0, len(cols))
		for _, c := range cols {
			oids = append(oids, OIDNqaResultsTable+"."+c+suffix)
		}
		vars, err := t.Get(ctx, oids)
		if err != nil {
			return nil, err
		}
		m := index(vars)
		at := func(c string) any { return m[OIDNqaResultsTable+"."+c+suffix] }
		r := PingResult{Owner: owner, Test: test, Source: PingSourceNQA, Sent: intPtr(at(nqaResColSent)),
			Responses: intPtr(at(nqaResColResponses)), MinRTTMs: msPtr(at(nqaResColRttMin)), MaxRTTMs: msPtr(at(nqaResColRttMax)),
			AvgRTTMs: msPtr(at(nqaResColRttAvg)), Target: inetAddress(at(nqaResColAddress))}
		if n, ok := asInt(at(nqaResColLostRatio)); ok && n >= 0 && n <= 100 {
			r.LossPct = f64(float64(n))
		}
		if r.Sent == nil && r.Responses == nil && r.LossPct == nil {
			continue // prueba sin resultados aún
		}
		finishPing(&r)
		out = append(out, r)
	}
	return out, nil
}
