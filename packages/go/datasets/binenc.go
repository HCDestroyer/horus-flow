package datasets

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"time"
)

// Enc es un codificador binario mínimo (varints, cadenas, prefijos y fechas)
// para los payloads de snapshot. Determinista: misma entrada, mismos bytes.
type Enc struct{ b []byte }

// Bytes devuelve el contenido codificado.
func (e *Enc) Bytes() []byte { return e.b }

// Uvarint añade un entero sin signo.
func (e *Enc) Uvarint(v uint64) { e.b = binary.AppendUvarint(e.b, v) }

// String añade una cadena con su longitud.
func (e *Enc) String(s string) {
	e.Uvarint(uint64(len(s)))
	e.b = append(e.b, s...)
}

// Time añade una fecha con resolución de segundos (cero = sin fecha).
func (e *Enc) Time(t time.Time) {
	if t.IsZero() {
		e.Uvarint(0)
		return
	}
	e.Uvarint(uint64(t.Unix()) + 1) //nolint:gosec // fechas posteriores a 1970
}

// Prefix añade un prefijo: familia (4|6), longitud y solo los bytes
// significativos de la dirección.
func (e *Enc) Prefix(p netip.Prefix) {
	a := p.Addr()
	var raw []byte
	if a.Is4() {
		b := a.As4()
		raw = b[:]
		e.b = append(e.b, 4)
	} else {
		b := a.As16()
		raw = b[:]
		e.b = append(e.b, 6)
	}
	e.b = append(e.b, byte(p.Bits()))
	e.b = append(e.b, raw[:(p.Bits()+7)/8]...)
}

// Dec decodifica lo escrito por Enc. El primer error queda registrado y las
// lecturas posteriores devuelven valores cero.
type Dec struct {
	b   []byte
	err error
}

// NewDec crea un decodificador sobre b.
func NewDec(b []byte) *Dec { return &Dec{b: b} }

// Err devuelve el primer error de decodificación.
func (d *Dec) Err() error { return d.err }

// Done indica si se consumió todo el buffer sin errores.
func (d *Dec) Done() bool { return d.err == nil && len(d.b) == 0 }

func (d *Dec) fail(what string) {
	if d.err == nil {
		d.err = fmt.Errorf("%w: payload truncado o inválido (%s)", ErrCorruptSnapshot, what)
	}
}

// Uvarint lee un entero sin signo.
func (d *Dec) Uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.b)
	if n <= 0 {
		d.fail("uvarint")
		return 0
	}
	d.b = d.b[n:]
	return v
}

// String lee una cadena.
func (d *Dec) String() string {
	n := d.Uvarint()
	if d.err != nil {
		return ""
	}
	if n > uint64(len(d.b)) {
		d.fail("string")
		return ""
	}
	s := string(d.b[:n])
	d.b = d.b[n:]
	return s
}

// Time lee una fecha.
func (d *Dec) Time() time.Time {
	v := d.Uvarint()
	if v == 0 || d.err != nil {
		return time.Time{}
	}
	return time.Unix(int64(v-1), 0).UTC() //nolint:gosec // rango validado por el codificador
}

// Prefix lee un prefijo.
func (d *Dec) Prefix() netip.Prefix {
	if d.err != nil {
		return netip.Prefix{}
	}
	if len(d.b) < 2 {
		d.fail("prefix")
		return netip.Prefix{}
	}
	fam, bits := d.b[0], int(d.b[1])
	width := 32
	if fam == 6 {
		width = 128
	} else if fam != 4 {
		d.fail("familia")
		return netip.Prefix{}
	}
	n := (bits + 7) / 8
	if bits > width || len(d.b) < 2+n {
		d.fail("prefix")
		return netip.Prefix{}
	}
	var raw [16]byte
	copy(raw[:], d.b[2:2+n])
	d.b = d.b[2+n:]
	var a netip.Addr
	if fam == 4 {
		a = netip.AddrFrom4([4]byte(raw[:4]))
	} else {
		a = netip.AddrFrom16(raw)
	}
	return netip.PrefixFrom(a, bits).Masked()
}

// ErrTrailing indica bytes sobrantes al final del payload.
var ErrTrailing = errors.New("datasets: bytes sobrantes en el payload")
