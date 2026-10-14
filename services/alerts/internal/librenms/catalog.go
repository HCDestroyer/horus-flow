package librenms

import "strconv"

// Prefix marca todo objeto de LibreNMS creado por Horus: lo que no lo lleva
// (o no consta en Horus) nunca se modifica ni se borra.
const Prefix = "Horus · "

// Claves estables de los objetos que Horus aprovisiona (alerts.librenms_object.key).
const (
	KeyHorusDevice  = "horus"
	KeyGroupRouters = "routers"
	KeyGroupInfra   = "infra"

	KeyRouterDown   = "router_down"
	KeyCPUHigh      = "cpu_high"
	KeyMemoryHigh   = "memory_high"
	KeyPortDown     = "port_down"
	KeyReboot       = "reboot"
	KeyInfraDown    = "infra_down"
	KeyHorusEvent   = "horus_event"
	HorusDeviceName = "Horus Flow"
	// SyslogPrefix inicia cada línea de syslog de Horus: la regla
	// «Evento de seguridad Horus» busca syslog.msg REGEXP '^HORUS '.
	SyslogPrefix = "HORUS"
)

// Umbrales por defecto (CPU > 85 %, memoria ≥ 90 %).
// TODO(umbrales SNMP por ISP): cuando exista la configuración por tenant de
// umbrales SNMP, leerla aquí en lugar de estos valores (Thresholds se pasa
// ya como parámetro del plan).
const (
	DefaultCPUThreshold    = 85
	DefaultMemoryThreshold = 90
)

// Thresholds son los umbrales con que se crean las reglas de CPU y memoria.
type Thresholds struct {
	CPU    int
	Memory int
}

// Normalize aplica los valores por defecto y los límites (50–99).
func (t Thresholds) Normalize() Thresholds {
	if t.CPU < 50 || t.CPU > 99 {
		t.CPU = DefaultCPUThreshold
	}
	if t.Memory < 50 || t.Memory > 99 {
		t.Memory = DefaultMemoryThreshold
	}
	return t
}

// templateDef es una plantilla Blade de LibreNMS con su título de alerta y
// de recuperación ($alert->state == 0 en la recuperación; faults solo
// existe mientras la alerta está activa).
type templateDef struct {
	Key      string
	Name     string
	Title    string
	TitleRec string
	Body     string
}

// ruleDef es una regla de LibreNMS: target es el grupo (routers|infra) o el
// dispositivo Horus.
type ruleDef struct {
	Key      string
	Name     string
	Target   string // KeyGroupRouters | KeyGroupInfra | KeyHorusDevice
	Severity string
	Builder  Builder
	Notes    string
	Count    int
	Delay    string
	Interval string
}

const bladeFooter = `
Regla: {{ $alert->name }} · Severidad: {{ $alert->severity }}
Hora: {{ $alert->timestamp }} · ID: {{ $alert->uid }}
Aprovisionado por Horus Flow (no editar: Horus lo sobrescribe al sincronizar).`

func bladeState(active, recovered string) string {
	return "@if ($alert->state == 0)\n" + recovered + "\nDuración: {{ $alert->elapsed }}\n@else\n" + active + "\n@endif"
}

