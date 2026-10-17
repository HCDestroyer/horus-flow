package routeros

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
)

// NetwatchProps son las propiedades leídas de /tool/netwatch (RouterOS v7;
// las estadísticas ICMP existen desde 7.4, nombres a verificar en cada
// versión soportada: docs/vendors/mikrotik.md §4.1).
var NetwatchProps = []string{"host", "type", "status", "comment", "disabled", "sent-count", "response-count", "loss-percent",
	"rtt-avg", "rtt-min", "rtt-max", "rtt-jitter", "rtt-stdev"}

var _ app.NetwatchReader = Reader{}

// ReadNetwatch implementa app.NetwatchReader: un GET de /tool/netwatch con la
// huella fijada (sin TOFU: una huella vacía o distinta es un error).
func (r Reader) ReadNetwatch(ctx context.Context, t app.ReadTarget) ([]devapi.NetwatchProbe, error) {
	if t.Pinned == "" {
		return nil, app.ErrRouterFingerprint
	}
	base := "https://" + t.TunnelIP
	if r.BaseURL != nil {
		base = r.BaseURL(t.TunnelIP)
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	c := New(Target{BaseURL: base, User: t.User, Password: t.Password, Pinned: t.Pinned}, timeout, r.Dial)
	rows, err := c.Print(ctx, "tool/netwatch", NetwatchProps...)
	switch {
	case errors.Is(err, ErrFingerprintChanged):
		return nil, app.ErrRouterFingerprint
	case errors.Is(err, ErrAuth):
		return nil, app.ErrRouterAuth
	case err != nil:
		return nil, app.ErrRouterUnreachable
	}
	out := make([]devapi.NetwatchProbe, 0, len(rows))
	for _, row := range rows {
		out = append(out, ParseNetwatch(row))
	}
	return out, nil
}

// ParseNetwatch convierte una fila de /tool/netwatch.
func ParseNetwatch(row Row) devapi.NetwatchProbe {
	p := devapi.NetwatchProbe{Host: row["host"], Type: row["type"], Status: row["status"], Comment: row["comment"],
		Disabled: truthy(row["disabled"])}
	if p.Type == "" {
		p.Type = "simple"
	}
	p.SentCount, p.ResponseCount = atoiPtr(row["sent-count"]), atoiPtr(row["response-count"])
	if v, ok := parsePercent(row["loss-percent"]); ok {
		p.LossPercent = &v
	} else if p.SentCount != nil && p.ResponseCount != nil && *p.SentCount > 0 {
		v := float64(*p.SentCount-*p.ResponseCount) / float64(*p.SentCount) * 100
		p.LossPercent = &v
	}
	p.RTTAvgMs, p.RTTMinMs, p.RTTMaxMs = durMs(row["rtt-avg"]), durMs(row["rtt-min"]), durMs(row["rtt-max"])
	p.RTTJitterMs, p.RTTStdevMs = durMs(row["rtt-jitter"]), durMs(row["rtt-stdev"])
	return p
}

func atoiPtr(s string) *int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return nil
	}
	return &v
}

func parsePercent(s string) (float64, bool) {
	s = strings.TrimSuffix(strings.TrimSpace(s), "%")
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil && v >= 0 && v <= 100
}

// durMs interpreta una duración de RouterOS en milisegundos: "1ms487us",
// "12ms", "1s200ms", "350us", "00:00:01.234" o "1.5ms". nil si no se entiende.
func durMs(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		if len(parts) != 3 {
			return nil
		}
		h, err1 := strconv.Atoi(parts[0])
		m, err2 := strconv.Atoi(parts[1])
		sec, err3 := strconv.ParseFloat(parts[2], 64)
		if err1 != nil || err2 != nil || err3 != nil {
			return nil
		}
		v := (float64(h)*3600 + float64(m)*60 + sec) * 1000
		return &v
	}
	units := map[string]float64{"w": 7 * 86400e3, "d": 86400e3, "h": 3600e3, "m": 60e3, "s": 1e3, "ms": 1, "us": 1e-3, "ns": 1e-6}
	var total float64
	i := 0
	for i < len(s) {
		j := i
		for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
			j++
		}
		if j == i {
			return nil
		}
		num, err := strconv.ParseFloat(s[i:j], 64)
		if err != nil {
			return nil
		}
		k := j
		for k < len(s) && s[k] >= 'a' && s[k] <= 'z' {
			k++
		}
		unit := s[j:k]
		if unit == "" {
			unit = "s"
		}
		f, ok := units[unit]
		if !ok {
			return nil
		}
		total += num * f
		i = k
	}
	return &total
}
