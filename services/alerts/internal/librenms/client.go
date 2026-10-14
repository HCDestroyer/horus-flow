// Package librenms es el cliente de la API v0 de LibreNMS que usa Horus para
// aprovisionar su integración (dispositivos, grupos estáticos, plantillas y
// reglas de alerta) y delegar el alertado en LibreNMS (D17): Horus deja sus
// eventos como líneas de syslog del dispositivo «Horus Flow» por
// POST /api/v0/syslogsink y una regla de LibreNMS las convierte en alertas que
// salen por los transportes de la instancia.
//
// Rutas y campos verificados contra la documentación oficial
// (doc/API/{Alerts,Devices,DeviceGroups,Logs}.md) y el código de
// includes/html/api_functions.inc.php (rama master y 25.10.0). Horus nunca
// toca objetos de LibreNMS que no creó (todo lo suyo lleva el prefijo
// «Horus · ») y nunca escribe en los routers.
package librenms

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Códigos de error (enum abierto LibreNmsErrorCode del contrato).
const (
	CodeDNSFailed          = "dns_failed"
	CodeConnectFailed      = "connect_failed"
	CodeTLSInvalid         = "tls_invalid"
	CodeTimeout            = "timeout"
	CodeAuthFailed         = "auth_failed"
	CodeForbidden          = "forbidden"
	CodeNotFound           = "not_found"
	CodeRejected           = "rejected"
	CodeRemoteError        = "remote_error"
	CodeUnexpected         = "unexpected_response"
	CodeMissingCredentials = "missing_credentials"
	CodeNoDefaultTransport = "no_default_transport"
	CodeNotProvisioned     = "librenms_not_provisioned"
	CodeDeviceMissing      = "device_missing"
	CodeRuleMissing        = "rule_missing"
	CodeSyslogNotStored    = "syslog_not_stored"
	maxMessageLen          = 240
	maxBody                = 4 << 20
)

// Error es un fallo de una llamada a LibreNMS ya clasificado. Message es el
// campo `message` de LibreNMS saneado (sin secretos ni saltos de línea).
type Error struct {
	Op      string
	Status  int
	Code    string
	Message string
	cause   error
}

func (e *Error) Error() string {
	s := "librenms " + e.Op + ": " + e.Code
	if e.Status != 0 {
		s += " (HTTP " + strconv.Itoa(e.Status) + ")"
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

func (e *Error) Unwrap() error { return e.cause }

// AsError extrae un *Error.
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// CodeOf devuelve el código de un error (connect_failed si no es *Error).
func CodeOf(err error) string {
	if e, ok := AsError(err); ok {
		return e.Code
	}
	return CodeConnectFailed
}

// Config es la conexión a una instancia (el canal librenms del ISP).
type Config struct {
	BaseURL  string
	Token    string // X-Auth-Token (preferido)
	Username string // autenticación básica si no hay token
	Password string
	// HTTPClient con el timeout y la verificación TLS del canal; si es nil
	// se crea uno con TLSVerify y Timeout.
	HTTPClient *http.Client
	TLSVerify  bool
	Timeout    time.Duration
}

// Client habla con la API v0.
type Client struct {
	base   string
	cfg    Config
	client *http.Client
}

// New crea el cliente.
func New(cfg Config) *Client {
	hc := cfg.HTTPClient
	if hc == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // transporte estándar
		if !cfg.TLSVerify {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // tls_verify=false explícito y auditado (D17)
		}
		hc = &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	return &Client{base: strings.TrimRight(cfg.BaseURL, "/") + "/api/v0", cfg: cfg, client: hc}
}

// ID es un identificador numérico de LibreNMS (llega como número o texto
// según la versión y el recurso).
type ID int64

// UnmarshalJSON acepta 12, "12" y null.
func (i *ID) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*i = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("librenms id %q: %w", s, err)
	}
	*i = ID(n)
	return nil
}

