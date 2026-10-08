// Package capture escribe y lee datagramas de exportación en dos formatos:
//
//   - hfsim: formato binario propio del simulador, pensado para fixtures de CI
//     (los *.pcap están en .gitignore porque podrían contener datos reales).
//   - pcap clásico (libpcap 2.4): para abrir en Wireshark o reproducir con
//     herramientas estándar. Al leer admite enlaces Ethernet (con 802.1Q),
//     Linux cooked (SLL), IPv4/IPv6 en bruto, de modo que también sirve para
//     las capturas del laboratorio CHR (I0-12).
//
// Formato hfsim (todos los enteros big-endian):
//
//	cabecera: "HORUSFLOWSIM" (12 bytes) | versión u16 = 1 | flags u16 = 0
//	registro: tiempo u64 (ns Unix) | familia u8 (4|6)
//	          | IP origen (4|16) | puerto origen u16
//	          | IP destino (4|16) | puerto destino u16
//	          | longitud u32 | carga UDP
package capture

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
	"time"
)

// Datagram es un datagrama UDP de exportación.
type Datagram struct {
	Time    time.Time
	Src     netip.AddrPort
	Dst     netip.AddrPort
	Payload []byte
}

// Format identifica el formato de fichero.
type Format int

// Formatos de fichero.
const (
	FormatHFSim Format = iota + 1
	FormatPcap
)

// ParseFormat interpreta "hfsim"/"bin" o "pcap".
func ParseFormat(s string) (Format, error) {
	switch s {
	case "hfsim", "bin":
		return FormatHFSim, nil
	case "pcap":
		return FormatPcap, nil
	}
	return 0, fmt.Errorf("formato desconocido %q (usa hfsim o pcap)", s)
}

// FormatFromPath deduce el formato por la extensión (.pcap o .pcap.gz →
// pcap; resto → hfsim).
func FormatFromPath(p string) Format {
	if strings.HasSuffix(strings.TrimSuffix(p, ".gz"), ".pcap") {
		return FormatPcap
	}
	return FormatHFSim
}

// Create crea un fichero de captura (comprimido con gzip si la ruta acaba en
// .gz) y devuelve el escritor y la función que lo cierra.
func Create(path string, f Format) (Writer, func() error, error) {
	fh, err := os.Create(path) //nolint:gosec // ruta indicada por quien ejecuta la herramienta
	if err != nil {
		return nil, nil, fmt.Errorf("captura: %w", err)
	}
	var out io.Writer = fh
	var gz *gzip.Writer
	if strings.HasSuffix(path, ".gz") {
		gz, _ = gzip.NewWriterLevel(fh, gzip.BestCompression)
		out = gz
	}
	w, err := NewWriter(out, f)
	if err != nil {
		_ = fh.Close()
		return nil, nil, err
	}
	closeFn := func() error {
		if err := w.Flush(); err != nil {
			_ = fh.Close()
			return err
		}
		if gz != nil {
			if err := gz.Close(); err != nil {
				_ = fh.Close()
				return fmt.Errorf("captura: %w", err)
			}
		}
		if err := fh.Close(); err != nil {
			return fmt.Errorf("captura: %w", err)
		}
		return nil
	}
	return w, closeFn, nil
}

// ReadFile lee los datagramas de un fichero hfsim o pcap, comprimido o no.
func ReadFile(path string, fn func(Datagram) error) error {
	fh, err := os.Open(path) //nolint:gosec // ruta indicada por quien ejecuta la herramienta
	if err != nil {
		return fmt.Errorf("captura: %w", err)
	}
	defer func() { _ = fh.Close() }()
	return Read(fh, fn)
}

var hfsimMagic = []byte("HORUSFLOWSIM")

const (
	hfsimVersion = 1
	maxPayload   = 65535
)

// Writer escribe datagramas en un fichero.
type Writer interface {
	Write(d Datagram) error
	Flush() error
}

// NewWriter crea un escritor del formato dado y escribe la cabecera.
func NewWriter(w io.Writer, f Format) (Writer, error) {
	bw := bufio.NewWriterSize(w, 1<<16)
	switch f {
	case FormatPcap:
		pw := &pcapWriter{w: bw}
		if err := pw.header(); err != nil {
			return nil, err
		}
		return pw, nil
	default:
		hw := &hfsimWriter{w: bw}
		hdr := append([]byte{}, hfsimMagic...)
		hdr = binary.BigEndian.AppendUint16(hdr, hfsimVersion)
		hdr = binary.BigEndian.AppendUint16(hdr, 0)
		if _, err := bw.Write(hdr); err != nil {
			return nil, fmt.Errorf("hfsim: cabecera: %w", err)
		}
		return hw, nil
	}
}

type hfsimWriter struct {
	w *bufio.Writer
}

