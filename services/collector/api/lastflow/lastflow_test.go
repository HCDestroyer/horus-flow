package lastflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFromStatesFiltersTenantAndMissing(t *testing.T) {
	tenant, other := uuid.New(), uuid.New()
	withFlow, noFlow, foreign, missing := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.FixedZone("x", 3600))
	states := map[uuid.UUID]*Exporter{
		withFlow: {TenantID: tenant, LastFlowAt: &at},
		noFlow:   {TenantID: tenant},
		foreign:  {TenantID: other, LastFlowAt: &at},
	}
	calls := 0
	r := FromStates(func(_ context.Context, id uuid.UUID) (*Exporter, error) {
		calls++
		return states[id], nil
	})
	got, err := r.LastFlowAt(context.Background(), tenant, []uuid.UUID{withFlow, noFlow, foreign, missing, withFlow})
	if err != nil || len(got) != 1 || !got[withFlow].Equal(at) || got[withFlow].Location() != time.UTC {
		t.Fatalf("got = %v %v", got, err)
	}
	if calls != 4 {
		t.Fatalf("lecturas = %d (un router repetido se lee una vez)", calls)
	}
	boom := errors.New("kv caído")
	if _, err := FromStates(func(context.Context, uuid.UUID) (*Exporter, error) { return nil, boom }).
		LastFlowAt(context.Background(), tenant, []uuid.UUID{withFlow}); !errors.Is(err, boom) {
		t.Fatalf("error = %v", err)
	}
}