// sanitize deja el `message` remoto en una línea corta sin secretos.
func (c *Client) sanitize(s string) string {
	for _, secret := range []string{c.cfg.Token, c.cfg.Password} {
		if len(secret) >= 4 {
			s = strings.ReplaceAll(s, secret, "***")
		}
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxMessageLen {
		s = string(r[:maxMessageLen]) + "…"
	}
	return s
}

// remoteMessage extrae `message` (texto u objeto de validación) del cuerpo.
func (c *Client) remoteMessage(body []byte) string {
	var env struct {
		Message json.RawMessage `json:"message"`
	}
	if json.Unmarshal(body, &env) != nil || len(env.Message) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(env.Message, &s) == nil {
		return c.sanitize(s)
	}
	// Errores de validación de Laravel: {"campo": ["mensaje"]}.
	var m map[string][]string
	if json.Unmarshal(env.Message, &m) == nil {
		parts := make([]string, 0, len(m))
		for k, v := range m {
			parts = append(parts, k+": "+strings.Join(v, " "))
		}
		return c.sanitize(strings.Join(parts, "; "))
	}
	return c.sanitize(string(env.Message))
}

func netCode(err error) string {
	var dns *net.DNSError
	var cert x509.UnknownAuthorityError
	var host x509.HostnameError
	var inv x509.CertificateInvalidError
	var tlsErr *tls.CertificateVerificationError
	var nerr net.Error
	switch {
	case errors.As(err, &dns):
		return CodeDNSFailed
	case errors.As(err, &cert), errors.As(err, &host), errors.As(err, &inv), errors.As(err, &tlsErr):
		return CodeTLSInvalid
	case errors.As(err, &nerr) && nerr.Timeout(), errors.Is(err, context.DeadlineExceeded):
		return CodeTimeout
	}
	return CodeConnectFailed
}

func statusCode(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return CodeAuthFailed
	case status == http.StatusForbidden:
		return CodeForbidden
	case status == http.StatusNotFound:
		return CodeNotFound
	case status >= 400 && status < 500:
		return CodeRejected
	case status >= 500:
		return CodeRemoteError
	}
	return CodeUnexpected
}