func appendAddrPort(b []byte, ap netip.AddrPort) []byte {
	a := ap.Addr().Unmap()
	if a.Is4() {
		x := a.As4()
		b = append(b, x[:]...)
	} else {
		x := a.As16()
		b = append(b, x[:]...)
	}
	return binary.BigEndian.AppendUint16(b, ap.Port())
}

func (h *hfsimWriter) Write(d Datagram) error {
	if len(d.Payload) > maxPayload {
		return fmt.Errorf("hfsim: datagrama de %d bytes demasiado grande", len(d.Payload))
	}
	src, dst := d.Src.Addr().Unmap(), d.Dst.Addr().Unmap()
	if src.Is4() != dst.Is4() {
		return errors.New("hfsim: origen y destino de familias distintas")
	}
	fam := byte(6)
	if src.Is4() {
		fam = 4
	}
	b := make([]byte, 0, 64)
	b = binary.BigEndian.AppendUint64(b, uint64(d.Time.UnixNano()))
	b = append(b, fam)
	b = appendAddrPort(b, netip.AddrPortFrom(src, d.Src.Port()))
	b = appendAddrPort(b, netip.AddrPortFrom(dst, d.Dst.Port()))
	b = binary.BigEndian.AppendUint32(b, uint32(len(d.Payload)))
	if _, err := h.w.Write(b); err != nil {
		return fmt.Errorf("hfsim: %w", err)
	}
	if _, err := h.w.Write(d.Payload); err != nil {
		return fmt.Errorf("hfsim: %w", err)
	}
	return nil
}

func (h *hfsimWriter) Flush() error {
	if err := h.w.Flush(); err != nil {
		return fmt.Errorf("hfsim: %w", err)
	}
	return nil
}

// ReadAll lee todos los datagramas de un fichero hfsim o pcap (detecta el
// formato por la cabecera).
func ReadAll(r io.Reader) ([]Datagram, error) {
	var out []Datagram
	err := Read(r, func(d Datagram) error {
		out = append(out, d)
		return nil
	})
	return out, err
}

// Read recorre los datagramas de un fichero hfsim o pcap y llama a fn por
// cada uno. Detecta el formato por la cabecera.
func Read(r io.Reader, fn func(Datagram) error) error {
	br := bufio.NewReaderSize(r, 1<<16)
	head, err := br.Peek(4)
	if err != nil {
		return fmt.Errorf("capture: cabecera: %w", err)
	}
	if head[0] == 0x1f && head[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return fmt.Errorf("capture: gzip: %w", err)
		}
		defer func() { _ = gz.Close() }()
		br = bufio.NewReaderSize(gz, 1<<16)
		if head, err = br.Peek(4); err != nil {
			return fmt.Errorf("capture: cabecera: %w", err)
		}
	}
	if bytes.HasPrefix(hfsimMagic, head) {
		return readHFSim(br, fn)
	}
	return readPcap(br, fn)
}

func readHFSim(r *bufio.Reader, fn func(Datagram) error) error {
	hdr := make([]byte, len(hfsimMagic)+4)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return fmt.Errorf("hfsim: cabecera: %w", err)
	}
	if !bytes.Equal(hdr[:len(hfsimMagic)], hfsimMagic) {
		return errors.New("hfsim: firma incorrecta")
	}
	if v := binary.BigEndian.Uint16(hdr[len(hfsimMagic):]); v != hfsimVersion {
		return fmt.Errorf("hfsim: versión %d no soportada", v)
	}
	for {
		var fixed [9]byte
		if _, err := io.ReadFull(r, fixed[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("hfsim: registro: %w", err)
		}
		ts := int64(binary.BigEndian.Uint64(fixed[:8]))
		alen := 4
		switch fixed[8] {
		case 4:
		case 6:
			alen = 16
		default:
			return fmt.Errorf("hfsim: familia %d inválida", fixed[8])
		}
		rest := make([]byte, 2*(alen+2)+4)
		if _, err := io.ReadFull(r, rest); err != nil {
			return fmt.Errorf("hfsim: registro: %w", err)
		}
		src := addrPortFrom(rest[:alen+2], alen)
		dst := addrPortFrom(rest[alen+2:2*(alen+2)], alen)
		n := binary.BigEndian.Uint32(rest[2*(alen+2):])
		if n > maxPayload {
			return fmt.Errorf("hfsim: longitud %d inválida", n)
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(r, payload); err != nil {
			return fmt.Errorf("hfsim: carga: %w", err)
		}
		if err := fn(Datagram{Time: time.Unix(0, ts).UTC(), Src: src, Dst: dst, Payload: payload}); err != nil {
			return err
		}
	}
}

func addrPortFrom(b []byte, alen int) netip.AddrPort {
	var a netip.Addr
	if alen == 4 {
		a = netip.AddrFrom4([4]byte(b[:4]))
	} else {
		a = netip.AddrFrom16([16]byte(b[:16]))
	}
	return netip.AddrPortFrom(a, binary.BigEndian.Uint16(b[alen:]))
}
