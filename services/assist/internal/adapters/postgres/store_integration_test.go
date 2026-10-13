//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/packages/go/tenanttest"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/app"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/assist/migrations"
)

func newStore(t *testing.T) (*Store, *pgdb.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: pgtest.New(t), AppRole: "assist_app", PlatformRole: "assist_platform"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := pgdb.Migrate(ctx, db, migrations.Schema, migrations.Postgres(), nil); err != nil {
		t.Fatal(err)
	}
	return New(db), db
}

func TestAssistSchemaHasForcedRLS(t *testing.T) {
	_, db := newStore(t)
	tables, err := tenanttest.InspectRLS(context.Background(), db.Pool, []string{migrations.Schema})
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 3 {
		t.Fatalf("tablas con tenant_id = %d", len(tables))
	}
	for _, tb := range tables {
		if !tb.Enabled || !tb.Forced || tb.TenantNullable {
			t.Errorf("%s: sin RLS forzada o tenant_id admite NULL: %+v", tb.Table, tb)
		}
	}
}

func TestMigrationsDownUp(t *testing.T) {
	_, db := newStore(t)
	ctx := context.Background()
	m, err := pgdb.NewMigrator(ctx, db, migrations.Schema, migrations.Postgres(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.DownTo(ctx, 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("up de nuevo: %v", err)
	}
}

func TestSettingsAuditTokensAndTenancy(t *testing.T) {
	st, db := newStore(t)
	ctx := context.Background()
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	user := uuid.Must(uuid.NewV7())
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

	if _, err := st.GetSettings(ctx, a); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("sin configuración: %v", err)
	}
	in := &domain.Settings{TenantID: a, Enabled: true, Provider: "anthropic", Model: "claude-opus-5-5", Effort: "high",
		DailyRequestLimit: 50, Sealed: &domain.SealedKey{Ciphertext: []byte{1, 2}, DEKWrapped: []byte{3}, KEKID: "kek-1"},
		UpdatedAt: now, UpdatedBy: &user}
	got, err := st.PutSettings(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Stored || got.Version != 1 || got.Sealed == nil || got.Sealed.KEKID != "kek-1" || got.Effort != "high" || *got.UpdatedBy != user {
		t.Fatalf("settings = %+v", got)
	}
	in.Provider, in.BaseURL, in.Sealed, in.Model = "openai_compatible", "http://10.0.0.5:11434", nil, "llama3.1"
	got, err = st.PutSettings(ctx, in)
	if err != nil || got.Version != 2 || got.Sealed != nil || got.BaseURL != "http://10.0.0.5:11434" {
		t.Fatalf("update = %+v %v", got, err)
	}
	// La base rechaza openai_compatible sin base_url (CHECK).
	in.BaseURL = ""
	if _, err := st.PutSettings(ctx, in); err == nil {
		t.Fatal("openai_compatible sin base_url aceptado")
	}
	// Aislamiento: el otro tenant no ve la fila.
	if _, err := st.GetSettings(ctx, b); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("tenant B ve la configuración de A: %v", err)
	}
	// Sin tenant (rol de aplicación), RLS devuelve cero filas.
	var n int
	if err := db.AppTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM assist.settings`).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("sin tenant: n=%d err=%v", n, err)
	}
	// Insertar una fila de otro tenant dentro de la transacción de A falla (WITH CHECK).
	err = db.TenantTx(ctx, pgdb.TenantID(a), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO assist.audit (id, tenant_id, feature, outcome) VALUES ($1, $2, 'finding', 'success')`,
			uuid.Must(uuid.NewV7()), b)
		return err
	})
	if err == nil {
		t.Fatal("insert cruzado de tenant aceptado")
	}

	// Auditoría y conteo del día.
	for i, e := range []domain.AuditEntry{
		{Feature: "finding", Outcome: "success", CreatedAt: now},
		{Feature: "router", Outcome: "error", CreatedAt: now.Add(time.Minute), ErrorCode: "PROVIDER_UNAVAILABLE"},
		{Feature: "finding", Outcome: "limited", CreatedAt: now.Add(2 * time.Minute)},
		{Feature: "test", Outcome: "success", CreatedAt: now.Add(3 * time.Minute)},
		{Feature: "mcp", Outcome: "success", CreatedAt: now.Add(4 * time.Minute), ContextID: "get_router"},
		{Feature: "finding", Outcome: "success", CreatedAt: now.Add(-24 * time.Hour)},
	} {
		e := e
		e.ID, e.TenantID, e.UserID, e.Provider, e.Model = uuid.Must(uuid.NewV7()), a, &user, "anthropic", "m"
		e.InputTokens, e.OutputTokens = int64(100*i), 10
		e.ToolCalls = []domain.ToolUse{{Name: "get_finding", Count: 2, Errors: 1}}
		if err := st.RecordAudit(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}
	cnt, err := st.CountRequests(ctx, a, domain.StartOfDay(now))
	if err != nil || cnt != 2 {
		t.Fatalf("cuenta del día = %d %v", cnt, err)
	}
	if cnt, _ := st.CountRequests(ctx, b, domain.StartOfDay(now)); cnt != 0 {
		t.Fatalf("cuenta de B = %d", cnt)
	}
	page, err := st.ListAudit(ctx, a, 4, nil)
	if err != nil || len(page) != 4 || page[0].Feature != "mcp" || page[0].ToolCalls[0].Errors != 1 {
		t.Fatalf("página 1 = %+v %v", page, err)
	}
	rest, err := st.ListAudit(ctx, a, 10, &app.AuditCursor{CreatedAt: page[3].CreatedAt, ID: page[3].ID})
	if err != nil || len(rest) != 2 || rest[1].CreatedAt.After(rest[0].CreatedAt) {
		t.Fatalf("página 2 = %+v %v", rest, err)
	}
	if other, _ := st.ListAudit(ctx, b, 10, nil); len(other) != 0 {
		t.Fatalf("B ve la auditoría de A: %d", len(other))
	}

	// Tokens MCP: búsqueda por hash (plataforma), lista y revocación por tenant.
	hash := make([]byte, 32)
	hash[0] = 7
	tok := &domain.MCPToken{ID: uuid.Must(uuid.NewV7()), TenantID: a, UserID: user, Name: "Claude Desktop", Hash: hash, Prefix: "hmcp_abcd",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := st.CreateMCPToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	found, err := st.MCPTokenByHash(ctx, hash)
	if err != nil || found.ID != tok.ID || found.TenantID != a {
		t.Fatalf("por hash = %+v %v", found, err)
	}
	if _, err := st.MCPTokenByHash(ctx, make([]byte, 32)); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("hash desconocido: %v", err)
	}
	if err := st.TouchMCPToken(ctx, tok.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.ListMCPTokens(ctx, b); len(list) != 0 {
		t.Fatal("B ve los tokens de A")
	}
	if err := st.RevokeMCPToken(ctx, b, tok.ID, now); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("B revoca un token de A: %v", err)
	}
	if err := st.RevokeMCPToken(ctx, a, tok.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListMCPTokens(ctx, a)
	if err != nil || len(list) != 1 || list[0].RevokedAt == nil || list[0].LastUsedAt == nil {
		t.Fatalf("lista = %+v %v", list, err)
	}
}
