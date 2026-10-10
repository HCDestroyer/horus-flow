package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
)

// fakeMsg es un mensaje de JetStream en memoria.
type fakeMsg struct {
	data      []byte
	hdr       nats.Header
	delivered uint64

	mu        sync.Mutex
	acked     bool
	ackErr    error
	progress  int
	naks, trm int
}

func (m *fakeMsg) Data() []byte         { return m.data }
func (m *fakeMsg) Headers() nats.Header { return m.hdr }
func (m *fakeMsg) Subject() string      { return flowbus.SubjectBatchPrefix + "x" }
func (m *fakeMsg) Metadata() (*jetstream.MsgMetadata, error) {
	return &jetstream.MsgMetadata{NumDelivered: m.delivered, Stream: flowbus.StreamTelemetry}, nil
}

func (m *fakeMsg) DoubleAck(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ackErr != nil {
		return m.ackErr
	}
	m.acked = true
	return nil
}

func (m *fakeMsg) InProgress() error {
	m.mu.Lock()
	m.progress++
	m.mu.Unlock()
	return nil
}

func (m *fakeMsg) NakWithDelay(time.Duration) error {
	m.mu.Lock()
	m.naks++
	m.mu.Unlock()
	return nil
}

func (m *fakeMsg) Term() error {
	m.mu.Lock()
	m.trm++
	m.mu.Unlock()
	return nil
}

func (m *fakeMsg) isAcked() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.acked
}

// dedupCH imita flows_raw con insert_deduplication_token: un token ya
// insertado se descarta entero.
type dedupCH struct {
	mu      sync.Mutex
	fails   int
	tokens  map[string]bool
	inserts int
	rows    map[uuid.UUID]int // filas por batch_id
	sizes   []int
}

func newDedupCH() *dedupCH { return &dedupCH{tokens: map[string]bool{}, rows: map[uuid.UUID]int{}} }

func (d *dedupCH) Insert(_ context.Context, token string, rows []Row) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fails > 0 {
		d.fails--
		return errors.New("clickhouse down")
	}
	d.inserts++
	if d.tokens[token] {
		return nil
	}
	d.tokens[token] = true
	d.sizes = append(d.sizes, len(rows))
	for i := range rows {
		d.rows[rows[i].BatchID]++
	}
	return nil
}

func (d *dedupCH) total() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, v := range d.rows {
		n += v
	}
	return n
}

type memLedger struct {
	mu sync.Mutex
	m  map[string][]string
}

func (l *memLedger) Put(_ context.Context, token string, ids []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.m[token] = append([]string(nil), ids...)
	return nil
}

func (l *memLedger) Delete(_ context.Context, token string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, token)
	return nil
}

func (l *memLedger) Load(context.Context) (map[string][]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string][]string{}
	for k, v := range l.m {
		out[k] = v
	}
	return out, nil
}

const testTenant = "0192e000-0000-7000-8000-000000000001"

// batchMsg crea un lote de n registros atribuibles.
func batchMsg(t *testing.T, id string, n int) *fakeMsg {
	t.Helper()
	fb := &flowpb.FlowBatch{BatchID: id, TenantID: testTenant,
		RouterID: "0192e333-0000-7000-8000-000000000033", ReceivedTo: time.Now()}
	for i := range n {
		fb.Records = append(fb.Records, flowpb.FlowRecord{SrcIP: a("10.20.0.5"), DstIP: a("8.8.8.8"),
			Bytes: uint64(i + 1), Packets: 1})
	}
	h := nats.Header{}
	h.Set(flowbus.HeaderTenant, testTenant)
	return &fakeMsg{data: fb.Marshal(), hdr: h, delivered: 1}
}

func newTestConsumer(t *testing.T, ins Inserter, led Ledger, opts GroupOptions) *Consumer {
	t.Helper()
	return &Consumer{Proc: &Processor{Inv: store(t)}, Ins: ins, Workers: 3, Group: opts, Ledger: led,
		M: NewMetrics(nil), Log: slog.New(slog.DiscardHandler)}
}

