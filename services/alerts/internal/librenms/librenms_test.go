package librenms_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/services/alerts/internal/librenms"
	"github.com/hcdestroyer/horus-flow/services/alerts/internal/librenms/librenmsfake"
)

const token = "token-de-prueba-librenms-1234"

func client(f *librenmsfake.Server, tok string) *librenms.Client {
	return librenms.New(librenms.Config{BaseURL: f.URL + "/", Token: tok, TLSVerify: true, Timeout: 5 * time.Second})
}

func plan() librenms.Plan {
	return librenms.Plan{HorusHost: "horus.isp.example", Thresholds: librenms.Thresholds{},
		Routers: []librenms.Target{
			{Key: "r1", Name: "rt-centro", Host: "198.51.100.10"},
			{Key: "r2", Name: "rt-norte", Host: "198.51.100.11", Community: "solo-lectura"},
		}}
}

func steps(r librenms.Result, action string) int {
	n := 0
	for _, s := range r.Steps {
		if s.Action == action {
			n++
		}
	}
	return n
}

func TestSyncProvisionsIdempotently(t *testing.T) {
	f := librenmsfake.New(t, token)
	p := librenms.NewProvisioner(client(f, token))
	ctx := context.Background()

	res := p.Sync(ctx, nil, plan())
	if res.Err != nil || res.Failed() != 0 {
		t.Fatalf("sync: %+v %+v", res.Err, res.Steps)
	}
	if res.Version != "25.10.0" {
		t.Fatalf("versión = %q", res.Version)
	}
	horus := f.DeviceByHost("horus.isp.example")
	if horus == nil || !horus.SNMPDisable || horus.OS != "ping" || !horus.ForceAdd || horus.Display != "Horus Flow" {
		t.Fatalf("dispositivo Horus = %+v", horus)
	}
	r2 := f.DeviceByHost("198.51.100.11")
	if r2 == nil || r2.SNMPVer != "v2c" || r2.Community != "solo-lectura" || r2.ForceAdd {
		t.Fatalf("router SNMP = %+v", r2)
	}
	if r1 := f.DeviceByHost("198.51.100.10"); r1 == nil || !r1.SNMPDisable || !r1.ForceAdd {
		t.Fatalf("router ICMP = %+v", r1)
	}
	routers := f.GroupByName("Horus · Routers")
	if routers == nil || len(routers.Devices) != 2 || f.GroupByName("Horus · Infraestructura") == nil {
		t.Fatalf("grupos = %+v", f.Groups)
	}
	cpu := f.RuleByName("Horus · CPU alta")
	if cpu == nil || len(cpu.Groups) != 1 || cpu.Groups[0] != routers.ID || len(cpu.Devices) != 0 || cpu.Severity != "warning" {
		t.Fatalf("regla CPU = %+v", cpu)
	}
	if !strings.Contains(stringify(cpu.Builder), `"value":"85"`) || !strings.Contains(stringify(cpu.Builder), "macros.device_up") {
		t.Fatalf("builder CPU = %s", stringify(cpu.Builder))
	}
	ev := f.RuleByName("Horus · Evento de seguridad Horus")
	if ev == nil || len(ev.Devices) != 1 || ev.Devices[0] != horus.ID || !strings.Contains(stringify(ev.Builder), "`macros.past_5m`") ||
		!strings.Contains(stringify(ev.Builder), `"operator":"regex","value":"^HORUS "`) {
		t.Fatalf("regla de eventos = %+v", ev)
	}
	if mem := f.RuleByName("Horus · Memoria alta"); mem == nil || !strings.Contains(stringify(mem.Builder), `"value":"90"`) {
		t.Fatalf("regla de memoria = %+v", mem)
	}
	if len(f.Templates) != 7 || len(f.Rules) != 7 {
		t.Fatalf("plantillas=%d reglas=%d", len(f.Templates), len(f.Rules))
	}
	for _, tpl := range f.Templates {
		if !strings.HasPrefix(tpl.Name, "Horus · ") || tpl.Title == "" || tpl.TitleRec == "" || len(tpl.Rules) != 1 ||
			!strings.Contains(tpl.Template, "$alert->state == 0") {
			t.Fatalf("plantilla = %+v", tpl)
		}
	}
	if _, ok := res.Find(librenms.KindRule, librenms.KeyHorusEvent); !ok {
		t.Fatal("regla de eventos no registrada")
	}

	// Segunda pasada: nada nuevo (idempotente).
	before := len(f.Devices)
	res2 := p.Sync(ctx, res.Objects, plan())
	if res2.Err != nil || res2.Failed() != 0 || steps(res2, librenms.ActionCreated) != 0 || len(f.Devices) != before || len(f.Rules) != 7 {
		t.Fatalf("segunda pasada: %+v", res2.Steps)
	}

	// Deriva: alguien borra en LibreNMS el dispositivo Horus y una regla → se recrean.
	f.Lock()
	delete(f.Devices, horus.ID)
	delete(f.Rules, cpu.ID)
	f.Unlock()
	res3 := p.Sync(ctx, res2.Objects, plan())
	if res3.Failed() != 0 || steps(res3, librenms.ActionRecreated) != 2 {
		t.Fatalf("deriva: %+v", res3.Steps)
	}
	if f.RuleByName("Horus · Evento de seguridad Horus").Devices[0] == horus.ID {
		t.Fatal("la regla de eventos sigue apuntando al dispositivo borrado")
	}

	// Un router sale del plan: fuera del grupo, sin borrar el dispositivo.
	pl := plan()
	pl.Routers = pl.Routers[:1]
	res4 := p.Sync(ctx, res3.Objects, pl)
	if res4.Failed() != 0 || len(f.GroupByName("Horus · Routers").Devices) != 1 || f.DeviceByHost("198.51.100.11") == nil {
		t.Fatalf("salida del plan: %+v", res4.Steps)
	}
	if o, ok := res4.Find(librenms.KindRouterDevice, "r2"); !ok || o.Active || !o.CreatedByHorus {
		t.Fatalf("router retirado = %+v %v", o, ok)
	}
}

