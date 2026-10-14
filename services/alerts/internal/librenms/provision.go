package librenms

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"
)

// Tipos de objeto que Horus registra (alerts.librenms_object.kind).
const (
	KindDevice       = "device"
	KindRouterDevice = "router_device"
	KindInfraDevice  = "infra_device"
	KindGroup        = "group"
	KindTemplate     = "template"
	KindRule         = "rule"
)

// Acciones de un paso de la sincronización.
const (
	ActionCreated   = "created"
	ActionAdopted   = "adopted"
	ActionUpdated   = "updated"
	ActionUnchanged = "unchanged"
	ActionRecreated = "recreated"
	ActionRemoved   = "removed"
	ActionDeleted   = "deleted"
	ActionFailed    = "failed"
	ActionSkipped   = "skipped"
)

// Object es un objeto de LibreNMS que Horus conoce: lo creó (CreatedByHorus)
// o lo adoptó porque ya existía con el mismo nombre/host. Active = false
// cuando el router o servicio ya no está en el plan (fuera del grupo, pero
// conservado para poder borrarlo a petición explícita).
type Object struct {
	Kind           string
	Key            string
	Name           string
	RemoteID       ID
	Hostname       string
	CreatedByHorus bool
	Active         bool
}

// Step es una línea del resultado (lo que se hizo con cada objeto).
type Step struct {
	Kind     string `json:"kind"`
	Key      string `json:"key"`
	Name     string `json:"name"`
	Action   string `json:"action"`
	RemoteID *int64 `json:"remote_id"`
	Code     string `json:"error_code,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Modos de monitoreo de un router en LibreNMS.
const (
	ModePing       = "ping"
	ModeSNMPv2c    = "snmp_v2c"
	ModeSNMPv3     = "snmp_v3"
	ModeHorusSNMP3 = "horus_snmp_v3"
)

// Target es un dispositivo a mantener en un grupo (router o servicio).
type Target struct {
	Key       string // router_id o nombre del servicio
	Name      string
	Host      string // IP de gestión alcanzable desde LibreNMS
	Community string
	V3        *SNMPv3
}

// Plan es lo que una sincronización debe dejar en LibreNMS.
type Plan struct {
	HorusHost  string
	Thresholds Thresholds
	// Routers: nil = no tocar la pertenencia de routers.
	Routers []Target
	// Infra: nil = no tocar la pertenencia de «Horus · Infraestructura».
	Infra []Target
	// InfraOnly sincroniza solo el grupo de infraestructura y sus
	// dispositivos (gancho del inventario de servicios).
	InfraOnly bool
}

// Result es el resultado de Sync.
type Result struct {
	Version  string
	Objects  []Object
	Steps    []Step
	Warnings []string
	// Err: fallo que impidió terminar (p. ej. autenticación); los fallos de
	// un objeto concreto quedan en su Step y la sincronización sigue.
	Err *Error
}

// Failed cuenta los pasos fallidos.
func (r *Result) Failed() int {
	n := 0
	for _, s := range r.Steps {
		if s.Action == ActionFailed {
			n++
		}
	}
	return n
}

// Find devuelve el objeto (kind, key) del resultado.
func (r *Result) Find(kind, key string) (Object, bool) {
	for _, o := range r.Objects {
		if o.Kind == kind && o.Key == key {
			return o, true
		}
	}
	return Object{}, false
}

// Provisioner aplica un Plan de forma idempotente.
type Provisioner struct {
	c *Client
}

// NewProvisioner crea el aprovisionador.
func NewProvisioner(c *Client) *Provisioner { return &Provisioner{c: c} }

type syncState struct {
	ctx      context.Context
	c        *Client
	res      *Result
	objs     map[string]*Object // kind/key
	devices  []Device
	groups   []Group
	rules    []Rule
	tpls     []Template
	devByID  map[ID]Device
	groupIDs map[ID]bool
	ruleByID map[ID]Rule
	tplIDs   map[ID]bool
}

func okey(kind, key string) string { return kind + "/" + key }

func idp(id ID) *int64 {
	v := int64(id)
	return &v
}

func (s *syncState) step(kind, key, name, action string, id ID, err error) {
	st := Step{Kind: kind, Key: key, Name: name, Action: action}
	if id != 0 {
		st.RemoteID = idp(id)
	}
	if err != nil {
		st.Action = ActionFailed
		st.Code = CodeOf(err)
		if e, ok := AsError(err); ok {
			st.Error = e.Message
		}
		if st.Error == "" {
			st.Error = err.Error()
		}
	}
	s.res.Steps = append(s.res.Steps, st)
}

func (s *syncState) put(o Object) {
	cp := o
	s.objs[okey(o.Kind, o.Key)] = &cp
}

func (s *syncState) get(kind, key string) *Object { return s.objs[okey(kind, key)] }

// Sync deja LibreNMS como pide el plan partiendo de los objetos que Horus ya
// conoce. Nunca modifica ni borra objetos que Horus no creó ni adoptó;
// recrea lo que se borró a mano en LibreNMS (deriva).
func (p *Provisioner) Sync(ctx context.Context, known []Object, plan Plan) (res Result) {
	s := &syncState{ctx: ctx, c: p.c, res: &res, objs: map[string]*Object{}}
	for _, o := range known {
		s.put(o)
	}
	defer func() {
		res.Objects = make([]Object, 0, len(s.objs))
		for _, o := range s.objs {
			res.Objects = append(res.Objects, *o)
		}
		slices.SortFunc(res.Objects, func(a, b Object) int { return strings.Compare(okey(a.Kind, a.Key), okey(b.Kind, b.Key)) })
	}()
	ver, err := p.c.System(ctx)
	if err != nil {
		res.Err = asErr(err)
		return
	}
	res.Version = ver
	if s.devices, err = p.c.ListDevices(ctx); err != nil {
		res.Err = asErr(err)
		return
	}
	if s.groups, err = p.c.ListDeviceGroups(ctx); err != nil {
		res.Err = asErr(err)
		return
	}
	s.index()
	if !plan.InfraOnly {
		if s.rules, err = p.c.ListRules(ctx); err != nil {
			res.Err = asErr(err)
			return
		}
		if s.tpls, err = p.c.ListTemplates(ctx); err != nil {
			res.Err = asErr(err)
			return
		}
		s.index()
		if strings.TrimSpace(plan.HorusHost) == "" {
			s.res.Warnings = append(s.res.Warnings, "Sin host público de Horus: no se crea el dispositivo «Horus Flow» ni su regla.")
		} else {
			s.ensureHorusDevice(strings.TrimSpace(plan.HorusHost))
		}
		s.ensureGroup(KeyGroupRouters, Prefix+"Routers", "Routers de Horus Flow (gestionado por Horus).")
	}
	s.ensureGroup(KeyGroupInfra, Prefix+"Infraestructura", "Servicios de infraestructura del ISP (gestionado por Horus).")
	if plan.Routers != nil && !plan.InfraOnly {
		s.syncMembers(KindRouterDevice, KeyGroupRouters, plan.Routers)
	}
	if plan.Infra != nil {
		s.syncMembers(KindInfraDevice, KeyGroupInfra, plan.Infra)
	}
	if !plan.InfraOnly {
		ruleIDs := s.ensureRules(plan.Thresholds.Normalize())
		s.ensureTemplates(ruleIDs)
	}
	return
}

func asErr(err error) *Error {
	if e, ok := AsError(err); ok {
		return e
	}
	return &Error{Code: CodeConnectFailed, Message: err.Error()}
}

func (s *syncState) index() {
	s.devByID = map[ID]Device{}
	for _, d := range s.devices {
		s.devByID[d.ID] = d
	}
	s.groupIDs = map[ID]bool{}
	for _, g := range s.groups {
		s.groupIDs[g.ID] = true
	}
	s.ruleByID = map[ID]Rule{}
	for _, r := range s.rules {
		s.ruleByID[r.ID] = r
	}
	s.tplIDs = map[ID]bool{}
	for _, t := range s.tpls {
		s.tplIDs[t.ID] = true
	}
}

// matchDevice busca un dispositivo existente por hostname, sysName o IP.
func (s *syncState) matchDevice(host string) (Device, bool) {
	ip, ipErr := netip.ParseAddr(host)
	for _, d := range s.devices {
		if strings.EqualFold(d.Hostname, host) || (d.SysName != "" && strings.EqualFold(d.SysName, host)) {
			return d, true
		}
		if ipErr == nil {
			if dip, err := netip.ParseAddr(d.IP); err == nil && dip == ip {
				return d, true
			}
		}
	}
	return Device{}, false
}

// ensureDevice garantiza un dispositivo (kind, key) con hostname host:
// conserva el conocido si sigue en LibreNMS, adopta uno existente con el
// mismo host o lo crea.
func (s *syncState) ensureDevice(kind, key, name, host string, nd NewDevice) (*Object, bool) {
	cur := s.get(kind, key)
	if cur != nil && cur.RemoteID != 0 && strings.EqualFold(cur.Hostname, host) {
		if _, ok := s.devByID[cur.RemoteID]; ok {
			cur.Active, cur.Name = true, name
			s.step(kind, key, name, ActionUnchanged, cur.RemoteID, nil)
			return cur, true
		}
	}
	action := ActionCreated
	if cur != nil && strings.EqualFold(cur.Hostname, host) {
		action = ActionRecreated // borrado a mano en LibreNMS
	}
	if cur != nil && cur.RemoteID != 0 && !strings.EqualFold(cur.Hostname, host) {
		if _, ok := s.devByID[cur.RemoteID]; ok && cur.CreatedByHorus {
			s.res.Warnings = append(s.res.Warnings, fmt.Sprintf("El dispositivo anterior de «%s» (id %d, %s) sigue en LibreNMS: bórralo allí si ya no lo usas.",
				name, cur.RemoteID, cur.Hostname))
		}
	}
	if d, ok := s.matchDevice(host); ok {
		o := Object{Kind: kind, Key: key, Name: name, RemoteID: d.ID, Hostname: host, Active: true}
		s.put(o)
		s.step(kind, key, name, ActionAdopted, d.ID, nil)
		return s.get(kind, key), true
	}
	nd.Hostname = host
	d, err := s.c.AddDevice(s.ctx, nd)
	if err != nil {
		s.step(kind, key, name, ActionFailed, 0, err)
		return nil, false
	}
	s.devices = append(s.devices, d)
	s.devByID[d.ID] = d
	o := Object{Kind: kind, Key: key, Name: name, RemoteID: d.ID, Hostname: host, CreatedByHorus: true, Active: true}
	s.put(o)
	s.step(kind, key, name, action, d.ID, nil)
	return s.get(kind, key), true
}

func (s *syncState) ensureHorusDevice(host string) {
	s.ensureDevice(KindDevice, KeyHorusDevice, HorusDeviceName, host,
		NewDevice{Display: HorusDeviceName, ForceAdd: true})
}

func (s *syncState) ensureGroup(key, name, desc string) {
	cur := s.get(KindGroup, key)
	if cur != nil && s.groupIDs[cur.RemoteID] {
		s.step(KindGroup, key, name, ActionUnchanged, cur.RemoteID, nil)
		return
	}
	for _, g := range s.groups {
		if g.Name == name {
			s.put(Object{Kind: KindGroup, Key: key, Name: name, RemoteID: g.ID, CreatedByHorus: true, Active: true})
			s.step(KindGroup, key, name, ActionAdopted, g.ID, nil)
			return
		}
	}
	action := ActionCreated
	if cur != nil {
		action = ActionRecreated
		// Los miembros conocidos se vuelven a añadir al recrear el grupo.
	}
	id, err := s.c.AddStaticGroup(s.ctx, name, desc, nil)
	if err != nil {
		s.step(KindGroup, key, name, ActionFailed, 0, err)
		if cur != nil {
			delete(s.objs, okey(KindGroup, key))
		}
		return
	}
	s.groups = append(s.groups, Group{ID: id, Name: name, Type: "static"})
	s.groupIDs[id] = true
	s.put(Object{Kind: KindGroup, Key: key, Name: name, RemoteID: id, CreatedByHorus: true, Active: true})
	s.step(KindGroup, key, name, action, id, nil)
}

// syncMembers mantiene los dispositivos del plan en el grupo y saca del grupo
// los que ya no están (sin borrarlos de LibreNMS).
func (s *syncState) syncMembers(kind, groupKey string, targets []Target) {
	group := s.get(KindGroup, groupKey)
	want := map[string]bool{}
	var add []ID
	for _, t := range targets {
		want[t.Key] = true
		nd := NewDevice{Display: t.Name, Community: t.Community, V3: t.V3}
		if t.Community == "" && t.V3 == nil {
			nd.ForceAdd = true // solo ICMP: se añade aunque hoy no responda (aparece caído)
		}
		prev := s.get(kind, t.Key)
		var prevID ID
		if prev != nil && !strings.EqualFold(prev.Hostname, t.Host) {
			prevID = prev.RemoteID
		}
		o, ok := s.ensureDevice(kind, t.Key, t.Name, t.Host, nd)
		if !ok {
			continue
		}
		add = append(add, o.RemoteID)
		if prevID != 0 && group != nil && prevID != o.RemoteID {
			if err := s.c.RemoveGroupDevices(s.ctx, group.RemoteID, []ID{prevID}); err != nil {
				s.step(kind, t.Key, t.Name, ActionFailed, prevID, err)
			}
		}
	}
	var remove []ID
	for _, o := range s.objs {
		if o.Kind != kind || want[o.Key] || !o.Active {
			continue
		}
		o.Active = false
		if _, ok := s.devByID[o.RemoteID]; !ok {
			delete(s.objs, okey(kind, o.Key)) // ya no existe en LibreNMS
			continue
		}
		remove = append(remove, o.RemoteID)
		s.step(kind, o.Key, o.Name, ActionRemoved, o.RemoteID, nil)
		if !o.CreatedByHorus {
			delete(s.objs, okey(kind, o.Key)) // adoptado: Horus deja de seguirlo
		}
	}
	if group == nil {
		return
	}
	if err := s.c.AddGroupDevices(s.ctx, group.RemoteID, add); err != nil {
		s.step(KindGroup, groupKey, group.Name, ActionFailed, group.RemoteID, err)
	}
	if err := s.c.RemoveGroupDevices(s.ctx, group.RemoteID, remove); err != nil {
		s.step(KindGroup, groupKey, group.Name, ActionFailed, group.RemoteID, err)
	}
}

func (s *syncState) findRuleByName(name string) (Rule, bool) {
	for _, r := range s.rules {
		if r.Name == name {
			return r, true
		}
	}
	return Rule{}, false
}

// ensureRules crea o actualiza cada regla; devuelve key → id.
func (s *syncState) ensureRules(th Thresholds) map[string]ID {
	out := map[string]ID{}
	for _, def := range rules(th) {
		in := RuleInput{Name: def.Name, Builder: def.Builder, Severity: def.Severity, Notes: def.Notes, Count: def.Count,
			Delay: def.Delay, Interval: def.Interval, Recovery: true}
		switch def.Target {
		case KeyHorusDevice:
			d := s.get(KindDevice, KeyHorusDevice)
			if d == nil || d.RemoteID == 0 {
				s.step(KindRule, def.Key, def.Name, ActionSkipped, 0, nil)
				continue
			}
			in.Devices = []ID{d.RemoteID}
		default:
			g := s.get(KindGroup, def.Target)
			if g == nil {
				s.step(KindRule, def.Key, def.Name, ActionSkipped, 0, nil)
				continue
			}
			in.Groups = []ID{g.RemoteID}
		}
		cur := s.get(KindRule, def.Key)
		var existing *Rule
		if cur != nil {
			if r, ok := s.ruleByID[cur.RemoteID]; ok {
				existing = &r
			}
		}
		if existing == nil {
			if r, ok := s.findRuleByName(def.Name); ok {
				existing = &r
			}
		}
		if existing != nil {
			if existing.HasOperation && existing.AlertOperationID != nil && *existing.AlertOperationID != 0 {
				in.KeepOperation = existing.AlertOperationID // no crear otra operación en cada sincronización
			}
			err := s.c.EditRule(s.ctx, existing.ID, in)
			action := ActionUpdated
			if cur == nil || cur.RemoteID != existing.ID {
				action = ActionAdopted
			}
			if err != nil && !s.transportWarning(err) {
				s.step(KindRule, def.Key, def.Name, ActionFailed, existing.ID, err)
				continue
			}
			s.put(Object{Kind: KindRule, Key: def.Key, Name: def.Name, RemoteID: existing.ID, CreatedByHorus: true, Active: true})
			s.step(KindRule, def.Key, def.Name, action, existing.ID, nil)
			out[def.Key] = existing.ID
			continue
		}
		addErr := s.c.AddRule(s.ctx, in)
		if addErr != nil && !s.transportWarning(addErr) {
			s.step(KindRule, def.Key, def.Name, ActionFailed, 0, addErr)
			continue
		}
		// add_rule no devuelve el id: se relee la lista y se busca por nombre.
		rs, err := s.c.ListRules(s.ctx)
		if err == nil {
			s.rules = rs
			s.index()
		}
		r, ok := s.findRuleByName(def.Name)
		if !ok {
			s.step(KindRule, def.Key, def.Name, ActionFailed, 0, &Error{Op: "add_rule", Code: CodeUnexpected,
				Message: "LibreNMS aceptó la regla pero no aparece en la lista"})
			continue
		}
		action := ActionCreated
		if cur != nil {
			action = ActionRecreated
		}
		s.put(Object{Kind: KindRule, Key: def.Key, Name: def.Name, RemoteID: r.ID, CreatedByHorus: true, Active: true})
		s.step(KindRule, def.Key, def.Name, action, r.ID, nil)
		out[def.Key] = r.ID
	}
	return out
}

// transportWarning reconoce el 400 de las versiones con operaciones cuando no
// hay transportes por defecto: la regla queda guardada pero sin avisos.
func (s *syncState) transportWarning(err error) bool {
	e, ok := AsError(err)
	if !ok || e.Status != 400 || !strings.Contains(strings.ToLower(e.Message), "transport") {
		return false
	}
	const w = "LibreNMS no tiene transportes marcados por defecto: las reglas se crearon sin avisos. Marca un transporte como «Default» en LibreNMS y vuelve a sincronizar."
	if !slices.Contains(s.res.Warnings, w) {
		s.res.Warnings = append(s.res.Warnings, w)
	}
	return true
}

func (s *syncState) ensureTemplates(ruleIDs map[string]ID) {
	for _, def := range templates() {
		in := TemplateInput{Name: def.Name, Template: def.Body, Title: def.Title, TitleRec: def.TitleRec}
		if id, ok := ruleIDs[def.Key]; ok {
			in.Rules = []ID{id}
		}
		cur := s.get(KindTemplate, def.Key)
		var id ID
		action := ActionCreated
		if cur != nil && s.tplIDs[cur.RemoteID] {
			id, action = cur.RemoteID, ActionUpdated
		} else {
			if cur != nil {
				action = ActionRecreated
			}
			for _, t := range s.tpls {
				if t.Name == def.Name {
					id, action = t.ID, ActionAdopted
					break
				}
			}
		}
		got, err := s.c.SaveTemplate(s.ctx, id, in)
		if err != nil {
			s.step(KindTemplate, def.Key, def.Name, ActionFailed, id, err)
			continue
		}
		s.put(Object{Kind: KindTemplate, Key: def.Key, Name: def.Name, RemoteID: got, CreatedByHorus: true, Active: true})
		s.step(KindTemplate, def.Key, def.Name, action, got, nil)
	}
}

// ---------------------------------------------------------------- syslog de Horus

// SyslogTime formatea t en la zona horaria de LibreNMS (offset en minutos
// respecto de UTC, medido con Calibrate).
func SyslogTime(t time.Time, offsetMinutes int) string {
	return t.UTC().Add(time.Duration(offsetMinutes) * time.Minute).Format(time.DateTime)
}

// Calibrate mide el desfase horario de LibreNMS: escribe una entrada en el
// registro de eventos del dispositivo y compara su `datetime` (hora local de
// LibreNMS) con la hora UTC. El syslog de LibreNMS guarda la marca de tiempo
// tal cual llega, y la regla compara con NOW() local: sin este desfase una
// línea podría quedar «en el futuro» y la alerta no se resolvería.
func (c *Client) Calibrate(ctx context.Context, device ID, now func() time.Time) (int, error) {
	marker := "Horus Flow: calibración horaria " + now().UTC().Format("150405.000")
	sent := now().UTC()
	if err := c.AddEventlog(ctx, device, marker, EventInfo, "horus"); err != nil {
		return 0, err
	}
	lines, err := c.RecentEventlog(ctx, device, 10)
	if err != nil {
		return 0, err
	}
	for _, l := range lines {
		if !strings.Contains(l.Text(), marker) {
			continue
		}
		local, err := time.Parse(time.DateTime, strings.TrimSpace(l.When()))
		if err != nil {
			return 0, &Error{Op: "list_eventlog", Code: CodeUnexpected, Message: "fecha del registro ilegible"}
		}
		diff := local.Sub(sent).Minutes()
		// Redondeo al cuarto de hora (zonas como +05:45).
		q := int((diff + 7.5*sign(diff)) / 15)
		return q * 15, nil
	}
	return 0, &Error{Op: "list_eventlog", Code: CodeUnexpected, Message: "la entrada de calibración no aparece en el registro"}
}

func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
}
