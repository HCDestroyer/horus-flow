// Package librenmsfake es un LibreNMS falso (API v0) para las pruebas: valida
// la forma de cada petición contra la documentación oficial
// (doc/API/*.md) y el código de includes/html/api_functions.inc.php, y
// responde como la instancia real (incluidas sus rarezas: ids como texto,
// add_rule sin id, 404 en un listado de grupos vacío). No usa la imagen
// Docker de LibreNMS.
package librenmsfake

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Device es un dispositivo del falso.
type Device struct {
	ID          int
	Hostname    string
	Display     string
	SNMPDisable bool
	OS          string
	SNMPVer     string
	Community   string
	AuthName    string
	ForceAdd    bool
}

// Rule es una regla del falso.
type Rule struct {
	ID          int
	Name        string
	Devices     []int
	Groups      []int
	Builder     map[string]any
	Severity    string
	OperationID *int
	Body        map[string]any
}

// Template es una plantilla del falso.
type Template struct {
	ID       int
	Name     string
	Template string
	Title    string
	TitleRec string
	Rules    []int
}

// Syslog es una línea guardada.
type Syslog struct {
	DeviceID  int
	Msg       string
	Timestamp string
	Program   string
}

// Server es el LibreNMS falso.
type Server struct {
	*httptest.Server
	Token string
	// SyslogEnabled: false descarta las líneas (instancia sin syslog).
	SyslogEnabled bool
	// Operations: modelo de operaciones de master (alert_operation_id);
	// DefaultTransports: hay transportes marcados por defecto.
	Operations        bool
	DefaultTransports bool
	// Offset es el desfase horario de la instancia (hora local = UTC + Offset).
	Offset time.Duration
	// Unreachable: hosts que add_device sin force_add rechaza.
	Unreachable map[string]bool

	mu        sync.Mutex
	nextID    int
	Devices   map[int]*Device
	Groups    map[int]*Group
	Rules     map[int]*Rule
	Templates map[int]*Template
	Syslogs   []Syslog
	Events    []Syslog
	Requests  []string
	nextOp    int
	t         testing.TB
}

// Group es un grupo del falso.
type Group struct {
	ID      int
	Name    string
	Type    string
	Desc    string
	Devices []int
}