func stringify(m map[string]any) string {
	var b strings.Builder
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			b.WriteString("{")
			for _, k := range []string{"id", "field", "type", "input", "operator", "value", "condition", "rules"} {
				if y, ok := x[k]; ok {
					b.WriteString(`"` + k + `":`)
					walk(y)
					b.WriteString(",")
				}
			}
			b.WriteString("}")
		case []any:
			for _, y := range x {
				walk(y)
			}
		case string:
			b.WriteString(`"` + x + `"`)
		}
	}
	walk(m)
	return strings.ReplaceAll(b.String(), ",}", "}")
}

func TestSyncNeverTouchesForeignObjects(t *testing.T) {
	f := librenmsfake.New(t, token)
	c := client(f, token)
	ctx := context.Background()
	// La instancia ya tiene un dispositivo con la IP de gestión del router.
	if _, err := c.AddDevice(ctx, librenms.NewDevice{Hostname: "198.51.100.10", Community: "public"}); err != nil {
		t.Fatal(err)
	}
	res := librenms.NewProvisioner(c).Sync(ctx, nil, plan())
	o, ok := res.Find(librenms.KindRouterDevice, "r1")
	if !ok || o.CreatedByHorus || res.Failed() != 0 {
		t.Fatalf("adoptado = %+v %+v", o, res.Steps)
	}
	// Al salir del plan se saca del grupo y Horus deja de seguirlo (nunca se borra).
	pl := plan()
	pl.Routers = nil
	pl.Routers = []librenms.Target{}
	res2 := librenms.NewProvisioner(c).Sync(ctx, res.Objects, pl)
	if _, ok := res2.Find(librenms.KindRouterDevice, "r1"); ok || f.DeviceByHost("198.51.100.10") == nil {
		t.Fatal("dispositivo ajeno borrado o aún seguido")
	}
}

func TestSyncOperationsModel(t *testing.T) {
	f := librenmsfake.New(t, token)
	f.Operations, f.DefaultTransports = true, false
	p := librenms.NewProvisioner(client(f, token))
	res := p.Sync(context.Background(), nil, plan())
	if res.Failed() != 0 || len(res.Warnings) == 0 || !strings.Contains(strings.Join(res.Warnings, " "), "transportes") {
		t.Fatalf("sin transportes por defecto: %+v %v", res.Steps, res.Warnings)
	}
	// Con transportes por defecto, la siguiente pasada crea la operación una
	// sola vez y después la conserva.
	f.DefaultTransports = true
	res2 := p.Sync(context.Background(), res.Objects, plan())
	op := f.RuleByName("Horus · Router caído").OperationID
	if res2.Failed() != 0 || op == nil {
		t.Fatalf("operación = %v %+v", op, res2.Steps)
	}
	_ = p.Sync(context.Background(), res2.Objects, plan())
	if got := f.RuleByName("Horus · Router caído").OperationID; got == nil || *got != *op {
		t.Fatalf("operación cambiada: %v → %v", *op, got)
	}
}

