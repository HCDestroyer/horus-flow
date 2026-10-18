//go:build integration

package clickhouse_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/chmigrate"
	"github.com/hcdestroyer/horus-flow/packages/go/chtest"
	ingesterschema "github.com/hcdestroyer/horus-flow/services/ingester/api/chschema"
	chadapter "github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/clickhouse"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// TestSwitchTop comprueba contra un ClickHouse real el top de muestras sFlow de un switch
// (flows.switch_samples, fuente secundaria) con el usuario horus_snmp: por IP de origen y de destino y por
// protocolo/puerto, filtro por puerto de entrada, aislamiento por tenant (p_tenant) y que la tabla no alimenta
// ningún agregado de flows (customer_*, site_*: sin doble conteo).
func TestSwitchTop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	srv := chtest.Start(t, func(ctx context.Context, dsn, pw string) error {
		if err := ingesterschema.MigrateClickHouse(ctx, []string{"HORUS_CLICKHOUSE_DSN=" + dsn, "HORUS_CLICKHOUSE_PASSWORD=" + pw},
			slog.New(slog.DiscardHandler)); err != nil {
			return err
		}
		return chadapter.Migrate(ctx, dsn, pw, capSNMPPassword, nil)
	})
	admin, err := chmigrate.Open(srv.DSN, srv.Password)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	c, err := chadapter.OpenCapacity(srv.DSN, chadapter.UserSNMP, capSNMPPassword)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	tenantA, tenantB, site, sw := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Second)
	ins := `INSERT INTO flows.switch_samples (tenant_id, site_id, switch_id, ts, input_if_index, output_if_index, src_ip, dst_ip, protocol,
		src_port, dst_port, bytes, packets, sampling_rate, batch_id) VALUES (?, ?, ?, ?, ?, 0, toIPv6(?), toIPv6(?), ?, ?, ?, ?, ?, 4096, ?)`
	b := uuid.New()
	mustExec(t, admin, ins, tenantA, site, sw, now.Add(-10*time.Minute), 54, "::ffff:10.20.1.9", "::ffff:8.8.8.8", 6, 40000, 443, 8_000_000, 4096, b)
	mustExec(t, admin, ins, tenantA, site, sw, now.Add(-9*time.Minute), 54, "::ffff:10.20.1.9", "::ffff:1.1.1.1", 17, 40001, 53, 2_000_000, 4096, b)
	mustExec(t, admin, ins, tenantA, site, sw, now.Add(-8*time.Minute), 6, "::ffff:10.20.1.7", "::ffff:8.8.8.8", 6, 40002, 443, 1_000_000, 4096, b)
	mustExec(t, admin, ins, tenantB, site, sw, now.Add(-8*time.Minute), 54, "::ffff:10.99.0.1", "::ffff:8.8.8.8", 6, 1, 443, 9_000_000, 4096, b)

	from, to := now.Add(-time.Hour), now.Add(time.Minute)
	rows, total, samples, err := c.SwitchTop(ctx, tenantA, sw, 0, domain.SwitchTopSrc, from, to, 10)
	if err != nil || total != 11_000_000 || samples != 3 || len(rows) != 2 || rows[0].Key != "10.20.1.9" || rows[0].Bytes != 10_000_000 {
		t.Fatalf("top por origen: %+v %d %d %v", rows, total, samples, err)
	}
	rows, total, _, err = c.SwitchTop(ctx, tenantA, sw, 54, domain.SwitchTopProtocol, from, to, 10)
	if err != nil || total != 10_000_000 || len(rows) != 2 || rows[0].Key != "6/443" || rows[1].Key != "17/53" {
		t.Fatalf("top por protocolo del puerto 54: %+v %v", rows, err)
	}
	if rows, _, _, err := c.SwitchTop(ctx, tenantA, sw, 0, domain.SwitchTopDst, from, to, 1); err != nil || len(rows) != 1 || rows[0].Key != "8.8.8.8" {
		t.Fatalf("top por destino: %+v %v", rows, err)
	}
	// Otro tenant con el mismo switch_id (imposible en la práctica): solo ve lo suyo.
	if rows, total, _, err := c.SwitchTop(ctx, tenantB, sw, 0, domain.SwitchTopSrc, from, to, 10); err != nil || total != 9_000_000 || len(rows) != 1 {
		t.Fatalf("aislamiento: %+v %d %v", rows, total, err)
	}
	// Sin doble conteo: ninguna vista materializada lee flows.switch_samples.
	var mv uint64
	if err := admin.QueryRowContext(ctx, `SELECT count() FROM system.tables WHERE engine = 'MaterializedView'
		AND has(dependencies_table, 'switch_samples')`).Scan(&mv); err != nil || mv != 0 {
		t.Fatalf("vistas sobre switch_samples: %d %v", mv, err)
	}
	var site5m uint64
	if err := admin.QueryRowContext(ctx, `SELECT count() FROM flows.site_5m WHERE tenant_id IN (?, ?)`, tenantA, tenantB).Scan(&site5m); err != nil || site5m != 0 {
		t.Fatalf("site_5m alimentado por muestras de switch: %d %v", site5m, err)
	}
}