// New arranca el falso.
func New(t testing.TB, token string) *Server {
	s := &Server{Token: token, SyslogEnabled: true, DefaultTransports: true, nextID: 1, Devices: map[int]*Device{},
		Groups: map[int]*Group{}, Rules: map[int]*Rule{}, Templates: map[int]*Template{}, Unreachable: map[string]bool{}, t: t}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

func (s *Server) id() int {
	s.nextID++
	return s.nextID
}

func write(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func apiErr(w http.ResponseWriter, code int, msg any) {
	write(w, code, map[string]any{"status": "error", "message": msg})
}

func ok(w http.ResponseWriter, extra map[string]any) {
	out := map[string]any{"status": "ok"}
	for k, v := range extra {
		out[k] = v
	}
	write(w, http.StatusOK, out)
}

// Count devuelve cuántas peticiones "MÉTODO /ruta" llegaron.
func (s *Server) Count(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.Requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

// Lock/Unlock protegen el acceso directo a los mapas desde los tests.
func (s *Server) Lock()   { s.mu.Lock() }
func (s *Server) Unlock() { s.mu.Unlock() }

func intsOf(v any) ([]int, bool) {
	arr, okArr := v.([]any)
	if !okArr {
		return nil, false
	}
	out := []int{}
	for _, x := range arr {
		switch n := x.(type) {
		case float64:
			out = append(out, int(n))
		case string:
			i, err := strconv.Atoi(n)
			if err != nil {
				return nil, false
			}
			out = append(out, i)
		default:
			return nil, false
		}
	}
	return out, true
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests = append(s.Requests, r.Method+" "+r.URL.Path)
	if r.Header.Get("X-Auth-Token") != s.Token {
		apiErr(w, http.StatusUnauthorized, "Unauthenticated.")
		return
	}
	var body map[string]any
	var list []any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			if err := json.Unmarshal(raw, &list); err != nil {
				apiErr(w, http.StatusBadRequest, "Not valid json")
				return
			}
		}
		if r.Header.Get("Content-Type") != "application/json" {
			apiErr(w, http.StatusBadRequest, "Content-Type debe ser application/json")
			return
		}
	}
	p := strings.TrimPrefix(r.URL.Path, "/api/v0")
	seg := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case r.Method == http.MethodGet && p == "/system":
		ok(w, map[string]any{"system": []any{map[string]any{"local_ver": "25.10.0", "db_schema": "x"}}, "count": 1})
	case p == "/devices" && r.Method == http.MethodGet:
		s.listDevices(w)
	case p == "/devices" && r.Method == http.MethodPost:
		s.addDevice(w, body)
	case len(seg) == 2 && seg[0] == "devices" && r.Method == http.MethodDelete:
		s.delDevice(w, seg[1])
	case len(seg) == 3 && seg[0] == "devices" && seg[2] == "eventlog" && r.Method == http.MethodPost:
		s.addEventlog(w, seg[1], body)
	case p == "/devicegroups" && r.Method == http.MethodGet:
		s.listGroups(w)
	case p == "/devicegroups" && r.Method == http.MethodPost:
		s.addGroup(w, body)
	case len(seg) == 3 && seg[0] == "devicegroups" && seg[2] == "devices":
		s.groupDevices(w, r.Method, seg[1], body)
	case p == "/rules" && r.Method == http.MethodGet:
		s.listRules(w)
	case p == "/rules" && (r.Method == http.MethodPost || r.Method == http.MethodPut):
		s.addEditRule(w, r.Method, body)
	case len(seg) == 2 && seg[0] == "rules" && r.Method == http.MethodDelete:
		id, _ := strconv.Atoi(seg[1])
		delete(s.Rules, id)
		ok(w, map[string]any{"message": "Alert rule has been removed"})
	case p == "/alert_templates" && r.Method == http.MethodGet:
		s.listTemplates(w)
	case p == "/alert_templates" && (r.Method == http.MethodPost || r.Method == http.MethodPut):
		s.saveTemplate(w, body)
	case p == "/syslogsink" && r.Method == http.MethodPost:
		s.syslogsink(w, body, list)
	case len(seg) == 3 && seg[0] == "logs" && (seg[1] == "syslog" || seg[1] == "eventlog") && r.Method == http.MethodGet:
		s.logs(w, r, seg[1], seg[2])
	default:
		apiErr(w, http.StatusNotFound, "This API route doesn't exist.")
	}
}

func (s *Server) deviceJSON(d *Device) map[string]any {
	// device_id como texto, como en la documentación de list_devices.
	return map[string]any{"device_id": strconv.Itoa(d.ID), "hostname": d.Hostname, "sysName": "", "ip": "", "display": d.Display, "os": d.OS}
}

