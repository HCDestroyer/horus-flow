// Package spool es el spool a disco del collector (docs/architecture.md
// §10.15, D23): cuando NATS no acepta lotes, el publicador los escribe aquí y
// al volver el bus los reenvía en orden con el mismo Nats-Msg-Id (= batch_id),
// así JetStream descarta lo que ya tenía.
//
// Formato: segmentos append-only `<seq>.seg` en Dir. Cada registro es
//
//	magia "HSP1" | longitud uint32 | CRC32C uint32 | carga
//	carga = uvarint registros | varint instante (ns) | asunto | cabeceras | cuerpo
//
// La magia permite resincronizar tras una zona corrupta (el registro dañado
// se salta y se cuenta, el resto del segmento se lee). Un registro a medio
// escribir al final del último segmento (kill -9 durante la escritura) se
// trunca al abrir. El cursor de lectura (segmento, desplazamiento) se guarda
// en `cursor` tras cada lote confirmado (pwrite: sobrevive a kill -9; fsync
// según Fsync), de modo que un reinicio no reenvía lo ya confirmado salvo el
// último lote si murió entre el PubAck y el cursor (JetStream lo descarta por
// Nats-Msg-Id).
//
// Límite de tamaño: si añadir un lote supera MaxBytes se borra el segmento más
// antiguo (lo más antiguo se descarta y se cuenta, OnDrop "full"). Con el disco
// lleno (ENOSPC) se hace lo mismo y se reintenta una vez; si sigue fallando el
// lote nuevo se descarta (OnDrop "write_error").
package spool

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	headerLen    = 12
	maxRecordLen = 16 << 20
	cursorName   = "cursor"
	segSuffix    = ".seg"
)

var (
	magic  = [4]byte{'H', 'S', 'P', '1'}
	crcTab = crc32.MakeTable(crc32.Castagnoli)
	// ErrClosed indica un spool cerrado.
	ErrClosed = errors.New("spool: closed")
)

// Razones de descarte (OnDrop).
const (
	DropFull       = "full"
	DropCorrupt    = "corrupt"
	DropWriteError = "write_error"
)

// Options configura el spool.
type Options struct {
	Dir string
	// MaxBytes es el tamaño máximo de los segmentos en disco.
	MaxBytes int64
	// SegmentBytes es el tamaño al que se cierra un segmento (≤ MaxBytes/2).
	SegmentBytes int64
	// Fsync: 0 = tras cada escritura; > 0 = como mucho cada Fsync; < 0 = solo
	// al cerrar un segmento.
	Fsync time.Duration
	// OnDrop se llama con los lotes y registros descartados (sin el cerrojo).
	OnDrop func(reason string, batches, records int)
	Log    *slog.Logger
	// writeHook sustituye la escritura (pruebas de disco lleno).
	writeHook func(f *os.File, b []byte) (int, error)
}

type segment struct {
	seq     uint64
	path    string
	size    int64
	records int // registros válidos sin leer (el de lectura: desde el cursor)
	batches int
	firstAt time.Time
}

// Spool es seguro para uso concurrente.
type Spool struct {
	o Options

	mu       sync.Mutex
	closed   bool
	segs     []*segment // orden de seq; el último es el de escritura
	w        *os.File
	r        *os.File // segmento de lectura (segs[0])
	readOff  int64
	cursor   *os.File
	head     *entry
	pending  int // lotes sin leer
	pendRecs int
	lastSync time.Time
	dirty    bool
}

type entry struct {
	msg     *nats.Msg
	records int
	at      time.Time
	next    int64 // desplazamiento tras el registro
}

