package librenms

import (
	"context"
	"strings"
	"time"
	"unicode"
)

// MaxLine es el largo máximo (en runas) de una línea de syslog de Horus.
const MaxLine = 900

// Event es una notificación de Horus para LibreNMS.
type Event struct {
	Severity   string // info | low | medium | high | critical
	EventType  string
	Subject    string
	Text       string
	Link       string
	DeliveryID string
}

func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.ReplaceAll(s, "|", "/")
	return strings.Join(strings.Fields(s), " ")
}

// Line es la línea de syslog:
// "HORUS <SEVERIDAD> <event_type> <asunto> | <texto> | <enlace> | id=<entrega>",
// en una sola línea y acotada a MaxLine runas (el id de la entrega siempre
// se conserva para identificar el duplicado de un reintento).
func Line(ev Event) string {
	sev := strings.ToUpper(oneLine(ev.Severity))
	if sev == "" {
		sev = "INFO"
	}
	typ := oneLine(ev.EventType)
	if typ == "" {
		typ = "event"
	}
	head := SyslogPrefix + " " + sev + " " + strings.ReplaceAll(typ, " ", "_") + " " + oneLine(ev.Subject)
	parts := []string{strings.TrimSpace(head)}
	if t := oneLine(ev.Text); t != "" {
		parts = append(parts, t)
	}
	if l := oneLine(ev.Link); l != "" {
		parts = append(parts, l)
	}
	tail := ""
	if ev.DeliveryID != "" {
		tail = " | id=" + oneLine(ev.DeliveryID)
	}
	line := strings.Join(parts, " | ")
	room := MaxLine - len([]rune(tail))
	if r := []rune(line); len(r) > room {
		line = string(r[:room-1]) + "…"
	}
	return line + tail
}

// syslogLevel traduce la severidad de Horus a nivel de syslog.
func syslogLevel(sev string) string {
	switch sev {
	case "critical":
		return "crit"
	case "high":
		return "err"
	case "medium":
		return "warning"
	case "low":
		return "notice"
	}
	return "info"
}

func eventSeverity(sev string) int {
	switch sev {
	case "critical", "high":
		return EventError
	case "medium":
		return EventWarning
	case "low":
		return EventNotice
	}
	return EventInfo
}

// HorusTarget es el dispositivo «Horus Flow» al que van las líneas.
type HorusTarget struct {
	Host     string
	DeviceID ID
	// OffsetMinutes es el desfase horario de LibreNMS respecto de UTC.
	OffsetMinutes int
}

// SendEvent manda la línea por POST /syslogsink (host = dispositivo Horus)
// y, como historial, la deja en el registro de eventos del dispositivo
// (best-effort: su fallo no falla la entrega). Devuelve la línea enviada.
func (c *Client) SendEvent(ctx context.Context, h HorusTarget, ev Event, now time.Time) (string, error) {
	line := Line(ev)
	entry := SyslogEntry{Host: h.Host, DeviceID: h.DeviceID, Msg: line, Program: "HORUS", Facility: "user",
		Priority: syslogLevel(ev.Severity), Level: syslogLevel(ev.Severity), Tag: "horus", Timestamp: SyslogTime(now, h.OffsetMinutes)}
	if err := c.SyslogSink(ctx, entry); err != nil {
		return line, err
	}
	_ = c.AddEventlog(ctx, h.DeviceID, line, eventSeverity(ev.Severity), "horus")
	return line, nil
}

// Pasos de la prueba de extremo a extremo.
const (
	StepAuth         = "auth"
	StepDevice       = "device"
	StepRule         = "rule"
	StepSyslogSend   = "syslog_send"
	StepSyslogStored = "syslog_stored"
	StepEventlog     = "eventlog"
)