func (s *Server) listDevices(w http.ResponseWriter) {
	ids := make([]int, 0, len(s.Devices))
	for id := range s.Devices {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := []any{}
	for _, id := range ids {
		out = append(out, s.deviceJSON(s.Devices[id]))
	}
	ok(w, map[string]any{"devices": out, "count": len(out)})
}

func str(b map[string]any, k string) string { v, _ := b[k].(string); return v }

func (s *Server) addDevice(w http.ResponseWriter, b map[string]any) {
	host := str(b, "hostname")
	if host == "" {
		apiErr(w, http.StatusBadRequest, "Missing the device hostname")
		return
	}
	for _, d := range s.Devices {
		if strings.EqualFold(d.Hostname, host) {
			apiErr(w, http.StatusBadRequest, "Already have device "+host)
			return
		}
	}
	for k := range b {
		if !slices.Contains([]string{"hostname", "display_template", "display", "location", "snmpver", "community", "authlevel", "authname",
			"authpass", "authalgo", "cryptopass", "cryptoalgo", "snmp_disable", "os", "force_add", "ping_fallback"}, k) {
			s.t.Errorf("add_device: campo no documentado %q", k)
		}
	}
	d := &Device{Hostname: host, Display: str(b, "display_template")}
	d.ForceAdd, _ = b["force_add"].(bool)
	if dis, _ := b["snmp_disable"].(bool); dis {
		d.SNMPDisable, d.OS = true, str(b, "os")
		if d.OS == "" {
			d.OS = "ping"
		}
	} else {
		d.SNMPVer = str(b, "snmpver")
		switch d.SNMPVer {
		case "v2c", "v1":
			d.Community = str(b, "community")
			if d.Community == "" {
				apiErr(w, http.StatusBadRequest, "SNMP information is required when force adding a device")
				return
			}
		case "v3":
			d.AuthName = str(b, "authname")
			if str(b, "authlevel") == "" || d.AuthName == "" {
				apiErr(w, http.StatusBadRequest, "SNMP information is required when force adding a device")
				return
			}
		default:
			apiErr(w, http.StatusBadRequest, "snmpver no válido")
			return
		}
		d.OS = "routeros"
	}
	if s.Unreachable[host] && !d.ForceAdd {
		apiErr(w, http.StatusBadRequest, "Could not connect to "+host+", please check the snmp details and snmp reachability")
		return
	}
	d.ID = s.id()
	s.Devices[d.ID] = d
	ok(w, map[string]any{"message": fmt.Sprintf("Device %s (%d) has been added successfully", host, d.ID), "devices": []any{s.deviceJSON(d)}})
}

func (s *Server) delDevice(w http.ResponseWriter, ref string) {
	id, err := strconv.Atoi(ref)
	d, found := s.Devices[id]
	if err != nil || !found {
		apiErr(w, http.StatusNotFound, "Device "+ref+" not found")
		return
	}
	delete(s.Devices, id)
	for _, g := range s.Groups {
		g.Devices = slices.DeleteFunc(g.Devices, func(x int) bool { return x == id })
	}
	ok(w, map[string]any{"devices": []any{s.deviceJSON(d)}, "message": "Removed device " + d.Hostname + "\n"})
}

func (s *Server) local() string { return time.Now().UTC().Add(s.Offset).Format(time.DateTime) }

func (s *Server) addEventlog(w http.ResponseWriter, ref string, b map[string]any) {
	id, _ := strconv.Atoi(ref)
	if _, found := s.Devices[id]; !found {
		apiErr(w, http.StatusNotFound, ref+" device does not exist")
		return
	}
	text := str(b, "text")
	if text == "" {
		apiErr(w, http.StatusBadRequest, "No Eventlog text provided.")
		return
	}
	if sev := str(b, "severity"); sev < "1" || sev > "5" || len(sev) != 1 {
		s.t.Errorf("add_eventlog: severity %q fuera de 1..5", sev)
	}
	s.Events = append(s.Events, Syslog{DeviceID: id, Msg: text, Timestamp: s.local()})
	ok(w, map[string]any{"message": "Eventlog received for " + ref})
}

func (s *Server) listGroups(w http.ResponseWriter) {
	if len(s.Groups) == 0 {
		apiErr(w, http.StatusNotFound, "No device groups found")
		return
	}
	out := []any{}
	for _, g := range s.Groups {
		out = append(out, map[string]any{"id": g.ID, "name": g.Name, "desc": g.Desc, "type": g.Type})
	}
	ok(w, map[string]any{"groups": out, "message": fmt.Sprintf("Found %d device groups", len(out))})
}

func (s *Server) addGroup(w http.ResponseWriter, b map[string]any) {
	name := str(b, "name")
	for _, g := range s.Groups {
		if g.Name == name {
			apiErr(w, http.StatusUnprocessableEntity, map[string][]string{"name": {"The name has already been taken."}})
			return
		}
	}
	devs, isArr := intsOf(b["devices"])
	if name == "" || str(b, "type") != "static" || !isArr {
		apiErr(w, http.StatusUnprocessableEntity, map[string][]string{"devices": {"The devices field must be present when type is static."}})
		return
	}
	g := &Group{ID: s.id(), Name: name, Type: "static", Desc: str(b, "desc"), Devices: devs}
	s.Groups[g.ID] = g
	write(w, http.StatusCreated, map[string]any{"status": "ok", "id": g.ID, "message": "Device group " + name + " created"})
}

func (s *Server) groupDevices(w http.ResponseWriter, method, ref string, b map[string]any) {
	id, _ := strconv.Atoi(ref)
	g, found := s.Groups[id]
	if !found {
		apiErr(w, http.StatusNotFound, "Device group "+ref+" not found")
		return
	}
	devs, isArr := intsOf(b["devices"])
	if !isArr {
		apiErr(w, http.StatusUnprocessableEntity, map[string][]string{"devices": {"The devices field must be an array."}})
		return
	}
	for _, d := range devs {
		if method == http.MethodPost && !slices.Contains(g.Devices, d) {
			g.Devices = append(g.Devices, d)
		}
		if method == http.MethodDelete {
			g.Devices = slices.DeleteFunc(g.Devices, func(x int) bool { return x == d })
		}
	}
	ok(w, map[string]any{"message": "Devices added"})
}

func (s *Server) listRules(w http.ResponseWriter) {
	out := []any{}
	for _, r := range s.Rules {
		m := map[string]any{"id": strconv.Itoa(r.ID), "name": r.Name, "severity": r.Severity, "disabled": "0", "extra": "{}"}
		if s.Operations {
			m["alert_operation_id"] = r.OperationID
		}
		out = append(out, m)
	}
	ok(w, map[string]any{"rules": out})
}

func (s *Server) addEditRule(w http.ResponseWriter, method string, b map[string]any) {
	devs, _ := intsOf(b["devices"])
	_, hasRuleID := b["rule_id"]
	if method == http.MethodPost && hasRuleID {
		s.t.Errorf("add_rule con rule_id")
	}
	if len(devs) == 0 && !hasRuleID {
		apiErr(w, http.StatusBadRequest, "Missing the devices or global device (-1)")
		return
	}
	builder, isObj := b["builder"].(map[string]any)
	if !isObj || builder["condition"] == nil || builder["rules"] == nil {
		apiErr(w, http.StatusBadRequest, "Missing the alert builder rule")
		return
	}
	for _, x := range builder["rules"].([]any) {
		c, _ := x.(map[string]any)
		for _, k := range []string{"id", "field", "type", "input", "operator", "value"} {
			if _, has := c[k]; !has {
				s.t.Errorf("builder: condición sin %q: %v", k, c)
			}
		}
	}
	name := str(b, "name")
	if name == "" {
		apiErr(w, http.StatusBadRequest, "Missing the alert rule name")
		return
	}
	sev := str(b, "severity")
	if !slices.Contains([]string{"ok", "warning", "critical"}, sev) {
		apiErr(w, http.StatusBadRequest, "Missing the severity")
		return
	}
	groups, _ := intsOf(b["groups"])
	devices := slices.DeleteFunc(devs, func(x int) bool { return x == -1 })
	var r *Rule
	if hasRuleID {
		id, _ := intsOf([]any{b["rule_id"]})
		r = s.Rules[id[0]]
		if r == nil {
			apiErr(w, http.StatusInternalServerError, "Failed to update existing alert rule")
			return
		}
		for _, o := range s.Rules {
			if o.Name == name && o.ID != r.ID {
				apiErr(w, http.StatusInternalServerError, "Update failed : Invalid rule id")
				return
			}
		}
	} else {
		for _, o := range s.Rules {
			if o.Name == name {
				apiErr(w, http.StatusInternalServerError, "Addition failed : Name has already been used")
				return
			}
		}
		r = &Rule{ID: s.id()}
		s.Rules[r.ID] = r
	}
	r.Name, r.Devices, r.Groups, r.Builder, r.Severity, r.Body = name, devices, groups, builder, sev, b
	if s.Operations {
		if v, has := b["alert_operation_id"]; has {
			if v == nil {
				r.OperationID = nil
			} else {
				n := int(v.(float64))
				r.OperationID = &n
			}
		} else if _, legacy := b["count"]; legacy {
			if !s.DefaultTransports {
				apiErr(w, http.StatusBadRequest, "Each operation must include at least one transport or transport group.")
				return
			}
			s.nextOp++
			n := s.nextOp
			r.OperationID = &n
		}
	}
	write(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) listTemplates(w http.ResponseWriter) {
	out := []any{map[string]any{"id": 1, "name": "Default Alert Template", "template": "x", "title": nil, "title_rec": nil}}
	for _, t := range s.Templates {
		out = append(out, map[string]any{"id": t.ID, "name": t.Name, "template": t.Template, "title": t.Title, "title_rec": t.TitleRec})
	}
	ok(w, map[string]any{"alert_templates": out})
}

func (s *Server) saveTemplate(w http.ResponseWriter, b map[string]any) {
	name, tpl := str(b, "name"), str(b, "template")
	if name == "" {
		apiErr(w, http.StatusBadRequest, "name variable must be set")
		return
	}
	if tpl == "" {
		apiErr(w, http.StatusBadRequest, "template variable must be set")
		return
	}
	for _, k := range []string{"{{ $alert", "@if", "@endif"} {
		if !strings.Contains(tpl, k) {
			s.t.Errorf("plantilla %q sin %q", name, k)
		}
	}
	if strings.Count(tpl, "@if") != strings.Count(tpl, "@endif") || strings.Count(tpl, "@foreach") != strings.Count(tpl, "@endforeach") {
		apiErr(w, http.StatusBadRequest, "Template failed to be parsed, please check the syntax.")
		return
	}
	rules, isArr := intsOf(b["alert_rules"])
	if !isArr {
		s.t.Errorf("plantilla %q sin alert_rules", name)
	}
	var t *Template
	if v, has := b["template_id"]; has {
		id, _ := intsOf([]any{v})
		t = s.Templates[id[0]]
		if t == nil {
			apiErr(w, http.StatusInternalServerError, "Template not found")
			return
		}
	} else {
		t = &Template{ID: s.id()}
		s.Templates[t.ID] = t
	}
	t.Name, t.Template, t.Title, t.TitleRec, t.Rules = name, tpl, str(b, "title"), str(b, "title_rec"), rules
	write(w, http.StatusOK, map[string]any{"status": "ok", "message": "Alert template has been created and attached rules have been updated.", "id": t.ID})
}

func (s *Server) syslogsink(w http.ResponseWriter, one map[string]any, many []any) {
	entries := many
	if one != nil {
		entries = []any{one}
	}
	for _, e := range entries {
		m, _ := e.(map[string]any)
		host, msg, ts := str(m, "host"), str(m, "msg"), str(m, "timestamp")
		if _, err := time.Parse(time.DateTime, ts); err != nil {
			s.t.Errorf("syslogsink: timestamp %q no es «AAAA-MM-DD hh:mm:ss»", ts)
		}
		if !s.SyslogEnabled {
			continue
		}
		for _, d := range s.Devices {
			if strings.EqualFold(d.Hostname, host) {
				s.Syslogs = append(s.Syslogs, Syslog{DeviceID: d.ID, Msg: msg, Timestamp: ts, Program: strings.ToUpper(str(m, "program"))})
			}
		}
	}
	ok(w, map[string]any{"message": fmt.Sprintf("Syslog received: %d", len(entries))})
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request, kind, ref string) {
	id, _ := strconv.Atoi(ref)
	src := s.Syslogs
	if kind == "eventlog" {
		src = s.Events
	}
	out := []any{}
	for i := len(src) - 1; i >= 0; i-- {
		l := src[i]
		if l.DeviceID != id {
			continue
		}
		if kind == "syslog" {
			out = append(out, map[string]any{"device_id": id, "msg": l.Msg, "timestamp": l.Timestamp, "program": l.Program})
		} else {
			out = append(out, map[string]any{"device_id": id, "message": l.Msg, "datetime": l.Timestamp, "type": "horus"})
		}
	}
	if r.URL.Query().Get("sortorder") != "DESC" {
		slices.Reverse(out)
	}
	ok(w, map[string]any{"logs": out, "count": len(out), "total": len(out)})
}

// GroupByName devuelve el grupo con ese nombre.
func (s *Server) GroupByName(name string) *Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.Groups {
		if g.Name == name {
			return g
		}
	}
	return nil
}

// RuleByName devuelve la regla con ese nombre.
func (s *Server) RuleByName(name string) *Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.Rules {
		if r.Name == name {
			return r
		}
	}
	return nil
}

// DeviceByHost devuelve el dispositivo con ese hostname.
func (s *Server) DeviceByHost(host string) *Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.Devices {
		if d.Hostname == host {
			return d
		}
	}
	return nil
}