// Open abre (o crea) el spool en o.Dir y recupera lo pendiente.
func Open(o Options) (*Spool, error) {
	if o.Dir == "" {
		return nil, errors.New("spool: empty dir")
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = 8 << 30
	}
	if o.SegmentBytes <= 0 {
		o.SegmentBytes = 64 << 20
	}
	o.SegmentBytes = min(o.SegmentBytes, max(o.MaxBytes/2, 1<<16))
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if err := os.MkdirAll(o.Dir, 0o750); err != nil {
		return nil, fmt.Errorf("spool: %w", err)
	}
	s := &Spool{o: o, lastSync: time.Now()}
	if err := s.recover(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Spool) segPath(seq uint64) string {
	return filepath.Join(s.o.Dir, fmt.Sprintf("%020d%s", seq, segSuffix))
}

// recover lee el cursor y los segmentos, trunca un registro incompleto al
// final del último y cuenta lo pendiente.
func (s *Spool) recover() error {
	cf, err := os.OpenFile(filepath.Join(s.o.Dir, cursorName), os.O_RDWR|os.O_CREATE, 0o640)
	if err != nil {
		return fmt.Errorf("spool: cursor: %w", err)
	}
	s.cursor = cf
	curSeg, curOff := readCursor(cf)
	names, err := os.ReadDir(s.o.Dir)
	if err != nil {
		return fmt.Errorf("spool: %w", err)
	}
	var seqs []uint64
	for _, n := range names {
		if !strings.HasSuffix(n.Name(), segSuffix) {
			continue
		}
		seq, err := strconv.ParseUint(strings.TrimSuffix(n.Name(), segSuffix), 10, 64)
		if err != nil {
			continue
		}
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for i, seq := range seqs {
		p := s.segPath(seq)
		if seq < curSeg {
			_ = os.Remove(p) // ya leído entero
			continue
		}
		from := int64(0)
		if seq == curSeg {
			from = curOff
		}
		sg, valid, corrupt, err := scanSegment(p, seq, from)
		if err != nil {
			return err
		}
		if seq == curSeg && curOff > sg.size {
			curOff = sg.size // el segmento se truncó por detrás del cursor
		}
		if i == len(seqs)-1 && valid < sg.size {
			// Cola incompleta (corte a mitad de escritura) del segmento de escritura.
			if err := os.Truncate(p, valid); err != nil {
				return fmt.Errorf("spool: truncate %s: %w", p, err)
			}
			s.o.Log.Warn("collector spool: incomplete record at the end of the last segment truncated",
				"segment", p, "bytes", sg.size-valid)
			sg.size = valid
		} else if corrupt > 0 {
			s.o.Log.Warn("collector spool: corrupt region in segment (skipped on replay)", "segment", p, "bytes", corrupt)
		}
		s.segs = append(s.segs, sg)
		s.pending += sg.batches
		s.pendRecs += sg.records
	}
	if len(s.segs) == 0 || (curSeg > s.segs[0].seq) {
		curOff = 0
	}
	if len(s.segs) > 0 && s.segs[0].seq != curSeg {
		curOff = 0
	}
	if len(s.segs) == 0 {
		next := curSeg + 1
		s.segs = append(s.segs, &segment{seq: next, path: s.segPath(next)})
	}
	last := s.segs[len(s.segs)-1]
	w, err := os.OpenFile(last.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o640)
	if err != nil {
		return fmt.Errorf("spool: %w", err)
	}
	s.w = w
	s.readOff = curOff
	if err := s.openReader(); err != nil {
		return err
	}
	if s.pending > 0 {
		s.o.Log.Info("collector spool: pending batches found at startup", "batches", s.pending, "records", s.pendRecs,
			"segments", len(s.segs), "bytes", s.diskBytes())
	}
	return nil
}

func (s *Spool) openReader() error {
	if s.r != nil {
		_ = s.r.Close()
		s.r = nil
	}
	r, err := os.Open(s.segs[0].path)
	if err != nil {
		return fmt.Errorf("spool: %w", err)
	}
	s.r = r
	return nil
}

func readCursor(f *os.File) (uint64, int64) {
	var b [20]byte
	if n, _ := f.ReadAt(b[:], 0); n != len(b) {
		return 0, 0
	}
	if crc32.Checksum(b[:16], crcTab) != binary.BigEndian.Uint32(b[16:]) {
		return 0, 0
	}
	return binary.BigEndian.Uint64(b[:8]), int64(binary.BigEndian.Uint64(b[8:16])) //nolint:gosec // desplazamiento
}

// scanSegment cuenta los registros válidos desde from y devuelve el final del
// último registro válido y los bytes corruptos saltados.
func scanSegment(path string, seq uint64, from int64) (*segment, int64, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("spool: %w", err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("spool: %w", err)
	}
	sg := &segment{seq: seq, path: path, size: fi.Size()}
	off, valid, corrupt := from, from, int64(0)
	for off < sg.size {
		e, next, skipped, err := readAt(f, off, sg.size)
		corrupt += skipped
		if err != nil {
			break
		}
		if sg.firstAt.IsZero() {
			sg.firstAt = e.at
		}
		sg.batches++
		sg.records += e.records
		off, valid = next, next
	}
	return sg, valid, corrupt, nil
}

// readAt lee el registro en off o, si está dañado, el siguiente válido
// (skipped son los bytes saltados). io.EOF si no queda ninguno completo.
func readAt(f *os.File, off, size int64) (*entry, int64, int64, error) {
	start := off
	for {
		if size-off < headerLen {
			return nil, off, off - start, io.EOF
		}
		var h [headerLen]byte
		if _, err := f.ReadAt(h[:], off); err != nil {
			return nil, off, off - start, io.EOF
		}
		if !bytes.Equal(h[:4], magic[:]) {
			next, ok := findMagic(f, off+1, size)
			if !ok {
				return nil, size, size - start, io.EOF
			}
			off = next
			continue
		}
		l := int64(binary.BigEndian.Uint32(h[4:8]))
		if l > maxRecordLen || off+headerLen+l > size {
			if off+headerLen+l > size && l <= maxRecordLen {
				// Puede ser un registro incompleto al final: no se salta.
				if next, ok := findMagic(f, off+1, size); ok {
					off = next
					continue
				}
				return nil, off, off - start, io.EOF
			}
			next, ok := findMagic(f, off+1, size)
			if !ok {
				return nil, size, size - start, io.EOF
			}
			off = next
			continue
		}
		payload := make([]byte, l)
		if _, err := f.ReadAt(payload, off+headerLen); err != nil {
			return nil, off, off - start, io.EOF
		}
		if crc32.Checksum(payload, crcTab) != binary.BigEndian.Uint32(h[8:12]) {
			next, ok := findMagic(f, off+1, size)
			if !ok {
				return nil, size, size - start, io.EOF
			}
			off = next
			continue
		}
		e, err := decodePayload(payload)
		if err != nil {
			next, ok := findMagic(f, off+1, size)
			if !ok {
				return nil, size, size - start, io.EOF
			}
			off = next
			continue
		}
		e.next = off + headerLen + l
		return e, e.next, off - start, nil
	}
}

func findMagic(f *os.File, off, size int64) (int64, bool) {
	buf := make([]byte, 64<<10)
	for off < size {
		n, _ := f.ReadAt(buf, off)
		if n <= 0 {
			return 0, false
		}
		if i := bytes.Index(buf[:n], magic[:]); i >= 0 {
			return off + int64(i), true
		}
		if n < len(magic) {
			return 0, false
		}
		off += int64(n - len(magic) + 1)
	}
	return 0, false
}

func encode(m *nats.Msg, records int, at time.Time) []byte {
	p := make([]byte, 0, 64+len(m.Subject)+len(m.Data)+64*len(m.Header))
	p = binary.AppendUvarint(p, uint64(records)) //nolint:gosec // >= 0
	p = binary.AppendVarint(p, at.UnixNano())
	p = appendBytes(p, []byte(m.Subject))
	keys := make([]string, 0, len(m.Header))
	for k := range m.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	p = binary.AppendUvarint(p, uint64(len(keys)))
	for _, k := range keys {
		p = appendBytes(p, []byte(k))
		vs := m.Header[k]
		p = binary.AppendUvarint(p, uint64(len(vs)))
		for _, v := range vs {
			p = appendBytes(p, []byte(v))
		}
	}
	p = append(p, m.Data...)
	rec := make([]byte, headerLen, headerLen+len(p))
	copy(rec, magic[:])
	binary.BigEndian.PutUint32(rec[4:], uint32(len(p))) //nolint:gosec // < maxRecordLen
	binary.BigEndian.PutUint32(rec[8:], crc32.Checksum(p, crcTab))
	return append(rec, p...)
}

func appendBytes(p, b []byte) []byte {
	p = binary.AppendUvarint(p, uint64(len(b)))
	return append(p, b...)
}

var errPayload = errors.New("spool: bad payload")

func decodePayload(p []byte) (*entry, error) {
	rd := bytes.NewReader(p)
	recs, err := binary.ReadUvarint(rd)
	if err != nil {
		return nil, errPayload
	}
	at, err := binary.ReadVarint(rd)
	if err != nil {
		return nil, errPayload
	}
	subj, err := readBytes(rd)
	if err != nil {
		return nil, errPayload
	}
	m := nats.NewMsg(string(subj))
	nh, err := binary.ReadUvarint(rd)
	if err != nil || nh > 256 {
		return nil, errPayload
	}
	for range nh {
		k, err := readBytes(rd)
		if err != nil {
			return nil, errPayload
		}
		nv, err := binary.ReadUvarint(rd)
		if err != nil || nv > 256 {
			return nil, errPayload
		}
		for range nv {
			v, err := readBytes(rd)
			if err != nil {
				return nil, errPayload
			}
			m.Header[string(k)] = append(m.Header[string(k)], string(v))
		}
	}
	m.Data = p[len(p)-rd.Len():]
	return &entry{msg: m, records: int(recs), at: time.Unix(0, at)}, nil //nolint:gosec // acotado por el CRC
}

func readBytes(rd *bytes.Reader) ([]byte, error) {
	n, err := binary.ReadUvarint(rd)
	if err != nil || n > uint64(rd.Len()) {
		return nil, errPayload
	}
	b := make([]byte, n)
	_, err = io.ReadFull(rd, b)
	return b, err
}

func (s *Spool) diskBytes() int64 {
	var n int64
	for _, sg := range s.segs {
		n += sg.size
	}
	return n
}

// Append escribe un lote al final. Devuelve error si no se pudo escribir (el
// lote se ha descartado y contado como write_error).
func (s *Spool) Append(m *nats.Msg, records int) error {
	rec := encode(m, records, time.Now())
	var drops []dropInfo
	s.mu.Lock()
	err := s.appendLocked(rec, records, &drops)
	s.mu.Unlock()
	for _, d := range drops {
		if s.o.OnDrop != nil {
			s.o.OnDrop(d.reason, d.batches, d.records)
		}
	}
	return err
}

type dropInfo struct {
	reason           string
	batches, records int
}

func (s *Spool) appendLocked(rec []byte, records int, drops *[]dropInfo) error {
	if s.closed {
		*drops = append(*drops, dropInfo{DropWriteError, 1, records})
		return ErrClosed
	}
	size := int64(len(rec))
	cur := s.segs[len(s.segs)-1]
	if cur.size > 0 && cur.size+size > s.o.SegmentBytes {
		if err := s.rotateLocked(); err != nil {
			*drops = append(*drops, dropInfo{DropWriteError, 1, records})
			return err
		}
		cur = s.segs[len(s.segs)-1]
	}
	for s.diskBytes()+size > s.o.MaxBytes && len(s.segs) > 1 {
		*drops = append(*drops, s.dropOldestLocked())
	}
	err := s.writeLocked(rec)
	if isNoSpace(err) && len(s.segs) > 1 {
		*drops = append(*drops, s.dropOldestLocked())
		err = s.writeLocked(rec)
	}
	if err != nil {
		*drops = append(*drops, dropInfo{DropWriteError, 1, records})
		return err
	}
	if cur.firstAt.IsZero() {
		cur.firstAt = time.Now()
	}
	cur.size += size
	cur.batches++
	cur.records += records
	s.pending++
	s.pendRecs += records
	s.dirty = true
	s.maybeSyncLocked(false)
	return nil
}

func isNoSpace(err error) bool { return errors.Is(err, syscall.ENOSPC) }

// writeLocked escribe rec entero o deja el segmento como estaba.
func (s *Spool) writeLocked(rec []byte) error {
	cur := s.segs[len(s.segs)-1]
	var n int
	var err error
	if s.o.writeHook != nil {
		n, err = s.o.writeHook(s.w, rec)
	} else {
		n, err = s.w.Write(rec)
	}
	if err == nil && n != len(rec) {
		err = io.ErrShortWrite
	}
	if err != nil && n > 0 {
		_ = s.w.Truncate(cur.size)
	}
	return err
}

func (s *Spool) rotateLocked() error {
	cur := s.segs[len(s.segs)-1]
	_ = s.w.Sync() // un segmento cerrado siempre queda en disco
	_ = s.w.Close()
	next := &segment{seq: cur.seq + 1, path: s.segPath(cur.seq + 1)}
	w, err := os.OpenFile(next.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o640)
	if err != nil {
		// Sin segmento nuevo se sigue en el actual.
		w2, err2 := os.OpenFile(cur.path, os.O_WRONLY|os.O_APPEND, 0o640)
		if err2 == nil {
			s.w = w2
		}
		return fmt.Errorf("spool: %w", err)
	}
	s.w = w
	s.segs = append(s.segs, next)
	syncDir(s.o.Dir)
	return nil
}

// dropOldestLocked borra el segmento más antiguo (con len(segs) > 1).
func (s *Spool) dropOldestLocked() dropInfo {
	old := s.segs[0]
	d := dropInfo{DropFull, old.batches, old.records}
	s.pending -= old.batches
	s.pendRecs -= old.records
	if s.r != nil {
		_ = s.r.Close()
		s.r = nil
	}
	_ = os.Remove(old.path)
	s.segs = s.segs[1:]
	s.readOff = 0
	s.head = nil
	_ = s.openReader()
	s.writeCursorLocked()
	s.o.Log.Warn("collector spool full: oldest segment dropped", "segment", old.path, "batches", old.batches, "records", old.records)
	return d
}

func (s *Spool) maybeSyncLocked(force bool) {
	if !s.dirty {
		return
	}
	if force || s.o.Fsync == 0 || (s.o.Fsync > 0 && time.Since(s.lastSync) >= s.o.Fsync) {
		_ = s.w.Sync()
		_ = s.cursor.Sync()
		s.lastSync = time.Now()
		s.dirty = false
	}
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}

// Peek devuelve el lote más antiguo sin confirmar (ok=false si no hay).
func (s *Spool) Peek() (*nats.Msg, int, bool) {
	var drops []dropInfo
	s.mu.Lock()
	m, n, ok := s.peekLocked(&drops)
	s.mu.Unlock()
	for _, d := range drops {
		if s.o.OnDrop != nil {
			s.o.OnDrop(d.reason, d.batches, d.records)
		}
	}
	return m, n, ok
}

func (s *Spool) peekLocked(drops *[]dropInfo) (*nats.Msg, int, bool) {
	if s.closed {
		return nil, 0, false
	}
	if s.head != nil {
		return s.head.msg, s.head.records, true
	}
	for {
		rs := s.segs[0]
		e, _, skipped, err := readAt(s.r, s.readOff, rs.size)
		if skipped > 0 {
			*drops = append(*drops, dropInfo{reason: DropCorrupt, batches: 1})
			s.o.Log.Warn("collector spool: corrupt bytes skipped on replay", "segment", rs.path, "offset", s.readOff, "bytes", skipped)
		}
		if err == nil {
			s.head = e
			return e.msg, e.records, true
		}
		if len(s.segs) == 1 {
			// Segmento de escritura leído entero: el spool está vacío.
			s.readOff = rs.size
			s.pending, s.pendRecs = 0, 0
			rs.batches, rs.records = 0, 0
			return nil, 0, false
		}
		// Segmento cerrado leído entero: se borra y se pasa al siguiente.
		_ = s.r.Close()
		s.r = nil
		_ = os.Remove(rs.path)
		s.pending -= rs.batches
		s.pendRecs -= rs.records
		s.segs = s.segs[1:]
		s.readOff = 0
		if err := s.openReader(); err != nil {
			return nil, 0, false
		}
		s.writeCursorLocked()
	}
}

// Commit confirma el lote devuelto por Peek (publicado con PubAck).
func (s *Spool) Commit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.head == nil {
		return
	}
	rs := s.segs[0]
	s.readOff = s.head.next
	rs.batches--
	rs.records -= s.head.records
	s.pending--
	s.pendRecs -= s.head.records
	s.head = nil
	s.writeCursorLocked()
	s.dirty = true
	s.maybeSyncLocked(false)
}

func (s *Spool) writeCursorLocked() {
	var b [20]byte
	binary.BigEndian.PutUint64(b[:8], s.segs[0].seq)
	binary.BigEndian.PutUint64(b[8:16], uint64(s.readOff)) //nolint:gosec // >= 0
	binary.BigEndian.PutUint32(b[16:], crc32.Checksum(b[:16], crcTab))
	_, _ = s.cursor.WriteAt(b[:], 0)
}

// Stats es el estado del spool.
type Stats struct {
	Batches, Records int
	Bytes            int64
	Segments         int
	Oldest           time.Time
	MaxBytes         int64
}

// Stats devuelve lo pendiente.
func (s *Spool) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Stats{Batches: s.pending, Records: s.pendRecs, Bytes: s.diskBytes(), Segments: len(s.segs), MaxBytes: s.o.MaxBytes}
	if s.head != nil {
		st.Oldest = s.head.at
	} else if s.pending > 0 {
		st.Oldest = s.segs[0].firstAt
	}
	return st
}

// Empty dice si no queda nada por reenviar.
func (s *Spool) Empty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending <= 0 && s.head == nil
}

// Sync fuerza fsync de datos y cursor.
func (s *Spool) Sync() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = true
	s.maybeSyncLocked(true)
}

// Close sincroniza y cierra.
func (s *Spool) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.dirty = true
	s.maybeSyncLocked(true)
	s.closed = true
	if s.r != nil {
		_ = s.r.Close()
	}
	_ = s.cursor.Close()
	return s.w.Close()
}