// feed entrega msgs al consumidor y lo para cuando done() se cumple.
func feed(t *testing.T, c *Consumer, msgs []*fakeMsg, done func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan BusMsg, len(msgs))
	for _, m := range msgs {
		ch <- m
	}
	stopped := make(chan struct{})
	var once sync.Once
	next := func() (BusMsg, error) {
		select {
		case m := <-ch:
			return m, nil
		case <-stopped:
			return nil, errors.New("stopped")
		}
	}
	errc := make(chan error, 1)
	go func() { errc <- c.runGrouped(ctx, next, func() { once.Do(func() { close(stopped) }) }) }()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func allAcked(msgs []*fakeMsg) func() bool {
	return func() bool {
		for _, m := range msgs {
			if !m.isAcked() {
				return false
			}
		}
		return true
	}
}

// TestGroupedInsertByRows: 10 lotes de 100 filas con umbral de 300 → INSERT
// de ≥ 300 filas; todo confirmado y cada fila una sola vez.
func TestGroupedInsertByRows(t *testing.T) {
	ch := newDedupCH()
	c := newTestConsumer(t, ch, &memLedger{m: map[string][]string{}}, GroupOptions{Rows: 300, Wait: time.Hour})
	var msgs []*fakeMsg
	for range 9 {
		msgs = append(msgs, batchMsg(t, uuid.NewString(), 100))
	}
	feed(t, c, msgs, allAcked(msgs))
	if ch.total() != 900 {
		t.Fatalf("rows = %d, want 900", ch.total())
	}
	for _, s := range ch.sizes {
		if s < 300 {
			t.Fatalf("insert of %d rows below threshold: %v", s, ch.sizes)
		}
	}
	if len(ch.sizes) != 3 {
		t.Fatalf("inserts = %v, want 3", ch.sizes)
	}
}

// TestGroupedInsertByTime: por debajo del umbral, el grupo se escribe al
// cumplir Wait.
func TestGroupedInsertByTime(t *testing.T) {
	ch := newDedupCH()
	led := &memLedger{m: map[string][]string{}}
	c := newTestConsumer(t, ch, led, GroupOptions{Rows: 1 << 20, Wait: 50 * time.Millisecond})
	msgs := []*fakeMsg{batchMsg(t, uuid.NewString(), 10), batchMsg(t, uuid.NewString(), 10)}
	feed(t, c, msgs, allAcked(msgs))
	if ch.total() != 20 || len(ch.sizes) != 1 {
		t.Fatalf("rows=%d inserts=%v", ch.total(), ch.sizes)
	}
	if len(led.m) != 0 {
		t.Fatalf("ledger not cleaned: %v", led.m)
	}
}

// TestGroupedNoAckBeforeInsert: con ClickHouse caído no se confirma nada; al
// volver se escribe una vez y se confirma.
func TestGroupedNoAckBeforeInsert(t *testing.T) {
	ch := newDedupCH()
	ch.fails = 3
	c := newTestConsumer(t, ch, &memLedger{m: map[string][]string{}}, GroupOptions{Rows: 150, Wait: time.Hour})
	msgs := []*fakeMsg{batchMsg(t, uuid.NewString(), 100), batchMsg(t, uuid.NewString(), 100)}
	time.AfterFunc(100*time.Millisecond, func() {
		for _, m := range msgs {
			if m.isAcked() {
				t.Error("acked while ClickHouse was down")
			}
		}
	})
	feed(t, c, msgs, allAcked(msgs))
	if ch.total() != 200 {
		t.Fatalf("rows = %d", ch.total())
	}
	if msgs[0].progress == 0 {
		t.Fatal("InProgress not sent while retrying")
	}
}

// TestGroupedDuplicateDelivery: una reentrega del mismo lote mientras su
// grupo está pendiente no duplica filas y confirma ambas entregas.
func TestGroupedDuplicateDelivery(t *testing.T) {
	ch := newDedupCH()
	c := newTestConsumer(t, ch, nil, GroupOptions{Rows: 1 << 20, Wait: 100 * time.Millisecond})
	id := uuid.NewString()
	first, again := batchMsg(t, id, 50), batchMsg(t, id, 50)
	again.delivered = 2
	msgs := []*fakeMsg{first, again}
	feed(t, c, msgs, allAcked(msgs))
	if ch.total() != 50 {
		t.Fatalf("rows = %d, want 50", ch.total())
	}
}

