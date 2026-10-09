package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	tw "github.com/hcdestroyer/horus-flow/services/analytics/api/trafficwidgets"
	"github.com/hcdestroyer/horus-flow/services/detection/api/securitywidgets"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

// Widgets resuelve los widgets de seguridad de C9 (securitywidgets.Provider).
type Widgets struct{ svc *Service }

// Widgets devuelve el proveedor de widgets de seguridad.
func (s *Service) Widgets() *Widgets { return &Widgets{svc: s} }

var _ securitywidgets.Provider = (*Widgets)(nil)

// Types implementa tw.Provider.
func (w *Widgets) Types() []string { return slices.Clone(securitywidgets.Types) }

type widgetConfig struct {
	SiteIDs     []uuid.UUID `json:"site_ids"`
	MinSeverity string      `json:"min_severity"`
	States      []string    `json:"states"`
	Limit       int         `json:"limit"`
	N           int         `json:"n"`
	Range       string      `json:"range"`
	GroupBy     string      `json:"group_by"`
}

// watchService es la etiqueta del puerto vigilado (dim.watch_port).
var watchService = map[uint16]string{23: "telnet", 2323: "telnet-alt", 37215: "huawei-hg532", 52869: "realtek-upnp", 7547: "tr-069",
	5555: "adb", 445: "smb", 139: "netbios", 6667: "irc", 6697: "irc-tls", 3389: "rdp", 1433: "mssql", 8291: "winbox", 25: "smtp"}

// sites combina config, variable del dashboard y alcance del espectador
// (nil = todo el tenant).
func sites(cfg widgetConfig, req tw.Request) []uuid.UUID {
	var want []uuid.UUID
	want = append(want, cfg.SiteIDs...)
	want = append(want, req.SiteIDs...)
	if len(want) == 0 {
		if req.AllowedSites == nil {
			return nil
		}
		return slices.Clone(req.AllowedSites)
	}
	if req.AllowedSites == nil {
		return want
	}
	out := []uuid.UUID{}
	for _, id := range want {
		if slices.Contains(req.AllowedSites, id) {
			out = append(out, id)
		}
	}
	return out
}

func severitiesFrom(min string) []string {
	r := domain.SeverityRank(min)
	var out []string
	for _, s := range validSeverities {
		if domain.SeverityRank(s) >= r {
			out = append(out, s)
		}
	}
	return out
}

// maskIP enmascara una IP de cliente (10.20.0.••• / 2001:db8:1000:••••::), como analytics.
func maskIP(s string) string {
	if i := strings.LastIndexByte(s, '.'); i > 0 && !strings.Contains(s, ":") {
		return s[:i] + ".•••"
	}
	parts := strings.Split(strings.TrimSuffix(strings.SplitN(s, "/", 2)[0], "::"), ":")
	if len(parts) > 3 {
		parts = parts[:3]
	}
	return strings.Join(parts, ":") + ":••••::"
}

// Resolve implementa tw.Provider.
func (w *Widgets) Resolve(ctx context.Context, req tw.Request) (*tw.WidgetData, error) {
	if !slices.Contains(securitywidgets.Types, req.Type) {
		return nil, tw.ErrUnsupportedType
	}
	if req.TenantID == uuid.Nil {
		return nil, fmt.Errorf("%w: sin tenant", tw.ErrInvalidConfig)
	}
	var cfg widgetConfig
	if len(req.Config) > 0 && string(req.Config) != "null" {
		if err := json.Unmarshal(req.Config, &cfg); err != nil {
			return nil, fmt.Errorf("%w: %v", tw.ErrInvalidConfig, err)
		}
	}
	if cfg.MinSeverity != "" && domain.SeverityRank(cfg.MinSeverity) == 0 {
		return nil, fmt.Errorf("%w: min_severity", tw.ErrInvalidConfig)
	}
	now := w.svc.now().UTC()
	out := &tw.WidgetData{Meta: tw.Meta{WidgetType: req.Type, GeneratedAt: now}}
	sc := sites(cfg, req)
	t := pgdb.TenantID(req.TenantID)
	var err error
	switch req.Type {
	case "findings_summary", "botnet_signals", "security_by_node":
		err = w.summary(ctx, out, req.Type, t, sc, cfg, now)
	case "findings_feed":
		err = w.feed(ctx, out, t, sc, cfg, req.ShowPersonalData)
	case "findings_trend":
		err = w.trend(ctx, out, t, sc, cfg, req, now)
	case "watched_ports":
		err = w.watched(ctx, out, t, sc, cfg, req, now)
	}
	if err != nil {
		return nil, err
	}
	out.Meta.DataEndpointKind = out.Data.Kind
	return out, nil
}

