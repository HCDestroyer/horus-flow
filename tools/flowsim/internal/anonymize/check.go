package anonymize

import (
	"bytes"
	"net/netip"
	"slices"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/capture"
)

// CheckReport compara una captura original con su versión anonimizada.
type CheckReport struct {
	OriginalIPs, OriginalMACs     int // identificativas (sin las conservadas)
	AnonymizedIPs, AnonymizedMACs int
	// LeakedIPs/LeakedMACs: direcciones identificativas del original que
	// aparecen como dirección (cabecera o campo) en la anonimizada.
	LeakedIPs  []netip.Addr
	LeakedMACs [][6]byte
	// RawMACHits: MAC originales encontradas como secuencia de 6 bytes en
	// cualquier posición de las tramas anonimizadas.
	RawMACHits int
	// RawIPv6Hits: ídem con IPv6 (16 bytes).
	RawIPv6Hits int
	// RawIPv4Hits: IPv4 originales como secuencia de 4 bytes en cualquier
	// posición (incluye coincidencias casuales en contadores o tiempos).
	RawIPv4Hits int
	// Kept son las direcciones conservadas por no ser identificativas.
	Kept []netip.Addr
}

// OK indica que no se filtra ninguna IP ni MAC original.
func (r *CheckReport) OK() bool {
	return len(r.LeakedIPs) == 0 && len(r.LeakedMACs) == 0 && r.RawMACHits == 0 && r.RawIPv6Hits == 0
}

func identifying(a netip.Addr) bool {
	if a.Is4() {
		return !keepV4(a)
	}
	return !a.IsUnspecified() && !a.IsLoopback() && !a.IsMulticast()
}

// Check inventaría ambas capturas y busca direcciones y MAC originales en la
// anonimizada, como dirección y como secuencia de bytes.
func Check(orig, anon []capture.Frame) (*CheckReport, error) {
	io, err := Collect(orig)
	if err != nil {
		return nil, err
	}
	ia, err := Collect(anon)
	if err != nil {
		return nil, err
	}
	r := &CheckReport{}
	for a := range ia.IPs {
		if identifying(a) {
			r.AnonymizedIPs++
		}
	}
	for x := range ia.MACs {
		if !keepMAC(x) {
			r.AnonymizedMACs++
		}
	}
	var all [][]byte
	for _, f := range anon {
		all = append(all, f.Data)
	}
	blob := bytes.Join(all, []byte{0xa5, 0x5a, 0xa5, 0x5a})
	for a := range io.IPs {
		if !identifying(a) {
			r.Kept = append(r.Kept, a)
			continue
		}
		r.OriginalIPs++
		if ia.IPs[a] {
			r.LeakedIPs = append(r.LeakedIPs, a)
		}
		r.addRaw(blob, a)
	}
	for x := range io.MACs {
		if keepMAC(x) {
			continue
		}
		r.OriginalMACs++
		if ia.MACs[x] {
			r.LeakedMACs = append(r.LeakedMACs, x)
		}
		r.RawMACHits += bytes.Count(blob, x[:])
	}
	slices.SortFunc(r.LeakedIPs, netip.Addr.Compare)
	slices.SortFunc(r.Kept, netip.Addr.Compare)
	return r, nil
}

func (r *CheckReport) addRaw(blob []byte, a netip.Addr) {
	n := bytes.Count(blob, a.AsSlice())
	if a.Is4() {
		r.RawIPv4Hits += n
	} else {
		r.RawIPv6Hits += n
	}
}