// TestGroupedCrashRecovery: el proceso muere después del INSERT y antes de
// confirmar. Al arrancar, los lotes reentregados (en otro orden y mezclados
// con lotes nuevos) se reagrupan con el token del Ledger: ClickHouse descarta
// el reintento y las filas quedan una sola vez.
func TestGroupedCrashRecovery(t *testing.T) {
	ch := newDedupCH()
	led := &memLedger{m: map[string][]string{}}
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	var run1 []*fakeMsg
	for _, id := range ids {
		m := batchMsg(t, id, 100)
		m.ackErr = errors.New("process killed")
		run1 = append(run1, m)
	}
	c1 := newTestConsumer(t, ch, led, GroupOptions{Rows: 300, Wait: time.Hour})
	feed(t, c1, run1, func() bool { return ch.total() == 300 })
	if len(led.m) != 1 {
		t.Fatalf("ledger = %v, want the unconfirmed group", led.m)
	}

	// Segundo proceso: reentrega en otro orden, uno de los tres ya se había
	// confirmado (confirmación parcial) y llegan lotes nuevos.
	fresh := batchMsg(t, uuid.NewString(), 100)
	r1, r2 := batchMsg(t, ids[2], 100), batchMsg(t, ids[0], 100)
	r1.delivered, r2.delivered = 2, 2
	run2 := []*fakeMsg{fresh, r1, r2}
	c2 := newTestConsumer(t, ch, led, GroupOptions{Rows: 1 << 20, Wait: 50 * time.Millisecond, RecoveryWait: 100 * time.Millisecond})
	feed(t, c2, run2, allAcked(run2))
	if ch.total() != 400 {
		t.Fatalf("rows = %d, want 400 (no duplicates)", ch.total())
	}
	for _, id := range ids {
		if n := ch.rows[uuid.MustParse(id)]; n != 100 {
			t.Fatalf("batch %s has %d rows", id, n)
		}
	}
	if len(led.m) != 0 {
		t.Fatalf("ledger not cleaned: %v", led.m)
	}
}

// TestGroupedRecoveryNotInserted: el proceso murió antes del INSERT (grupo en
// el Ledger sin escribir): la reentrega lo escribe completo.
func TestGroupedRecoveryNotInserted(t *testing.T) {
	ch := newDedupCH()
	ids := []string{uuid.NewString(), uuid.NewString()}
	led := &memLedger{m: map[string][]string{"tok-1": ids}}
	msgs := []*fakeMsg{batchMsg(t, ids[1], 100), batchMsg(t, ids[0], 100)}
	c := newTestConsumer(t, ch, led, GroupOptions{Rows: 1 << 20, Wait: time.Hour, RecoveryWait: time.Hour})
	feed(t, c, msgs, allAcked(msgs))
	if ch.total() != 200 || !ch.tokens["tok-1"] {
		t.Fatalf("rows=%d tokens=%v", ch.total(), ch.tokens)
	}
}

// TestGroupedRejects: un lote corrupto va a DLQ sin bloquear el grupo.
func TestGroupedRejects(t *testing.T) {
	ch := newDedupCH()
	c := newTestConsumer(t, ch, nil, GroupOptions{Rows: 1 << 20, Wait: 20 * time.Millisecond})
	bad := &fakeMsg{data: []byte{0xff}, hdr: nats.Header{}, delivered: 1}
	good := batchMsg(t, uuid.NewString(), 5)
	feed(t, c, []*fakeMsg{bad, good}, func() bool {
		bad.mu.Lock()
		defer bad.mu.Unlock()
		return bad.trm == 1 && good.isAcked()
	})
	if ch.total() != 5 {
		t.Fatalf("rows = %d", ch.total())
	}
}