// TestStep es un paso de la prueba.
type TestStep struct {
	Step   string `json:"step"`
	OK     bool   `json:"ok"`
	Code   string `json:"error_code,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// TestResult es el resultado de la prueba de extremo a extremo.
type TestResult struct {
	OK         bool
	FailedStep string
	Code       string
	Error      string
	Version    string
	Line       string
	Steps      []TestStep
}

func (r *TestResult) add(step string, err error, okDetail string) bool {
	st := TestStep{Step: step, OK: err == nil, Detail: okDetail}
	if err != nil {
		st.Code = CodeOf(err)
		st.Detail = err.Error()
		if e, ok := AsError(err); ok && e.Message != "" {
			st.Detail = e.Message
		}
		if r.FailedStep == "" {
			r.FailedStep, r.Code, r.Error = step, st.Code, st.Detail
		}
	}
	r.Steps = append(r.Steps, st)
	return err == nil
}

// TestOptions afina la espera de la línea en el syslog.
type TestOptions struct {
	Wait  time.Duration // por defecto 6 s
	Every time.Duration // por defecto 1 s
	Now   func() time.Time
}

// EndToEnd prueba la cadena completa: autenticación, dispositivo Horus,
// regla de eventos de Horus, envío de una línea de prueba por syslogsink y su
// llegada al syslog del dispositivo (GET /logs/syslog/:id). Indica el paso
// exacto que falla.
func (c *Client) EndToEnd(ctx context.Context, h HorusTarget, rule ID, ev Event, o TestOptions) TestResult {
	if o.Wait <= 0 {
		o.Wait = 6 * time.Second
	}
	if o.Every <= 0 {
		o.Every = time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	var r TestResult
	ver, err := c.System(ctx)
	if !r.add(StepAuth, err, ver) {
		return r
	}
	r.Version = ver
	devs, err := c.ListDevices(ctx)
	if err == nil {
		found := false
		for _, d := range devs {
			if d.ID == h.DeviceID {
				found = true
				break
			}
		}
		if !found {
			err = &Error{Op: "list_devices", Code: CodeDeviceMissing,
				Message: "El dispositivo «Horus Flow» ya no existe en LibreNMS: pulsa «Sincronizar» para recrearlo."}
		}
	}
	if !r.add(StepDevice, err, h.Host) {
		return r
	}
	rules, err := c.ListRules(ctx)
	if err == nil {
		found := false
		for _, x := range rules {
			if x.ID == rule {
				found = true
				break
			}
		}
		if !found {
			err = &Error{Op: "list_alert_rules", Code: CodeRuleMissing,
				Message: "La regla «Horus · Evento de seguridad Horus» ya no existe en LibreNMS: pulsa «Sincronizar»."}
		}
	}
	if !r.add(StepRule, err, "") {
		return r
	}
	line, err := c.SendEvent(ctx, h, ev, o.Now())
	r.Line = line
	if !r.add(StepSyslogSend, err, "") {
		return r
	}
	needle := "id=" + ev.DeliveryID
	deadline := o.Now().Add(o.Wait)
	var lastErr error
poll:
	for {
		lines, err := c.RecentSyslog(ctx, h.DeviceID, 25)
		lastErr = err
		if err == nil {
			for _, l := range lines {
				if strings.Contains(l.Text(), needle) {
					r.add(StepSyslogStored, nil, l.When())
					r.OK = true
					return r
				}
			}
		} else if e, ok := AsError(err); ok && (e.Code == CodeAuthFailed || e.Code == CodeForbidden) {
			break
		}
		if !o.Now().Add(o.Every).Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			lastErr = &Error{Op: "list_syslog", Code: CodeTimeout, cause: ctx.Err()}
			break poll
		case <-time.After(o.Every):
		}
	}
	if lastErr == nil || CodeOf(lastErr) == CodeNotFound {
		lastErr = &Error{Op: "list_syslog", Code: CodeSyslogNotStored,
			Message: "LibreNMS aceptó la línea pero no la guardó en el syslog de «Horus Flow»: activa el syslog (lnms config:set enable_syslog true) y comprueba que el host del dispositivo coincide."}
	}
	r.add(StepSyslogStored, lastErr, "")
	return r
}