func (w *Widgets) summary(ctx context.Context, out *tw.WidgetData, typ string, t pgdb.TenantID, sc []uuid.UUID, cfg widgetConfig, now time.Time) error {
	var c *postgres.SummaryCounts
	err := w.svc.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		c, err = postgres.Summary(ctx, tx, sc, now)
		return err
	})
	if err != nil {
		return err
	}
	switch typ {
	case "findings_summary":
		min := cfg.MinSeverity
		if min == "" {
			min = domain.SeverityLow
		}
		total := 0
		for sev, n := range c.OpenBySeverity {
			if domain.SeverityRank(sev) >= domain.SeverityRank(min) {
				total += n
			}
		}
		out.Data = tw.Data{Kind: "state", Values: map[string]any{"open_total": total, "open_by_severity": c.OpenBySeverity,
			"new_last_24h": c.NewLast24h, "affected_customers": c.Affected, "by_security_state": c.BySecurityState}}
	case "botnet_signals":
		out.Data = tw.Data{Kind: "state", Values: map[string]any{"by_signal": c.BySignal, "affected_customers": c.Affected}}
	default:
		n := cfg.N
		if n <= 0 || n > 10 {
			n = 10
		}
		rows := c.BySite
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Customers != rows[j].Customers {
				return rows[i].Customers > rows[j].Customers
			}
			return rows[i].OpenFindings > rows[j].OpenFindings
		})
		if len(rows) > n {
			rows = rows[:n]
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.SiteID)
		}
		names := w.siteNames(ctx, t, ids)
		out.Data = tw.Data{Kind: "table", Columns: []tw.Column{{Key: "site", Type: "string"}, {Key: "customers_with_signals", Type: "number"},
			{Key: "open_findings", Type: "number"}}, Rows: []map[string]any{}}
		for _, r := range rows {
			out.Data.Rows = append(out.Data.Rows, map[string]any{"site": names[r.SiteID], "customers_with_signals": r.Customers, "open_findings": r.OpenFindings})
		}
	}
	return nil
}

// siteNames resuelve nombres de nodo con dim.site; si no hay, el UUID.
func (w *Widgets) siteNames(ctx context.Context, t pgdb.TenantID, ids []uuid.UUID) map[uuid.UUID]string {
	out := map[uuid.UUID]string{}
	if w.svc.flows != nil {
		if got, err := w.svc.flows.SiteNames(ctx, t.UUID(), ids); err == nil {
			out = got
		}
	}
	for _, id := range ids {
		if out[id] == "" {
			out[id] = id.String()
		}
	}
	return out
}

func (w *Widgets) feed(ctx context.Context, out *tw.WidgetData, t pgdb.TenantID, sc []uuid.UUID, cfg widgetConfig, personal bool) error {
	limit := cfg.Limit
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	states := cfg.States
	if len(states) == 0 {
		states = []string{domain.StateOpen, domain.StateAcknowledged}
	}
	for _, st := range states {
		if st != domain.StateOpen && st != domain.StateAcknowledged {
			return fmt.Errorf("%w: states", tw.ErrInvalidConfig)
		}
	}
	min := cfg.MinSeverity
	if min == "" {
		min = domain.SeverityMedium
	}
	var rows []domain.Finding
	var secStates map[uuid.UUID]string
	err := w.svc.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		rows, err = postgres.List(ctx, tx, postgres.ListFilter{States: states, Severities: severitiesFrom(min), Sites: sc,
			Sort: "-last_seen_at", Limit: limit})
		if err != nil {
			return err
		}
		if len(rows) > limit {
			rows = rows[:limit]
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, f := range rows {
			ids = append(ids, f.CustomerID)
		}
		secStates, err = postgres.SecurityStates(ctx, tx, ids)
		return err
	})
	if err != nil {
		return err
	}
	siteIDs := make([]uuid.UUID, 0, len(rows))
	for _, f := range rows {
		siteIDs = append(siteIDs, f.SiteID)
	}
	names := w.siteNames(ctx, t, siteIDs)
	var aliases map[uuid.UUID]*string
	if personal {
		aliases = map[uuid.UUID]*string{}
		for i, v := range w.svc.customerViews(ctx, t, rows) {
			aliases[rows[i].ID] = v.Alias
		}
	}
	out.Meta.MaskedPersonalData = !personal
	out.Data = tw.Data{Kind: "table", Columns: []tw.Column{{Key: "id", Type: "string"}, {Key: "severity", Type: "string"}, {Key: "kind", Type: "string"},
		{Key: "summary", Type: "string"}, {Key: "customer_ip", Type: "ip", PersonalData: true}, {Key: "alias", Type: "string", PersonalData: true},
		{Key: "site", Type: "string"}, {Key: "security_state", Type: "string"}, {Key: "confidence", Type: "number"}, {Key: "last_seen_at", Type: "timestamp"}},
		Rows: []map[string]any{}}
	for _, f := range rows {
		ip := addrString(&f)
		var alias *string
		if personal {
			alias = aliases[f.ID]
		} else {
			ip = maskIP(ip)
		}
		st := secStates[f.CustomerID]
		if st == "" {
			st = domain.SecuritySuspected
		}
		out.Data.Rows = append(out.Data.Rows, map[string]any{"id": f.ID.String(), "severity": f.Severity, "kind": f.Kind, "summary": f.Summary.Text,
			"customer_ip": ip, "alias": alias, "site": names[f.SiteID], "security_state": st, "confidence": f.Confidence,
			"last_seen_at": ts(f.LastSeenAt)})
	}
	return nil
}

