package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
)

// peersStore es un Store que solo implementa AllPeers.
type peersStore struct {
	Store
	peers []domain.Peer
	err   error
}

func (s peersStore) AllPeers(context.Context) ([]domain.Peer, error) { return s.peers, s.err }

func TestOverviewStatsCountsStaleTunnels(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { v := now.Add(-d); return &v }
	a, b := uuid.New(), uuid.New()
	svc := New(Options{Now: func() time.Time { return now }, Store: peersStore{peers: []domain.Peer{
		{TenantID: a, LastHandshakeAt: ago(10 * time.Second)},                // ok
		{TenantID: a, LastHandshakeAt: ago(domain.StaleAfter + time.Second)}, // caído
		{TenantID: a, LastHandshakeAt: ago(time.Hour)},                       // caído
		{TenantID: a}, // sin handshake aún: no cuenta
		{TenantID: b, LastHandshakeAt: ago(time.Minute)}, // ok
	}}})
	got, err := svc.OverviewStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n := got[a].TunnelsDown; n == nil || *n != 2 {
		t.Fatalf("tenant a tunnels_down = %v, want 2", n)
	}
	if n := got[b].TunnelsDown; n == nil || *n != 0 {
		t.Fatalf("tenant b tunnels_down = %v, want 0", n)
	}
	if got[a].RoutersTotal != nil || got[a].OpenFindingsCritical != nil {
		t.Fatalf("wireguard must only report tunnels: %+v", got[a])
	}
}

func TestOverviewStatsStoreError(t *testing.T) {
	svc := New(Options{Store: peersStore{err: errors.New("down")}})
	if _, err := svc.OverviewStats(context.Background()); err == nil {
		t.Fatal("want error")
	}
}