func templates() []templateDef {
	host := "{{ $alert->display }} ({{ $alert->hostname }})"
	return []templateDef{
		{Key: KeyRouterDown, Name: Prefix + "Router caído",
			Title: "Horus · Router caído: {{ $alert->display }}", TitleRec: "Horus · Router recuperado: {{ $alert->display }}",
			Body: bladeState("El router "+host+" no responde desde LibreNMS.\n@if ($alert->faults)\n@foreach ($alert->faults as $key => $value)\n- Motivo: {{ $value['status_reason'] ?? 'sin respuesta' }}\n@endforeach\n@endif",
				"El router "+host+" vuelve a responder.") + bladeFooter},
		{Key: KeyCPUHigh, Name: Prefix + "CPU alta",
			Title: "Horus · CPU alta en {{ $alert->display }}", TitleRec: "Horus · CPU normal en {{ $alert->display }}",
			Body: bladeState("Uso de CPU alto en "+host+":\n@if ($alert->faults)\n@foreach ($alert->faults as $key => $value)\n- {{ $value['processor_descr'] ?? 'CPU' }}: {{ $value['processor_usage'] ?? '?' }} %\n@endforeach\n@endif",
				"El uso de CPU de "+host+" volvió a la normalidad.") + bladeFooter},
		{Key: KeyMemoryHigh, Name: Prefix + "Memoria alta",
			Title: "Horus · Memoria alta en {{ $alert->display }}", TitleRec: "Horus · Memoria normal en {{ $alert->display }}",
			Body: bladeState("Uso de memoria alto en "+host+":\n@if ($alert->faults)\n@foreach ($alert->faults as $key => $value)\n- {{ $value['mempool_descr'] ?? 'Memoria' }}: {{ $value['mempool_perc'] ?? '?' }} %\n@endforeach\n@endif",
				"El uso de memoria de "+host+" volvió a la normalidad.") + bladeFooter},
		{Key: KeyPortDown, Name: Prefix + "Interfaz caída",
			Title: "Horus · Interfaz caída en {{ $alert->display }}", TitleRec: "Horus · Interfaz recuperada en {{ $alert->display }}",
			Body: bladeState("Interfaces caídas (administrativamente arriba) en "+host+":\n@if ($alert->faults)\n@foreach ($alert->faults as $key => $value)\n- {{ $value['ifName'] ?? $value['ifDescr'] ?? 'interfaz' }} {{ $value['ifAlias'] ?? '' }}\n@endforeach\n@endif",
				"Las interfaces de "+host+" vuelven a estar arriba.") + bladeFooter},
		{Key: KeyReboot, Name: Prefix + "Reinicio",
			Title: "Horus · Reinicio de {{ $alert->display }}", TitleRec: "Horus · {{ $alert->display }} estable tras el reinicio",
			Body: bladeState("El router "+host+" se reinició (tiempo encendido: {{ $alert->uptime_short }}).",
				"El router "+host+" lleva más de 5 minutos encendido tras el reinicio.") + bladeFooter},
		{Key: KeyInfraDown, Name: Prefix + "Servicio de infraestructura caído",
			Title: "Horus · Servicio caído: {{ $alert->display }}", TitleRec: "Horus · Servicio recuperado: {{ $alert->display }}",
			Body: bladeState("El servicio de infraestructura "+host+" no responde al ping desde LibreNMS.",
				"El servicio de infraestructura "+host+" vuelve a responder.") + bladeFooter},
		{Key: KeyHorusEvent, Name: Prefix + "Evento de seguridad Horus",
			Title: "Horus · Evento de seguridad", TitleRec: "Horus · Sin eventos nuevos de seguridad",
			Body: bladeState("Horus Flow informó eventos (últimos 5 minutos):\n@if ($alert->faults)\n@foreach ($alert->faults as $key => $value)\n- {{ $value['msg'] ?? '' }}\n@endforeach\n@endif",
				"Horus Flow no informó eventos nuevos en los últimos 5 minutos.") + bladeFooter},
	}
}

func rules(th Thresholds) []ruleDef {
	devUp := Cond("macros.device_up", "integer", "radio", "equal", "1")
	return []ruleDef{
		{Key: KeyRouterDown, Name: Prefix + "Router caído", Target: KeyGroupRouters, Severity: "critical",
			Builder: All(Cond("macros.device_down", "integer", "radio", "equal", "1")), Count: 1, Delay: "0m", Interval: "30m",
			Notes: "Router del grupo «Horus · Routers» sin respuesta."},
		{Key: KeyCPUHigh, Name: Prefix + "CPU alta", Target: KeyGroupRouters, Severity: "warning",
			Builder: All(Cond("processors.processor_usage", "string", "text", "greater", strconv.Itoa(th.CPU)), devUp),
			Count: 1, Delay: "10m", Interval: "1h", Notes: "Umbral de CPU de Horus: " + strconv.Itoa(th.CPU) + " %."},
		{Key: KeyMemoryHigh, Name: Prefix + "Memoria alta", Target: KeyGroupRouters, Severity: "warning",
			Builder: All(Cond("mempools.mempool_class", "string", "text", "equal", "system"),
				Cond("mempools.mempool_perc", "string", "number", "greater_or_equal", strconv.Itoa(th.Memory)), devUp),
			Count: 1, Delay: "10m", Interval: "1h", Notes: "Umbral de memoria de Horus: " + strconv.Itoa(th.Memory) + " %."},
		{Key: KeyPortDown, Name: Prefix + "Interfaz caída", Target: KeyGroupRouters, Severity: "warning",
			Builder: All(Cond("macros.port_down", "integer", "radio", "equal", "1"), devUp), Count: 1, Delay: "5m", Interval: "1h",
			Notes: "Interfaz operativamente caída y administrativamente arriba."},
		{Key: KeyReboot, Name: Prefix + "Reinicio", Target: KeyGroupRouters, Severity: "warning",
			Builder: All(Cond("devices.uptime", "string", "text", "less", "300"), Cond("macros.device", "integer", "radio", "equal", "1")),
			Count: 1, Delay: "0m", Interval: "1h", Notes: "Tiempo encendido menor de 5 minutos."},
		{Key: KeyInfraDown, Name: Prefix + "Servicio de infraestructura caído", Target: KeyGroupInfra, Severity: "critical",
			Builder: All(Cond("macros.device_down", "integer", "radio", "equal", "1")), Count: 1, Delay: "0m", Interval: "30m",
			Notes: "Servicio del grupo «Horus · Infraestructura» sin respuesta al ping."},
		{Key: KeyHorusEvent, Name: Prefix + "Evento de seguridad Horus", Target: KeyHorusDevice, Severity: "critical",
			Builder: All(Cond("syslog.timestamp", "datetime", "text", "greater_or_equal", "`macros.past_5m`"),
				Cond("syslog.msg", "string", "text", "regex", "^"+SyslogPrefix+" ")),
			Count: 1, Delay: "0m", Interval: "5m", Notes: "Líneas de syslog «HORUS …» que Horus Flow envía por la API (syslogsink)."},
	}
}
