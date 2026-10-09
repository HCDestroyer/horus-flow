package capture

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"net/netip"
	"path/filepath"
	"testing"
	"time"
)

func sample() []Datagram {
	t := time.Date(2026, 1, 5, 18, 0, 1, 123456789, time.UTC)
	return []Datagram{
		{Time: t, Src: netip.MustParseAddrPort("10.255.0.2:49152"), Dst: netip.MustParseAddrPort("10.255.0.1:4739"), Payload: []byte{0, 10, 0, 3, 9}},
		{Time: t.Add(time.Second), Src: netip.MustParseAddrPort("[2001:db8::2]:50000"), Dst: netip.MustParseAddrPort("[2001:db8::1]:2055"), Payload: bytes.Repeat([]byte{7}, 1300)},
	}
}

func TestRoundTrip(t *testing.T) {
	for _, f := range []Format{FormatHFSim, FormatPcap, FormatPcapng} {
		var buf bytes.Buffer
		w, err := NewWriter(&buf, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range sample() {
			if err := w.Write(d); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		got, err := ReadAll(&buf)
		if err != nil {
			t.Fatal(err)
		}
		want := sample()
		if len(got) != len(want) {
			t.Fatalf("formato %d: %d datagramas", f, len(got))
		}
		for i := range want {
			g, w := got[i], want[i]
			if !g.Time.Equal(w.Time) || g.Src != w.Src || g.Dst != w.Dst || !bytes.Equal(g.Payload, w.Payload) {
				t.Errorf("formato %d, datagrama %d: got %+v", f, i, g)
			}
		}
	}
}

func TestPcapChecksums(t *testing.T) {
	d := sample()[0]
	pkt, err := buildIPUDP(d)
	if err != nil {
		t.Fatal(err)
	}
	if cs := checksumFold(checksumAdd(0, pkt[:20])); cs != 0 {
		t.Errorf("checksum IPv4 inválido: %#x", cs)
	}
	// Checksum UDP con pseudo-cabecera: debe sumar 0xffff.
	s, dst := d.Src.Addr().As4(), d.Dst.Addr().As4()
	sum := checksumAdd(0, s[:])
	sum = checksumAdd(sum, dst[:])
	sum += 17 + uint32(len(pkt)-20)
	if cs := checksumFold(checksumAdd(sum, pkt[20:])); cs != 0 {
		t.Errorf("checksum UDP inválido: %#x", cs)
	}
}

func TestEthernetVLANFrame(t *testing.T) {
	pkt, err := buildIPUDP(sample()[0])
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 12)
	frame = binary.BigEndian.AppendUint16(frame, 0x8100)
	frame = append(frame, 0, 10)
	frame = binary.BigEndian.AppendUint16(frame, 0x0800)
	frame = append(frame, pkt...)
	d, ok := parseFrame(linkEthernet, frame)
	if !ok || d.Src != sample()[0].Src || !bytes.Equal(d.Payload, sample()[0].Payload) {
		t.Fatalf("trama Ethernet+802.1Q mal interpretada: %+v %v", d, ok)
	}
}

// beBlock construye un bloque pcapng big-endian.
func beBlock(typ uint32, body []byte) []byte {
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	b := binary.BigEndian.AppendUint32(nil, typ)
	b = binary.BigEndian.AppendUint32(b, uint32(len(body)+12))
	b = append(b, body...)
	return binary.BigEndian.AppendUint32(b, uint32(len(body)+12))
}

// TestPcapngBigEndianMicro lee un pcapng big-endian con resolución por
// defecto (µs), Ethernet y un SPB, como los que generan otras herramientas.
func TestPcapngBigEndianMicro(t *testing.T) {
	d := sample()[0]
	pkt, err := buildIPUDP(d)
	if err != nil {
		t.Fatal(err)
	}
	frame := append(make([]byte, 12), 0x08, 0x00)
	frame = append(frame, pkt...)
	be := binary.BigEndian
	shb := be.AppendUint32(nil, 0x1A2B3C4D)
	shb = be.AppendUint16(shb, 1)
	shb = be.AppendUint16(shb, 0)
	shb = be.AppendUint64(shb, ^uint64(0))
	idb := be.AppendUint16(nil, linkEthernet)
	idb = be.AppendUint16(idb, 0)
	idb = be.AppendUint32(idb, 0)
	us := uint64(d.Time.UnixMicro())
	epb := be.AppendUint32(nil, 0)
	epb = be.AppendUint32(epb, uint32(us>>32))
	epb = be.AppendUint32(epb, uint32(us))
	epb = be.AppendUint32(epb, uint32(len(frame)))
	epb = be.AppendUint32(epb, uint32(len(frame)))
	epb = append(epb, frame...)
	spb := be.AppendUint32(nil, uint32(len(frame)))
	spb = append(spb, frame...)
	var file []byte
	file = append(file, beBlock(pcapngSHB, shb)...)
	file = append(file, beBlock(pcapngIDB, idb)...)
	file = append(file, beBlock(pcapngEPB, epb)...)
	file = append(file, beBlock(pcapngSPB, spb)...)
	got, err := ReadAll(bytes.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].Time.Equal(d.Time.Truncate(time.Microsecond)) || got[0].Src != d.Src ||
		!bytes.Equal(got[1].Payload, d.Payload) {
		t.Fatalf("pcapng big-endian mal interpretado: %+v", got)
	}
}

func TestCreateGzip(t *testing.T) {
	for _, name := range []string{"x.hfsim.gz", "x.pcap.gz", "x.hfsim", "x.pcapng"} {
		p := filepath.Join(t.TempDir(), name)
		w, closeFn, err := Create(p, FormatFromPath(p))
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range sample() {
			if err := w.Write(d); err != nil {
				t.Fatal(err)
			}
		}
		if err := closeFn(); err != nil {
			t.Fatal(err)
		}
		n := 0
		if err := ReadFile(p, func(Datagram) error { n++; return nil }); err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Fatalf("%s: %d datagramas", name, n)
		}
	}
}

func FuzzRead(f *testing.F) {
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, FormatHFSim)
	for _, d := range sample() {
		_ = w.Write(d)
	}
	_ = w.Flush()
	f.Add(buf.Bytes())
	var pb bytes.Buffer
	pw, _ := NewWriter(&pb, FormatPcap)
	_ = pw.Write(sample()[0])
	_ = pw.Flush()
	f.Add(pb.Bytes())
	var ng bytes.Buffer
	nw, _ := NewWriter(&ng, FormatPcapng)
	_ = nw.Write(sample()[0])
	_ = nw.Flush()
	f.Add(ng.Bytes())
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(buf.Bytes())
	_ = zw.Close()
	f.Add(gz.Bytes())
	f.Fuzz(func(_ *testing.T, data []byte) {
		_ = Read(bytes.NewReader(data), func(Datagram) error { return nil })
	})
}
