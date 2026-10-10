//go:build integration

package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/services/alerts/migrations"
)

// telegramStub cuenta los sendMessage aceptados y puede fallar los primeros.
type telegramStub struct {
	mu       sync.Mutex
	failNext int
	accepted int
	block    chan struct{}
}

func (s *telegramStub) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	block := s.block
	if s.failNext > 0 {
		s.failNext--
		s.mu.Unlock()
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	s.mu.Unlock()
	if block != nil {
		<-block
	}
	s.mu.Lock()
	s.accepted++
	s.mu.Unlock()
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *telegramStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accepted
}

func newQueueService(t *testing.T, tg *telegramStub) (*Service, *pgdb.DB, pgdb.TenantID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: pgtest.New(t), AppRole: "alerts_app", PlatformRole: "alerts_platform"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := pgdb.Migrate(ctx, db, migrations.Schema, migrations.Postgres(), nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(tg)
	t.Cleanup(srv.Close)
	st := NewStore(db)
	svc := NewService(st, envelope.Ephemeral(), &Senders{TelegramAPI: srv.URL, TelegramToken: "1:installation-bot-token-xxxxxxxxxxxxxxxx"},
		pagination.NewCodec(nil), nil, "https://horus.example.net", nil)
	svc.SetRetry(4, []time.Duration{10 * time.Millisecond}, time.Second)
	tenant := pgdb.TenantID(uuid.Must(uuid.NewV7()))
	cfg, _ := json.Marshal(TelegramConfig{ChatID: "-1001"})
	c := &Channel{ID: uuid.Must(uuid.NewV7()), TenantID: tenant.UUID(), Name: "noc", Kind: KindTelegram, Enabled: true, Config: cfg,
		Subscription: Subscription{EventTypes: []string{eventTunnelDown}}, Status: statusOK, CreatedAt: time.Now(), Version: 1}
	if err := st.insertChannel(ctx, tenant, c); err != nil {
		t.Fatal(err)
	}
	return svc, db, tenant, c.ID
}

func tunnelDown(tenant pgdb.TenantID, router uuid.UUID) *natsx.Message {
	tid := tenant.UUID()
	id := uuid.Must(uuid.NewV7())
	data, _ := json.Marshal(map[string]any{"id": uuid.NewString(), "router_id": router.String()})
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	return &natsx.Message{Tenant: tid, Envelope: &natsx.Envelope{ID: id, Type: "horus.wireguard.peer.handshake_stale", TenantID: &tid,
		Data: data, TraceParent: &tp}}
}

func deliveryState(t *testing.T, db *pgdb.DB) (status string, attempts int) {
	t.Helper()
	if err := db.Pool.QueryRow(context.Background(), `SELECT status, attempts FROM alerts.notification_delivery`).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	return status, attempts
}

// D23: el evento se confirma al quedar en la cola; la reentrega del mismo
// evento no duplica; los fallos se reintentan con backoff y la entrega
// termina enviada una sola vez.
func TestQueueRetriesWithoutDuplicates(t *testing.T) {
	ctx := context.Background()
	tg := &telegramStub{failNext: 2}
	svc, db, tenant, _ := newQueueService(t, tg)
	msg := tunnelDown(tenant, uuid.New())
	if err := svc.HandleEvent(ctx, msg); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleEvent(ctx, msg); err != nil { // reentrega de NATS tras un reinicio
		t.Fatal(err)
	}
	if tg.count() != 0 {
		t.Fatal("HandleEvent must only queue")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := svc.DispatchOnce(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if st, _ := deliveryState(t, db); st == deliverySent {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	st, attempts := deliveryState(t, db)
	if st != deliverySent || attempts != 3 || tg.count() != 1 {
		t.Fatalf("status=%s attempts=%d accepted=%d", st, attempts, tg.count())
	}
	var n int
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM alerts.outbox WHERE subject LIKE 'horus.alerts.notification.sent.%'`).Scan(&n)
	if n != 1 {
		t.Fatalf("notification.sent events = %d", n)
	}
}

// Un proceso que muere a mitad de un envío (lease sin cerrar) no pierde la
// entrega: al vencer el plazo, otra pasada (el proceso reiniciado) la envía.
func TestQueueResumesClaimedDeliveryAfterCrash(t *testing.T) {
	ctx := context.Background()
	tg := &telegramStub{}
	svc, db, tenant, _ := newQueueService(t, tg)
	if err := svc.HandleEvent(ctx, tunnelDown(tenant, uuid.New())); err != nil {
		t.Fatal(err)
	}
	// "kill -9" justo después de reclamar: la fila queda con lease y sin resultado.
	claimed, err := svc.st.claimDue(ctx, 10, time.Second)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v %d", err, len(claimed))
	}
	if n, _ := svc.DispatchOnce(ctx, 10); n != 0 {
		t.Fatal("a claimed delivery was taken again before its lease expired")
	}
	time.Sleep(1100 * time.Millisecond)
	if n, err := svc.DispatchOnce(ctx, 10); err != nil || n != 1 {
		t.Fatalf("resume: n=%d err=%v", n, err)
	}
	if st, attempts := deliveryState(t, db); st != deliverySent || attempts != 2 || tg.count() != 1 {
		t.Fatalf("status=%s attempts=%d accepted=%d", st, attempts, tg.count())
	}
}

// Agotados los intentos queda failed (y notification.failed) sin más envíos.
func TestQueueGivesUpAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	tg := &telegramStub{failNext: 100}
	svc, db, tenant, _ := newQueueService(t, tg)
	if err := svc.HandleEvent(ctx, tunnelDown(tenant, uuid.New())); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		_, _ = svc.DispatchOnce(ctx, 10)
		if st, _ := deliveryState(t, db); st == deliveryFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st, attempts := deliveryState(t, db); st != deliveryFailed || attempts != 4 {
		t.Fatalf("status=%s attempts=%d", st, attempts)
	}
	var n int
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM alerts.outbox WHERE subject LIKE 'horus.alerts.notification.failed.%'`).Scan(&n)
	if n != 1 {
		t.Fatalf("notification.failed events = %d", n)
	}
}
