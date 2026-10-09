package capture

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

// Frame es una trama capturada en bruto (pcap o pcapng).
type Frame struct {
	Time     time.Time
	LinkType uint32
	Data     []byte
}

// Tipos de bloque pcapng (draft-ietf-opsawg-pcapng).
const (
	pcapngSHB      = 0x0A0D0D0A
	pcapngIDB      = 0x00000001
	pcapngPB       = 0x00000002 // Packet Block (obsoleto)
	pcapngSPB      = 0x00000003
	pcapngEPB      = 0x00000006
	pcapngBOM      = 0x1A2B3C4D
	pcapngMaxBlock = 1 << 24

	optEnd      = 0
	optIfName   = 2
	optIfTSResl = 9
)

type pcapngIface struct {
	link    uint32
	snaplen uint32
	// unidades por segundo de las marcas de tiempo (if_tsresol, 10^6 por defecto).
	units uint64
}

// readPcapng recorre las tramas de un fichero pcapng (varias secciones e
// interfaces, ambos órdenes de bytes, EPB/SPB/PB).
func readPcapng(r *bufio.Reader, fn func(Frame) error) error {
	var bo binary.ByteOrder = binary.LittleEndian
	var ifaces []pcapngIface
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("pcapng: bloque: %w", err)
		}
		typ := binary.LittleEndian.Uint32(hdr[:4])
		if typ == pcapngSHB {
			// El orden de bytes lo fija el BOM del propio SHB.
			var bom [4]byte
			if _, err := io.ReadFull(r, bom[:]); err != nil {
				return fmt.Errorf("pcapng: SHB: %w", err)
			}
			switch binary.LittleEndian.Uint32(bom[:]) {
			case pcapngBOM:
				bo = binary.LittleEndian
			case 0x4D3C2B1A:
				bo = binary.BigEndian
			default:
				return errors.New("pcapng: SHB con BOM inválido")
			}
			total := bo.Uint32(hdr[4:])
			if total < 28 || total%4 != 0 || total > pcapngMaxBlock {
				return fmt.Errorf("pcapng: SHB de longitud %d inválida", total)
			}
			if _, err := io.CopyN(io.Discard, r, int64(total)-12); err != nil {
				return fmt.Errorf("pcapng: SHB: %w", err)
			}
			ifaces = ifaces[:0]
			continue
		}
		typ = bo.Uint32(hdr[:4])
		total := bo.Uint32(hdr[4:])
		if total < 12 || total%4 != 0 || total > pcapngMaxBlock {
			return fmt.Errorf("pcapng: bloque %#x de longitud %d inválida", typ, total)
		}
		body := make([]byte, total-12)
		if _, err := io.ReadFull(r, body); err != nil {
			return fmt.Errorf("pcapng: bloque: %w", err)
		}
		var trailer [4]byte
		if _, err := io.ReadFull(r, trailer[:]); err != nil {
			return fmt.Errorf("pcapng: bloque: %w", err)
		}
		if bo.Uint32(trailer[:]) != total {
			return fmt.Errorf("pcapng: longitudes de bloque %#x no coinciden", typ)
		}
		switch typ {
		case pcapngIDB:
			if len(body) < 8 {
				return errors.New("pcapng: IDB truncado")
			}
			ifc := pcapngIface{link: uint32(bo.Uint16(body[0:])), snaplen: bo.Uint32(body[4:]), units: 1_000_000}
			walkOptions(bo, body[8:], func(code uint16, v []byte) {
				if code == optIfTSResl && len(v) >= 1 {
					ifc.units = tsUnits(v[0])
				}
			})
			ifaces = append(ifaces, ifc)
		case pcapngEPB, pcapngPB:
			if len(body) < 20 {
				return errors.New("pcapng: EPB truncado")
			}
			var id uint32
			if typ == pcapngEPB {
				id = bo.Uint32(body[0:])
			} else {
				id = uint32(bo.Uint16(body[0:]))
			}
			if int(id) >= len(ifaces) {
				return fmt.Errorf("pcapng: interfaz %d no declarada", id)
			}
			ts := uint64(bo.Uint32(body[4:]))<<32 | uint64(bo.Uint32(body[8:]))
			capLen := bo.Uint32(body[12:])
			if int(capLen) > len(body)-20 {
				return errors.New("pcapng: EPB con longitud capturada inválida")
			}
			ifc := ifaces[id]
			f := Frame{Time: tsTime(ts, ifc.units), LinkType: ifc.link, Data: body[20 : 20+capLen]}
			if err := fn(f); err != nil {
				return err
			}
		case pcapngSPB:
			if len(ifaces) == 0 || len(body) < 4 {
				return errors.New("pcapng: SPB sin interfaz")
			}
			orig := bo.Uint32(body[0:])
			n := min(int(orig), len(body)-4)
			if s := int(ifaces[0].snaplen); s > 0 && n > s {
				n = s
			}
			if err := fn(Frame{LinkType: ifaces[0].link, Data: body[4 : 4+n]}); err != nil {
				return err
			}
		}
	}
}