func TestErrorsAreClassified(t *testing.T) {
	f := librenmsfake.New(t, token)
	ctx := context.Background()
	res := librenms.NewProvisioner(client(f, "token-incorrecto-123456")).Sync(ctx, nil, plan())
	if res.Err == nil || res.Err.Code != librenms.CodeAuthFailed || res.Err.Message != "Unauthenticated." {
		t.Fatalf("auth = %+v", res.Err)
	}
	if strings.Contains(res.Err.Error(), "token-incorrecto") {
		t.Fatal("el error lleva el token")
	}
	c := client(f, token)
	f.Unreachable["198.51.100.11"] = true
	res2 := librenms.NewProvisioner(c).Sync(ctx, nil, plan())
	var failed *librenms.Step
	for i := range res2.Steps {
		if res2.Steps[i].Action == librenms.ActionFailed {
			failed = &res2.Steps[i]
		}
	}
	if failed == nil || failed.Key != "r2" || failed.Code != librenms.CodeRejected || !strings.Contains(failed.Error, "snmp reachability") {
		t.Fatalf("router inalcanzable: %+v", res2.Steps)
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	_ = l.Close()
	_, err := librenms.New(librenms.Config{BaseURL: "http://" + addr, Token: token, Timeout: time.Second}).System(ctx)
	if librenms.CodeOf(err) != librenms.CodeConnectFailed {
		t.Fatalf("caído = %v", err)
	}
}

func TestLineAndEndToEnd(t *testing.T) {
	line := librenms.Line(librenms.Event{Severity: "high", EventType: "finding_opened", Subject: "Hallazgo abierto",
		Text: "Escaneo\nsaliente | 5 min", Link: "https://horus.example/x", DeliveryID: "d-1"})
	if line != "HORUS HIGH finding_opened Hallazgo abierto | Escaneo saliente / 5 min | https://horus.example/x | id=d-1" {
		t.Fatalf("línea = %q", line)
	}
	long := librenms.Line(librenms.Event{Severity: "info", EventType: "x", Subject: strings.Repeat("á", 2000), DeliveryID: "d-2"})
	if len([]rune(long)) > librenms.MaxLine || !strings.HasSuffix(long, "| id=d-2") {
		t.Fatalf("línea larga = %d runas", len([]rune(long)))
	}

	f := librenmsfake.New(t, token)
	f.Offset = -6 * time.Hour
	c := client(f, token)
	ctx := context.Background()
	res := librenms.NewProvisioner(c).Sync(ctx, nil, plan())
	dev, _ := res.Find(librenms.KindDevice, librenms.KeyHorusDevice)
	rule, _ := res.Find(librenms.KindRule, librenms.KeyHorusEvent)
	off, err := c.Calibrate(ctx, dev.RemoteID, time.Now)
	if err != nil || off != -360 {
		t.Fatalf("calibración = %d %v", off, err)
	}
	h := librenms.HorusTarget{Host: dev.Hostname, DeviceID: dev.RemoteID, OffsetMinutes: off}
	ev := librenms.Event{Severity: "info", EventType: "test", Subject: "Prueba", DeliveryID: "prueba-1"}
	r := c.EndToEnd(ctx, h, rule.RemoteID, ev, librenms.TestOptions{Wait: 2 * time.Second, Every: 100 * time.Millisecond})
	if !r.OK || len(r.Steps) != 5 {
		t.Fatalf("prueba = %+v", r)
	}
	f.Lock()
	got := f.Syslogs[len(f.Syslogs)-1]
	f.Unlock()
	local := time.Now().UTC().Add(-6 * time.Hour)
	ts, _ := time.Parse(time.DateTime, got.Timestamp)
	if d := ts.Sub(local); d > time.Minute || d < -time.Minute || got.Program != "HORUS" {
		t.Fatalf("línea guardada = %+v", got)
	}
	// Syslog desactivado: falla en syslog_stored con el código preciso.
	f.SyslogEnabled = false
	ev.DeliveryID = "prueba-2"
	r = c.EndToEnd(ctx, h, rule.RemoteID, ev, librenms.TestOptions{Wait: 300 * time.Millisecond, Every: 100 * time.Millisecond})
	if r.OK || r.FailedStep != librenms.StepSyslogStored || r.Code != librenms.CodeSyslogNotStored {
		t.Fatalf("sin syslog = %+v", r)
	}
	// Regla borrada en LibreNMS.
	f.Lock()
	delete(f.Rules, int(rule.RemoteID))
	f.Unlock()
	r = c.EndToEnd(ctx, h, rule.RemoteID, ev, librenms.TestOptions{Wait: time.Millisecond})
	if r.FailedStep != librenms.StepRule || r.Code != librenms.CodeRuleMissing {
		t.Fatalf("sin regla = %+v", r)
	}
	// Token inválido.
	r = client(f, "otro-token-no-valido-1234").EndToEnd(ctx, h, rule.RemoteID, ev, librenms.TestOptions{})
	if r.FailedStep != librenms.StepAuth || r.Code != librenms.CodeAuthFailed {
		t.Fatalf("auth = %+v", r)
	}
}

func TestInfraOnlyHook(t *testing.T) {
	f := librenmsfake.New(t, token)
	p := librenms.NewProvisioner(client(f, token))
	ctx := context.Background()
	res := p.Sync(ctx, nil, librenms.Plan{InfraOnly: true, Infra: []librenms.Target{{Key: "dns-1", Name: "DNS primario", Host: "192.0.2.53"}}})
	g := f.GroupByName("Horus · Infraestructura")
	if res.Failed() != 0 || g == nil || len(g.Devices) != 1 || len(f.Rules) != 0 || f.GroupByName("Horus · Routers") != nil {
		t.Fatalf("infra: %+v", res.Steps)
	}
	res2 := p.Sync(ctx, res.Objects, librenms.Plan{InfraOnly: true, Infra: []librenms.Target{}})
	if len(f.GroupByName("Horus · Infraestructura").Devices) != 0 || f.DeviceByHost("192.0.2.53") == nil || res2.Failed() != 0 {
		t.Fatalf("infra vacía: %+v", res2.Steps)
	}
}
