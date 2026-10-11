//go:build integration

package itest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

// Fuentes de reputación de plataforma en PostgreSQL: siembra/conciliación del
// catálogo (sin pisar `enabled`), unicidad, If-Match y secreto cifrado.
func TestReputationSourcesStore(t *testing.T) {
	ctx := context.Background()
	st := postgres.New(openPG(t))
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	lic := "CC0-1.0"
	cat := domain.ReputationSource{ID: uuid.New(), NumericID: 101, Key: "abusech_feodo", DatasetID: "abusech-feodo",
		Origin: domain.OriginCatalog, Name: "Feodo", URL: "https://feodotracker.abuse.ch/downloads/ipblocklist.csv",
		Format: "abusech-feodo-csv", Frequency: "1h", Category: "botnet_cc", Confidence: 90, Enabled: true, License: &lic,
		MinEntries: 1, MaxBytes: 16 << 20, CreatedAt: now, UpdatedAt: now, Version: 1}
	old := cat
	old.ID, old.NumericID, old.Key, old.DatasetID = uuid.New(), 102, "old_feed", "old-feed"
	if err := st.SyncCatalog(ctx, []domain.ReputationSource{cat, old}); err != nil {
		t.Fatal(err)
	}
	// El superadmin la desactiva…
	got, err := st.GetReputationSource(ctx, cat.ID)
	if err != nil {
		t.Fatal(err)
	}
	got.Enabled, got.Version = false, 2
	if err := st.UpdateReputationSource(ctx, got, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateReputationSource(ctx, got, 1); !errors.Is(err, domain.ErrSourceStale) {
		t.Fatalf("versión vieja: %v", err)
	}
	// …y al conciliar con una definición nueva se conserva la desactivación;
	// la que ya no está declarada desaparece.
	cat.Name, cat.Confidence = "Feodo Tracker", 85
	if err := st.SyncCatalog(ctx, []domain.ReputationSource{cat}); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetReputationSource(ctx, cat.ID)
	if got.Enabled || got.Name != "Feodo Tracker" || got.Confidence != 85 || got.Version != 3 {
		t.Fatalf("conciliación: %+v", got)
	}
	if err := st.SyncCatalog(ctx, []domain.ReputationSource{cat}); err != nil {
		t.Fatal(err)
	}
	if again, _ := st.GetReputationSource(ctx, cat.ID); again.Version != 3 {
		t.Fatalf("sin cambios no sube la versión: %d", again.Version)
	}
	if _, err := st.GetReputationSource(ctx, old.ID); !errors.Is(err, domain.ErrSourceNotFound) {
		t.Fatalf("retirada del catálogo: %v", err)
	}

	hdr := "Auth-Key"
	by := uuid.New()
	custom := domain.ReputationSource{ID: uuid.New(), NumericID: 2001, Key: "csirt_scanners", DatasetID: "custom-csirt_scanners",
		Origin: domain.OriginCustom, Name: "CSIRT", URL: "https://feeds.example.net/s.netset", Format: "netset", Frequency: "6h",
		Category: "scanner", Confidence: 70, Enabled: true, MinEntries: 1, MaxBytes: 64 << 20, AuthHeaderName: &hdr,
		AuthSecret:          &domain.SealedSecret{Ciphertext: []byte{1, 2, 3}, DEK: []byte{4, 5}, KEKID: "kek-1"},
		TermsAcknowledgedAt: &now, CreatedBy: &by, CreatedAt: now, UpdatedAt: now, Version: 1}
	if err := st.InsertReputationSource(ctx, &custom); err != nil {
		t.Fatal(err)
	}
	dup := custom
	dup.ID, dup.DatasetID, dup.NumericID = uuid.New(), "custom-other", 2002
	if err := st.InsertReputationSource(ctx, &dup); !errors.Is(err, domain.ErrSourceExists) {
		t.Fatalf("clave repetida: %v", err)
	}
	list, err := st.ListReputationSources(ctx)
	if err != nil || len(list) != 2 || list[0].Origin != domain.OriginCatalog {
		t.Fatalf("list: %+v %v", list, err)
	}
	c := list[1]
	if !c.HasAuth() || c.AuthSecret.KEKID != "kek-1" || string(c.AuthSecret.Ciphertext) != string([]byte{1, 2, 3}) || *c.CreatedBy != by {
		t.Fatalf("personalizada: %+v", c)
	}
	// Quitar la autenticación borra el secreto.
	c.AuthHeaderName, c.AuthSecret, c.Version = nil, nil, 2
	if err := st.UpdateReputationSource(ctx, &c, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteReputationSource(ctx, c.ID, 1); !errors.Is(err, domain.ErrSourceStale) {
		t.Fatalf("delete versión vieja: %v", err)
	}
	if err := st.DeleteReputationSource(ctx, c.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteReputationSource(ctx, c.ID, 2); !errors.Is(err, domain.ErrSourceNotFound) {
		t.Fatalf("delete inexistente: %v", err)
	}
}