func walkOptions(bo binary.ByteOrder, b []byte, fn func(code uint16, v []byte)) {
	for len(b) >= 4 {
		code, l := bo.Uint16(b), int(bo.Uint16(b[2:]))
		if code == optEnd || 4+l > len(b) {
			return
		}
		fn(code, b[4:4+l])
		b = b[4+(l+3)&^3:]
	}
}

func tsUnits(v byte) uint64 {
	exp := uint64(v & 0x7f)
	if v&0x80 != 0 {
		if exp > 62 {
			exp = 62
		}
		return 1 << exp
	}
	u := uint64(1)
	for i := uint64(0); i < exp && u <= math.MaxUint64/10; i++ {
		u *= 10
	}
	return u
}

func tsTime(ts, units uint64) time.Time {
	sec := ts / units
	frac := ts % units
	ns := frac * 1_000_000_000 / units
	return time.Unix(int64(sec), int64(ns)).UTC() //nolint:gosec // marcas de tiempo de captura
}

// PcapngWriter escribe tramas en un fichero pcapng con una sola sección e
// interfaz, sin opciones identificativas (ni sistema operativo, ni hardware,
// ni aplicación): solo el nombre de interfaz indicado y resolución de
// nanosegundos.
type PcapngWriter struct {
	w *bufio.Writer
}

// NewPcapngWriter escribe la cabecera de sección y la descripción de la
// interfaz.
func NewPcapngWriter(w io.Writer, linkType uint32, ifName string) (*PcapngWriter, error) {
	bw := bufio.NewWriterSize(w, 1<<16)
	le := binary.LittleEndian
	shb := le.AppendUint32(nil, pcapngBOM)
	shb = le.AppendUint16(shb, 1)
	shb = le.AppendUint16(shb, 0)
	shb = le.AppendUint64(shb, math.MaxUint64) // longitud de sección desconocida
	if err := writeBlock(bw, pcapngSHB, shb); err != nil {
		return nil, err
	}
	idb := le.AppendUint16(nil, uint16(linkType)) //nolint:gosec // linktypes < 2^16
	idb = le.AppendUint16(idb, 0)
	idb = le.AppendUint32(idb, 0) // sin límite de captura
	if ifName != "" {
		idb = appendOption(idb, optIfName, []byte(ifName))
	}
	idb = appendOption(idb, optIfTSResl, []byte{9})
	idb = le.AppendUint32(idb, 0) // opt_endofopt
	if err := writeBlock(bw, pcapngIDB, idb); err != nil {
		return nil, err
	}
	return &PcapngWriter{w: bw}, nil
}

func appendOption(b []byte, code uint16, v []byte) []byte {
	b = binary.LittleEndian.AppendUint16(b, code)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(v))) //nolint:gosec // opciones cortas
	b = append(b, v...)
	for i := len(v); i%4 != 0; i++ {
		b = append(b, 0)
	}
	return b
}

func writeBlock(w *bufio.Writer, typ uint32, body []byte) error {
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	total := uint32(len(body) + 12) //nolint:gosec // bloques < 16 MiB
	b := binary.LittleEndian.AppendUint32(nil, typ)
	b = binary.LittleEndian.AppendUint32(b, total)
	b = append(b, body...)
	b = binary.LittleEndian.AppendUint32(b, total)
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("pcapng: %w", err)
	}
	return nil
}

// WriteFrame añade una trama (Enhanced Packet Block).
func (p *PcapngWriter) WriteFrame(t time.Time, data []byte) error {
	ns := uint64(t.UnixNano()) //nolint:gosec // fechas posteriores a 1970
	le := binary.LittleEndian
	b := le.AppendUint32(make([]byte, 0, 20+len(data)+3), 0)
	b = le.AppendUint32(b, uint32(ns>>32))
	b = le.AppendUint32(b, uint32(ns))        //nolint:gosec // parte baja
	b = le.AppendUint32(b, uint32(len(data))) //nolint:gosec // tramas < 4 GiB
	b = le.AppendUint32(b, uint32(len(data))) //nolint:gosec // tramas < 4 GiB
	b = append(b, data...)
	return writeBlock(p.w, pcapngEPB, b)
}

// Write implementa Writer: el datagrama va como IP en bruto (LINKTYPE_RAW).
func (p *PcapngWriter) Write(d Datagram) error {
	pkt, err := buildIPUDP(d)
	if err != nil {
		return err
	}
	return p.WriteFrame(d.Time, pkt)
}

// Flush vacía el búfer.
func (p *PcapngWriter) Flush() error {
	if err := p.w.Flush(); err != nil {
		return fmt.Errorf("pcapng: %w", err)
	}
	return nil
}