func (w *Widgets) trend(ctx context.Context, out *tw.WidgetData, t pgdb.TenantID, sc []uuid.UUID, cfg widgetConfig, req tw.Request, now time.Time) error {
	rng := cfg.Range
	if req.Range == "7d" || req.Range == "30d" {
		rng = req.Range
	}
	days := 7
	switch rng {
	case "", "7d":
	case "30d":
		days = 30
	default:
		return fmt.Errorf("%w: range", tw.ErrInvalidConfig)
	}
	bySeverity := cfg.GroupBy == "severity"
	if cfg.GroupBy != "" && cfg.GroupBy != "kind" && !bySeverity {
		return fmt.Errorf("%w: group_by", tw.ErrInvalidConfig)
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	from := today.AddDate(0, 0, -(days - 1))
	var rows []postgres.TrendRow
	err := w.svc.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		rows, err = postgres.Trend(ctx, tx, sc, from, bySeverity)
		return err
	})
	if err != nil {
		return err
	}
	counts := map[string]map[time.Time]int{}
	totals := map[string]int{}
	for _, r := range rows {
		if counts[r.Group] == nil {
			counts[r.Group] = map[time.Time]int{}
		}
		counts[r.Group][r.Day] += r.Count
		totals[r.Group] += r.Count
	}
	groups := make([]string, 0, len(counts))
	for g := range counts {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		if totals[groups[i]] != totals[groups[j]] {
			return totals[groups[i]] > totals[groups[j]]
		}
		return groups[i] < groups[j]
	})
	step := 86400
	to := today.AddDate(0, 0, 1)
	out.Meta.From, out.Meta.To, out.Meta.Step = &from, &to, &step
	out.Data = tw.Data{Kind: "series", Series: []tw.Series{}}
	for _, g := range groups {
		group := g
		pts := make([]tw.Point, 0, days)
		for d := 0; d < days; d++ {
			day := from.AddDate(0, 0, d)
			pts = append(pts, tw.Point{ts(day), counts[g][day]})
		}
		out.Data.Series = append(out.Data.Series, tw.Series{Metric: "findings_opened", Unit: "count", Group: &group, Points: pts})
	}
	return nil
}

func (w *Widgets) watched(ctx context.Context, out *tw.WidgetData, t pgdb.TenantID, sc []uuid.UUID, cfg widgetConfig, req tw.Request, now time.Time) error {
	rng := cfg.Range
	if req.Range != "" {
		rng = req.Range
	}
	if rng == "" {
		rng = "1h"
	}
	d, ok := ranges[rng]
	if !ok {
		return fmt.Errorf("%w: range", tw.ErrInvalidConfig)
	}
	if w.svc.flows == nil {
		return tw.ErrUnavailable
	}
	from := now.Add(-d)
	rows, err := w.svc.flows.WatchedPorts(ctx, t.UUID(), sc, from, now)
	if err != nil {
		return fmt.Errorf("%w: %v", tw.ErrUnavailable, err)
	}
	out.Meta.From, out.Meta.To = &from, &now
	out.Data = tw.Data{Kind: "table", Columns: []tw.Column{{Key: "port", Type: "number"}, {Key: "protocol", Type: "string"},
		{Key: "service", Type: "string"}, {Key: "customers", Type: "number"}, {Key: "flows", Type: "number"}}, Rows: []map[string]any{}}
	for _, r := range rows {
		proto := "tcp"
		if r.Protocol == 17 {
			proto = "udp"
		}
		svc := watchService[r.Port]
		if svc == "" {
			svc = fmt.Sprint(r.Port)
		}
		out.Data.Rows = append(out.Data.Rows, map[string]any{"port": int(r.Port), "protocol": proto, "service": svc,
			"customers": r.Customers, "flows": r.Flows})
	}
	return nil
}