// do envía la petición y decodifica la respuesta JSON en out (si no es nil).
// LibreNMS responde {"status":"ok"|"error","message":...}; un status "error"
// con HTTP 200 también es un fallo.
func (c *Client) do(ctx context.Context, op, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return &Error{Op: op, Code: CodeUnexpected, cause: err}
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return &Error{Op: op, Code: CodeConnectFailed, Message: "URL no válida", cause: err}
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.Token != "" {
		req.Header.Set("X-Auth-Token", c.cfg.Token)
	} else {
		req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return &Error{Op: op, Code: netCode(err), cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return &Error{Op: op, Status: resp.StatusCode, Code: netCode(err), cause: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &Error{Op: op, Status: resp.StatusCode, Code: statusCode(resp.StatusCode), Message: c.remoteMessage(raw)}
	}
	var st struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(raw, &st) != nil {
		return &Error{Op: op, Status: resp.StatusCode, Code: CodeUnexpected, Message: "respuesta no JSON"}
	}
	if st.Status == "error" {
		return &Error{Op: op, Status: resp.StatusCode, Code: CodeRejected, Message: c.remoteMessage(raw)}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return &Error{Op: op, Status: resp.StatusCode, Code: CodeUnexpected, Message: "respuesta inesperada", cause: err}
		}
	}
	return nil
}

// ---------------------------------------------------------------- sistema

// System es GET /system: versión de la instancia (comprueba alcance y token).
func (c *Client) System(ctx context.Context) (string, error) {
	var out struct {
		System []struct {
			LocalVer string `json:"local_ver"`
		} `json:"system"`
	}
	if err := c.do(ctx, "system", http.MethodGet, "/system", nil, &out); err != nil {
		return "", err
	}
	if len(out.System) == 0 {
		return "", nil
	}
	return out.System[0].LocalVer, nil
}

// ---------------------------------------------------------------- dispositivos

// Device es lo que Horus lee de un dispositivo.
type Device struct {
	ID       ID     `json:"device_id"`
	Hostname string `json:"hostname"`
	SysName  string `json:"sysName"`
	IP       string `json:"ip"`
	Display  string `json:"display"`
	OS       string `json:"os"`
}

// ListDevices es GET /devices.
func (c *Client) ListDevices(ctx context.Context) ([]Device, error) {
	var out struct {
		Devices []Device `json:"devices"`
	}
	if err := c.do(ctx, "list_devices", http.MethodGet, "/devices", nil, &out); err != nil {
		return nil, err
	}
	return out.Devices, nil
}

// SNMPv3 son las credenciales v3 en el formato de add_device.
type SNMPv3 struct {
	AuthLevel  string `json:"authlevel"` // noAuthNoPriv | authNoPriv | authPriv
	AuthName   string `json:"authname"`
	AuthPass   string `json:"authpass,omitempty"`
	AuthAlgo   string `json:"authalgo,omitempty"` // MD5 | SHA | SHA-224 | SHA-256 | SHA-384 | SHA-512
	CryptoPass string `json:"cryptopass,omitempty"`
	CryptoAlgo string `json:"cryptoalgo,omitempty"` // AES | DES
}

// NewDevice es el cuerpo de add_device. Sin SNMP (ICMP) → snmp_disable + os
// ping; con v2c, community; con v3, las credenciales v3.
type NewDevice struct {
	Hostname  string
	Display   string
	Location  string
	Community string
	V3        *SNMPv3
	ForceAdd  bool
}

// AddDevice es POST /devices; devuelve el dispositivo creado.
func (c *Client) AddDevice(ctx context.Context, d NewDevice) (Device, error) {
	body := map[string]any{"hostname": d.Hostname}
	if d.Display != "" {
		// display_template (master) y display (versiones anteriores).
		body["display_template"] = d.Display
		body["display"] = d.Display
	}
	if d.Location != "" {
		body["location"] = d.Location
	}
	switch {
	case d.V3 != nil:
		body["snmpver"] = "v3"
		b, _ := json.Marshal(d.V3)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		for k, v := range m {
			body[k] = v
		}
	case d.Community != "":
		body["snmpver"] = "v2c"
		body["community"] = d.Community
	default:
		body["snmp_disable"] = true
		body["os"] = "ping"
	}
	if d.ForceAdd {
		body["force_add"] = true
	}
	var out struct {
		Devices []Device `json:"devices"`
	}
	if err := c.do(ctx, "add_device", http.MethodPost, "/devices", body, &out); err != nil {
		return Device{}, err
	}
	if len(out.Devices) == 0 || out.Devices[0].ID == 0 {
		return Device{}, &Error{Op: "add_device", Code: CodeUnexpected, Message: "LibreNMS no devolvió el dispositivo creado"}
	}
	return out.Devices[0], nil
}

// DeleteDevice es DELETE /devices/:id (por id numérico, nunca por IP).
func (c *Client) DeleteDevice(ctx context.Context, id ID) error {
	return c.do(ctx, "del_device", http.MethodDelete, "/devices/"+strconv.FormatInt(int64(id), 10), nil, nil)
}

// Severidades de add_eventlog (LibreNMS\Enum\Severity).
const (
	EventOk      = 1
	EventInfo    = 2
	EventNotice  = 3
	EventWarning = 4
	EventError   = 5
)

// AddEventlog es POST /devices/:id/eventlog.
func (c *Client) AddEventlog(ctx context.Context, device ID, text string, severity int, typ string) error {
	body := map[string]any{"text": text, "severity": strconv.Itoa(severity), "type": typ}
	return c.do(ctx, "add_eventlog", http.MethodPost, "/devices/"+strconv.FormatInt(int64(device), 10)+"/eventlog", body, nil)
}

// ---------------------------------------------------------------- grupos

// Group es un grupo de dispositivos.
type Group struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// ListDeviceGroups es GET /devicegroups (404 «No device groups found» = vacío).
func (c *Client) ListDeviceGroups(ctx context.Context) ([]Group, error) {
	var out struct {
		Groups []Group `json:"groups"`
	}
	if err := c.do(ctx, "get_device_groups", http.MethodGet, "/devicegroups", nil, &out); err != nil {
		if e, ok := AsError(err); ok && e.Status == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	return out.Groups, nil
}

// AddStaticGroup es POST /devicegroups (type static); devuelve el id.
func (c *Client) AddStaticGroup(ctx context.Context, name, desc string, devices []ID) (ID, error) {
	if devices == nil {
		devices = []ID{}
	}
	var out struct {
		ID ID `json:"id"`
	}
	if err := c.do(ctx, "add_device_group", http.MethodPost, "/devicegroups",
		map[string]any{"name": name, "type": "static", "desc": desc, "devices": devices}, &out); err != nil {
		return 0, err
	}
	if out.ID == 0 {
		return 0, &Error{Op: "add_device_group", Code: CodeUnexpected, Message: "LibreNMS no devolvió el id del grupo"}
	}
	return out.ID, nil
}

// AddGroupDevices es POST /devicegroups/:id/devices (sin desasociar los demás).
func (c *Client) AddGroupDevices(ctx context.Context, group ID, devices []ID) error {
	if len(devices) == 0 {
		return nil
	}
	return c.do(ctx, "update_device_group_add_devices", http.MethodPost, "/devicegroups/"+strconv.FormatInt(int64(group), 10)+"/devices",
		map[string]any{"devices": devices}, nil)
}

// RemoveGroupDevices es DELETE /devicegroups/:id/devices.
func (c *Client) RemoveGroupDevices(ctx context.Context, group ID, devices []ID) error {
	if len(devices) == 0 {
		return nil
	}
	return c.do(ctx, "update_device_group_remove_devices", http.MethodDelete, "/devicegroups/"+strconv.FormatInt(int64(group), 10)+"/devices",
		map[string]any{"devices": devices}, nil)
}

// ---------------------------------------------------------------- reglas

// Rule es lo que Horus lee de una regla de alerta. HasOperation indica que
// la instancia usa el modelo de «operaciones» (alert_operation_id, master).
type Rule struct {
	ID               ID     `json:"id"`
	Name             string `json:"name"`
	AlertOperationID *ID    `json:"alert_operation_id"`
	HasOperation     bool   `json:"-"`
}

// ListRules es GET /rules.
func (c *Client) ListRules(ctx context.Context) ([]Rule, error) {
	var out struct {
		Rules []json.RawMessage `json:"rules"`
	}
	if err := c.do(ctx, "list_alert_rules", http.MethodGet, "/rules", nil, &out); err != nil {
		return nil, err
	}
	rules := make([]Rule, 0, len(out.Rules))
	for _, raw := range out.Rules {
		var r Rule
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, &Error{Op: "list_alert_rules", Code: CodeUnexpected, Message: "regla ilegible", cause: err}
		}
		var keys map[string]json.RawMessage
		_ = json.Unmarshal(raw, &keys)
		_, r.HasOperation = keys["alert_operation_id"]
		rules = append(rules, r)
	}
	return rules, nil
}

// Builder es el JSON del query builder de LibreNMS (jQuery QueryBuilder).
type Builder struct {
	Condition string        `json:"condition"`
	Rules     []BuilderRule `json:"rules"`
	Valid     bool          `json:"valid"`
}

// BuilderRule es una condición del builder.
type BuilderRule struct {
	ID       string `json:"id"`
	Field    string `json:"field"`
	Type     string `json:"type"`
	Input    string `json:"input"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

// Cond construye una condición (id = field).
func Cond(field, typ, input, op, value string) BuilderRule {
	return BuilderRule{ID: field, Field: field, Type: typ, Input: input, Operator: op, Value: value}
}

// All es un builder AND válido.
func All(rules ...BuilderRule) Builder { return Builder{Condition: "AND", Rules: rules, Valid: true} }

// RuleInput es el cuerpo de add_rule / edit_rule.
type RuleInput struct {
	Name     string
	Devices  []ID // vacío = sin dispositivos (se envía -1 si tampoco hay grupos)
	Groups   []ID
	Builder  Builder
	Severity string // ok | warning | critical
	Notes    string
	Proc     string
	// Count: máximo de avisos por alerta (-1 = sin límite); Delay e
	// Interval en el formato de LibreNMS ("5m"). Son los campos «legacy»:
	// en las versiones con operaciones (master) LibreNMS crea con ellos una
	// operación con los transportes marcados por defecto.
	Count    int
	Delay    string
	Interval string
	Recovery bool
	// KeepOperation: id de operación existente a conservar al editar (si
	// la instancia usa operaciones y la regla ya tiene una).
	KeepOperation *ID
}

func (r RuleInput) body(ruleID ID) map[string]any {
	devices := make([]any, 0, len(r.Devices))
	for _, d := range r.Devices {
		devices = append(devices, d)
	}
	if len(devices) == 0 {
		// add_rule exige devices; -1 se descarta y la regla queda ligada
		// solo a sus grupos.
		devices = append(devices, -1)
	}
	groups := r.Groups
	if groups == nil {
		groups = []ID{}
	}
	b := map[string]any{
		"name": r.Name, "devices": devices, "groups": groups, "locations": []ID{}, "builder": r.Builder,
		"severity": r.Severity, "disabled": 0, "notes": r.Notes, "override_query": false, "adv_query": "",
		"invert": false, "recovery": r.Recovery,
	}
	if r.Proc != "" {
		b["proc"] = r.Proc
	}
	if r.KeepOperation != nil {
		b["alert_operation_id"] = *r.KeepOperation
	} else {
		b["count"], b["delay"], b["interval"], b["mute"] = r.Count, r.Delay, r.Interval, false
	}
	if ruleID != 0 {
		b["rule_id"] = ruleID
	}
	return b
}

// AddRule es POST /rules. LibreNMS no devuelve el id: se busca por nombre.
func (c *Client) AddRule(ctx context.Context, r RuleInput) error {
	return c.do(ctx, "add_rule", http.MethodPost, "/rules", r.body(0), nil)
}

// EditRule es PUT /rules con rule_id.
func (c *Client) EditRule(ctx context.Context, id ID, r RuleInput) error {
	return c.do(ctx, "edit_rule", http.MethodPut, "/rules", r.body(id), nil)
}

// DeleteRule es DELETE /rules/:id.
func (c *Client) DeleteRule(ctx context.Context, id ID) error {
	return c.do(ctx, "delete_rule", http.MethodDelete, "/rules/"+strconv.FormatInt(int64(id), 10), nil, nil)
}

// ---------------------------------------------------------------- plantillas

// Template es una plantilla de alerta.
type Template struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
}

// ListTemplates es GET /alert_templates.
func (c *Client) ListTemplates(ctx context.Context) ([]Template, error) {
	var out struct {
		Templates []Template `json:"alert_templates"`
	}
	if err := c.do(ctx, "list_alert_templates", http.MethodGet, "/alert_templates", nil, &out); err != nil {
		return nil, err
	}
	return out.Templates, nil
}

// TemplateInput es una plantilla Blade con título de alerta y de recuperación.
type TemplateInput struct {
	Name     string
	Template string
	Title    string
	TitleRec string
	Rules    []ID
}

// SaveTemplate crea (id = 0, POST) o edita (PUT con template_id) una
// plantilla y la asocia a sus reglas; devuelve el id.
func (c *Client) SaveTemplate(ctx context.Context, id ID, t TemplateInput) (ID, error) {
	rules := t.Rules
	if rules == nil {
		rules = []ID{}
	}
	body := map[string]any{"name": t.Name, "template": t.Template, "title": t.Title, "title_rec": t.TitleRec, "alert_rules": rules}
	method, op := http.MethodPost, "add_alert_template"
	if id != 0 {
		body["template_id"] = id
		method, op = http.MethodPut, "edit_alert_template"
	}
	var out struct {
		ID ID `json:"id"`
	}
	if err := c.do(ctx, op, method, "/alert_templates", body, &out); err != nil {
		return 0, err
	}
	if out.ID == 0 {
		out.ID = id
	}
	if out.ID == 0 {
		return 0, &Error{Op: op, Code: CodeUnexpected, Message: "LibreNMS no devolvió el id de la plantilla"}
	}
	return out.ID, nil
}

// ---------------------------------------------------------------- syslog y registros

// SyslogEntry es una línea para POST /syslogsink.
type SyslogEntry struct {
	Host      string `json:"host"`
	DeviceID  ID     `json:"device_id,omitempty"`
	Msg       string `json:"msg"`
	Program   string `json:"program"`
	Facility  string `json:"facility"`
	Priority  string `json:"priority"`
	Level     string `json:"level"`
	Tag       string `json:"tag"`
	Timestamp string `json:"timestamp"` // "2006-01-02 15:04:05" en la zona horaria de LibreNMS
}

// SyslogSink es POST /syslogsink (una o varias líneas). LibreNMS las asocia
// al dispositivo por host (hostname, sysName o IP); si no lo encuentra las
// descarta sin error.
func (c *Client) SyslogSink(ctx context.Context, entries ...SyslogEntry) error {
	var body any = entries
	if len(entries) == 1 {
		body = entries[0]
	}
	return c.do(ctx, "post_syslogsink", http.MethodPost, "/syslogsink", body, nil)
}

// LogLine es una entrada de GET /logs/{syslog|eventlog}/:id.
type LogLine struct {
	Msg      string `json:"msg"`
	Message  string `json:"message"`
	Program  string `json:"program"`
	Time     string `json:"timestamp"`
	Datetime string `json:"datetime"`
}

// Text devuelve el texto de la línea (msg en syslog, message en eventlog).
func (l LogLine) Text() string {
	if l.Msg != "" {
		return l.Msg
	}
	return l.Message
}

// When devuelve la marca de tiempo (timestamp en syslog, datetime en eventlog).
func (l LogLine) When() string {
	if l.Time != "" {
		return l.Time
	}
	return l.Datetime
}

func (c *Client) logs(ctx context.Context, kind string, device ID, limit int) ([]LogLine, error) {
	var out struct {
		Logs []LogLine `json:"logs"`
	}
	q := url.Values{"sortorder": {"DESC"}, "limit": {strconv.Itoa(limit)}}
	if err := c.do(ctx, "list_"+kind, http.MethodGet, "/logs/"+kind+"/"+strconv.FormatInt(int64(device), 10)+"?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out.Logs, nil
}

// RecentSyslog es GET /logs/syslog/:id (las más recientes primero).
func (c *Client) RecentSyslog(ctx context.Context, device ID, limit int) ([]LogLine, error) {
	return c.logs(ctx, "syslog", device, limit)
}

// RecentEventlog es GET /logs/eventlog/:id (las más recientes primero).
func (c *Client) RecentEventlog(ctx context.Context, device ID, limit int) ([]LogLine, error) {
	return c.logs(ctx, "eventlog", device, limit)
}
