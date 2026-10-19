import type {
  InterfaceSample,
  ManagementSubnet,
  Router,
  SwitchFlowTop,
  SwitchNeighbor,
  SwitchOnboarding,
  SwitchPort,
  SwitchPorts,
  SwitchStatus,
} from '~~/types/api'
import type { MockTenant } from './data'
import { forbidden, ifMatch, json, notFound, paginate, problem, randomHex } from './http'
import { findSite, routersOf, sitesOf } from './inventory'
import { hash, iso, mockUuid, rng } from './random'
import type { TenantRouteContext } from './tenant-routes'

/**
 * Switches de monitoreo (contrato v0: `devices.yaml` switch-onboarding y `snmp.yaml` tag switches).
 * Mismas reglas que el servidor: un switch es solo de monitoreo (nunca principal, siempre directo por
 * la red de gestión), plataforma routeros | swos | vrp (SwOS = SNMP v2c con aviso), sFlow solo en la
 * serie S (fuente secundaria), elección de puertos con máximo 64 e If-Match, uplinks y vecinos LLDP.
 * La verificación SNMP se simula: el switch pasa a `snmp_verified` 3 s después del alta.
 *
 * Simulación (localStorage): `horus.mock.switchVerifyMs` cambia esos 3 s.
 *
 * Vía router (ADR-0032): el alta con `connection_mode=via_router` exige un router del ISP con túnel
 * (wireguard | ipsec | gre), asigna una dirección Horus (10.255.200.x, nunca la IP privada) y
 * devuelve la configuración del router del sitio; la misma IP privada no se repite por router.
 * Redes de gestión por router en memoria. `horus.mock.viaTunnelDown = 1` simula el túnel del
 * router caído: el switch queda `unreachable / via_router_tunnel_down`.
 */

type Platform = 'routeros' | 'swos' | 'vrp'

interface PortDef {
  ifIndex: number
  name: string
  alias: string | null
  speed: number
  oper: 'up' | 'down'
  admin: boolean
  /** Vecino LLDP (null = ninguno). */
  neighbor: { sys: string; port: string; known: string | null; mgmt: string | null } | null
  type?: string
}

interface MockSwitch {
  router: Router
  platform: Platform
  ports: PortDef[]
  /** Elección guardada (null = por defecto). */
  selection: { version: number; at: string; by: string; ports: Map<string, boolean> } | null
  verifyAt: number | null
}

const MAX_PORTS = 64
const store = new Map<string, MockSwitch[]>()
const subnetStore = new Map<string, ManagementSubnet[]>()
let viaSeq = 0

function viaTunnelDown() {
  try {
    return globalThis.localStorage?.getItem('horus.mock.viaTunnelDown') === '1'
  } catch {
    return false
  }
}

function verifyMs() {
  try {
    const v = Number(globalThis.localStorage?.getItem('horus.mock.switchVerifyMs'))
    return Number.isFinite(v) && v > 0 ? v : 3000
  } catch {
    return 3000
  }
}

function portsFor(platform: Platform, uplinkTo: string): PortDef[] {
  if (platform === 'vrp') {
    return [
      {
        ifIndex: 54,
        name: 'XGigabitEthernet0/0/1',
        alias: `Uplink a ${uplinkTo}`,
        speed: 10e9,
        oper: 'up',
        admin: true,
        neighbor: { sys: uplinkTo, port: 'sfp-sfpplus1', known: uplinkTo, mgmt: '10.255.3.17' },
      },
      {
        ifIndex: 55,
        name: 'XGigabitEthernet0/0/2',
        alias: null,
        speed: 10e9,
        oper: 'down',
        admin: false,
        neighbor: null,
      },
      {
        ifIndex: 6,
        name: 'GigabitEthernet0/0/1',
        alias: 'AP torre norte',
        speed: 1e9,
        oper: 'up',
        admin: true,
        neighbor: { sys: 'ap-torre-norte', port: 'eth0', known: null, mgmt: null },
      },
      {
        ifIndex: 7,
        name: 'GigabitEthernet0/0/2',
        alias: null,
        speed: 1e9,
        oper: 'up',
        admin: true,
        neighbor: null,
      },
      {
        ifIndex: 8,
        name: 'GigabitEthernet0/0/3',
        alias: null,
        speed: 1e9,
        oper: 'down',
        admin: true,
        neighbor: null,
      },
      {
        ifIndex: 9,
        name: 'GigabitEthernet0/0/4',
        alias: 'OLT puerto 1',
        speed: 1e9,
        oper: 'up',
        admin: true,
        neighbor: null,
      },
      {
        ifIndex: 60,
        name: 'Eth-Trunk1',
        alias: 'LAG a sw-acceso-2',
        speed: 2e9,
        oper: 'up',
        admin: true,
        type: 'ieee8023adLag',
        neighbor: { sys: 'sw-acceso-2', port: 'Eth-Trunk1', known: null, mgmt: '10.20.0.6' },
      },
    ]
  }
  const sfp = platform === 'swos' ? 'SFP+1' : 'sfp-sfpplus1'
  const eth = (i: number) => (platform === 'swos' ? `Port${i}` : `ether${i}`)
  return [
    {
      ifIndex: 25,
      name: sfp,
      alias: 'Uplink',
      speed: 10e9,
      oper: 'up',
      admin: true,
      neighbor:
        platform === 'swos'
          ? null
          : { sys: uplinkTo, port: 'sfp-sfpplus2', known: uplinkTo, mgmt: null },
    },
    ...Array.from({ length: 6 }, (_, i): PortDef => ({
      ifIndex: i + 1,
      name: eth(i + 1),
      alias: i === 2 ? 'Cliente edificio A' : null,
      speed: 1e9,
      oper: i === 4 ? 'down' : 'up',
      admin: true,
      neighbor: null,
    })),
  ]
}

function newRouter(
  tenant: MockTenant,
  id: string,
  siteId: string,
  name: string,
  platform: Platform,
  at: string,
  mgmt: string | null,
  state: Router['onboarding_state'],
  sflow: boolean,
): Router {
  return {
    id,
    tenant_id: tenant.tenant_id,
    created_at: at,
    updated_at: at,
    version: 1,
    site_id: siteId,
    name,
    display_name: null,
    is_primary: false,
    vendor: platform === 'vrp' ? 'huawei' : 'mikrotik',
    model: null,
    routeros_version: null,
    tags: [],
    admin_state: 'active',
    onboarding_state: state,
    routeros_version_detected: null,
    routeros_version_supported: null,
    tunnel_address: null,
    wireguard_peer_id: null,
    connection_mode: 'direct',
    management_address: mgmt,
    protection_level: 'unencrypted_private',
    tunnel_remote_address: null,
    tunnel_remote_id: null,
    warnings: [],
    credentials: [],
    device_type: 'switch',
    switch_platform: platform,
    sflow_enabled: sflow,
    via_router_id: null,
    via_address: null,
    via_router: null,
  }
}

/** Switches del ISP (dos de partida en el primer nodo del ISP de demostración). */
export function switchesOf(tenant: MockTenant, now: Date): MockSwitch[] {
  let list = store.get(tenant.tenant_id)
  if (!list) {
    list = []
    const site = sitesOf(tenant)[0]
    if (site && tenant.sites.length) {
      const at = iso(now.getTime() - 20 * 86_400_000)
      list.push(
        {
          router: newRouter(
            tenant,
            mockUuid('0194a5a0', (hash(tenant.tenant_id) % 1000) + 1),
            site.id,
            'sw-core-1',
            'vrp',
            at,
            '10.20.0.5',
            'exporting',
            true,
          ),
          platform: 'vrp',
          ports: portsFor('vrp', 'rt-centro-01'),
          selection: null,
          verifyAt: null,
        },
        {
          router: newRouter(
            tenant,
            mockUuid('0194a5a0', (hash(tenant.tenant_id) % 1000) + 2),
            site.id,
            'sw-acceso-2',
            'swos',
            at,
            '10.20.0.6',
            'snmp_verified',
            false,
          ),
          platform: 'swos',
          ports: portsFor('swos', 'sw-core-1'),
          selection: null,
          verifyAt: null,
        },
      )
    }
    store.set(tenant.tenant_id, list)
  }
  for (const s of list) {
    if (
      s.verifyAt !== null &&
      now.getTime() >= s.verifyAt &&
      s.router.onboarding_state === 'pending_configuration'
    ) {
      s.router = {
        ...s.router,
        onboarding_state: 'snmp_verified',
        version: s.router.version + 1,
        updated_at: iso(now.getTime()),
      }
      s.verifyAt = null
    }
  }
  return list
}

/** Elección vigente: la guardada o la de por defecto (uplinks sugeridos + puertos con descripción). */
function choices(s: MockSwitch): Map<string, boolean> {
  if (s.selection) return s.selection.ports
  const out = new Map<string, boolean>()
  for (const p of s.ports) {
    const suggested = !!p.neighbor?.known
    if (suggested || p.alias) out.set(p.name, suggested)
  }
  return out
}

function sample(s: MockSwitch, p: PortDef, now: Date, uplink: boolean): InterfaceSample | null {
  if (!choices(s).has(p.name)) return null
  const live = p.oper === 'up'
  const r = rng(hash(p.name + s.router.id + Math.floor(now.getTime() / 60_000)))
  const base = (uplink ? 0.55 : 0.12) * p.speed
  const rx = live ? Math.round(base * (0.8 + r() * 0.4)) : null
  const tx = live ? Math.round(base * 0.35 * (0.8 + r() * 0.4)) : null
  const errs = p.name.includes('0/0/4') ? 17 : 0
  return {
    interface_id: mockUuid('0194a5b0', hash(s.router.id + p.name)),
    if_index: p.ifIndex,
    name: p.name,
    alias: p.alias,
    uplink,
    oper_status: p.oper,
    speed_bps: p.speed,
    rx_bps: rx,
    tx_bps: tx,
    rx_pps: rx === null ? null : Math.round(rx / 5600),
    tx_pps: tx === null ? null : Math.round(tx / 5600),
    rx_errors_delta: live ? errs : null,
    tx_errors_delta: live ? 0 : null,
    rx_drops_delta: live ? (errs ? 4 : 0) : null,
    tx_drops_delta: live ? 0 : null,
    utilization_rx: rx === null ? null : Math.round((rx / p.speed) * 1000) / 1000,
    utilization_tx: tx === null ? null : Math.round((tx / p.speed) * 1000) / 1000,
    counter_discontinuity: false,
  }
}

function neighborOf(p: PortDef): SwitchNeighbor[] {
  if (!p.neighbor) return []
  return [
    {
      local_if_index: p.ifIndex,
      local_port: p.name,
      sys_name: p.neighbor.sys,
      port_id: p.neighbor.port,
      port_desc: p.neighbor.port,
      chassis_id: '48:8F:5A:11:22:33',
      mgmt_address: p.neighbor.mgmt,
      platform: null,
      source: 'lldp',
      known_device: p.neighbor.known,
    },
  ]
}

function flapsOf(p: PortDef) {
  return p.name === 'GigabitEthernet0/0/2' || p.name === 'ether5'
    ? { h: 5, d: 12 }
    : { h: 0, d: p.oper === 'down' ? 1 : 0 }
}

export function switchPorts(s: MockSwitch, now: Date): SwitchPorts {
  const ch = choices(s)
  const verified = s.router.onboarding_state !== 'pending_configuration'
  const data: SwitchPort[] = (verified ? s.ports : []).map((p) => {
    const uplink = ch.get(p.name) === true
    const f = flapsOf(p)
    return {
      interface_id: mockUuid('0194a5b0', hash(s.router.id + p.name)),
      if_index: p.ifIndex,
      name: p.name,
      alias: p.alias,
      type: p.type ?? 'ethernetCsmacd',
      speed_bps: p.speed,
      admin_up: p.admin,
      oper_status: p.oper,
      monitored: ch.has(p.name),
      uplink,
      suggested_uplink: !!p.neighbor?.known,
      flaps_1h: f.h,
      flaps_24h: f.d,
      last_change_at: f.d ? iso(now.getTime() - 25 * 60_000) : null,
      sample: sample(s, p, now, uplink),
      neighbors: neighborOf(p),
    }
  })
  return {
    router_id: s.router.id,
    platform: s.platform,
    interval_seconds: 60,
    max_ports: MAX_PORTS,
    monitored_count: data.filter((p) => p.monitored).length,
    selection: s.selection
      ? {
          configured: true,
          version: s.selection.version,
          updated_at: s.selection.at,
          updated_by: s.selection.by,
        }
      : { configured: false, version: null, updated_at: null, updated_by: null },
    discovered_at: verified ? iso(now.getTime() - 30 * 60_000) : null,
    neighbors_at: verified && s.platform !== 'swos' ? iso(now.getTime() - 30 * 60_000) : null,
    data,
  }
}

export function switchStatus(tenant: MockTenant, s: MockSwitch, now: Date): SwitchStatus {
  const site = findSite(tenant, s.router.site_id)
  const ports = switchPorts(s, now).data
  const monitored = ports.filter((p) => p.monitored)
  const uplinks = monitored.filter((p) => p.uplink)
  const down = uplinks.filter((p) => p.oper_status !== 'up' && p.admin_up !== false).length
  const flapping = monitored.filter((p) => p.flaps_1h >= 4)
  const verified = s.router.onboarding_state !== 'pending_configuration'
  const swos = s.platform === 'swos'
  const unreachable = verified && s.router.connection_mode === 'via_router' && viaTunnelDown()
  const status = !verified
    ? 'unknown'
    : unreachable
      ? 'unreachable'
      : down
        ? 'warning'
        : flapping.some((p) => p.uplink)
          ? 'warning'
          : 'online'
  const reason = !verified
    ? null
    : unreachable
      ? 'via_router_tunnel_down'
      : down
        ? 'interface_down'
        : status === 'warning'
          ? 'interface_flapping'
          : 'ok'
  const t = now.getTime()
  const up = uplinks[0]
  return {
    router_id: s.router.id,
    site_id: s.router.site_id,
    name: s.router.name,
    site_name: site?.name ?? '',
    status,
    observed_state: verified
      ? status === 'warning' || status === 'unreachable'
        ? status
        : 'online'
      : null,
    reason,
    since: verified ? iso(t - 6 * 3_600_000) : null,
    last_observed_at: verified ? iso(t - 20_000) : null,
    signals: {
      icmp: verified && s.router.connection_mode !== 'via_router' ? 'ok' : 'unknown',
      snmp: verified && !unreachable ? 'ok' : 'unknown',
      wireguard: 'unknown',
      exporter: s.router.sflow_enabled ? 'exporting' : null,
    },
    device: verified
      ? {
          sampled_at: iso(t - 20_000),
          uptime_seconds: 30 * 86_400,
          cpu_percent: swos ? null : 12,
          cpu_max_core_percent: swos ? null : 12,
          memory_percent: swos ? null : 45,
          temperature_celsius: swos ? 39.5 : 41,
          voltage: swos ? 24 : null,
          model:
            s.platform === 'vrp' ? 'S5735-L48T4X-A1' : swos ? 'CSS326-24G-2S+' : 'CRS326-24G-2S+',
          firmware: s.platform === 'vrp' ? 'V200R021C10SPC600' : swos ? null : '7.16.2',
          sensors: [],
        }
      : { sampled_at: null },
    uplink: up?.sample
      ? {
          interface_id: up.interface_id,
          name: up.name,
          rx_bps: up.sample.rx_bps,
          tx_bps: up.sample.tx_bps,
          speed_bps: up.speed_bps,
          utilization: Math.max(up.sample.utilization_rx ?? 0, up.sample.utilization_tx ?? 0),
        }
      : null,
    interfaces_up: verified ? monitored.filter((p) => p.oper_status === 'up').length : null,
    interfaces_total: verified ? monitored.length : null,
    thresholds: [],
    last_flow_at: s.router.sflow_enabled ? iso(t - 30_000) : null,
    connection_mode: s.router.connection_mode,
    protection_level: 'unencrypted_private',
    device_type: 'switch',
    platform: s.platform,
    sflow_enabled: s.router.sflow_enabled,
    ports_monitored: monitored.length,
    uplinks: uplinks.length,
    uplinks_down: down,
    ports_flapping: flapping.length,
    neighbors_count: ports.reduce((n, p) => n + p.neighbors.length, 0),
  }
}

function onboardingFor(
  s: MockSwitch,
  mgmt: string,
  sflow: boolean,
  rate: number,
  via: Router | null = null,
): SwitchOnboarding {
  const user = `horus-${s.router.id.slice(-8)}`
  const auth = randomHex(24)
  const priv = randomHex(24)
  const notes = [
    'Switch de MONITOREO: Horus solo lee (SNMP de solo lectura). Nunca escribe en el switch.',
  ]
  const warnings: string[] = []
  let config: string
  let language: SwitchOnboarding['language'] = 'routeros'
  if (s.platform === 'routeros') {
    config = `/snmp community add name=${user} security=private \\\n    authentication-protocol=SHA1 authentication-password="${auth}" \\\n    encryption-protocol=AES encryption-password="${priv}" \\\n    addresses={HORUS_POLLER_IP} read-access=yes write-access=no comment="horus"\n/snmp set enabled=yes\n`
  } else if (s.platform === 'swos') {
    language = 'swos_web'
    config = `SwOS (interfaz web del switch ${s.router.name}):\n1. Pestaña SNMP: Enabled = sí; Community = ${randomHex(32)}.\n2. Pestaña System: Allow From = {RED_DE_GESTION}.\n3. Pulse «Apply All».\n`
    warnings.push(
      'SwOS solo admite SNMP v1/v2c: la comunidad viaja EN CLARO. Úsela solo en la red de gestión, aislada de clientes e Internet.',
    )
  } else {
    language = 'vrp'
    config = `system-view\nacl number 2991\n rule 5 permit source {HORUS_POLLER_IP} 0\n rule 10 deny\n quit\nsnmp-agent\nsnmp-agent usm-user v3 ${user} group horus-ro\nsnmp-agent usm-user v3 ${user} authentication-mode sha2-256\n${auth}\n${auth}\nsnmp-agent usm-user v3 ${user} privacy-mode aes128\n${priv}\n${priv}\nreturn\n`
    notes.push('PENDIENTE DE VALIDACIÓN EN EQUIPO REAL: sintaxis de VRP de la serie S.')
  }
  const sflowConfig = sflow
    ? `system-view\nsflow agent ip ${mgmt}\nsflow collector 1 ip {HORUS_COLLECTOR_IP} udp-port 2055 description horus\ninterface {PUERTO_SFLOW}\n sflow flow-sampling collector 1\n sflow flow-sampling rate ${rate}\n sflow flow-sampling inbound\n quit\nreturn\n`
    : null
  if (sflow)
    notes.push(
      'sFlow del switch = fuente SECUNDARIA: nunca suma al tráfico de clientes ni de nodos.',
    )
  const horus = s.router.via_address
  const transport = (via?.connection_mode ?? 'wireguard') as 'wireguard' | 'ipsec' | 'gre'
  const tag = `horus-via ${s.router.id}`
  const inIf =
    transport === 'ipsec'
      ? 'ipsec-policy=in,ipsec '
      : `in-interface=${transport === 'gre' ? 'horus-gre' : 'wg-horus'} `
  const routerConfig =
    via && horus
      ? {
          language: (via.vendor === 'huawei' ? 'vrp' : 'routeros') as 'vrp' | 'routeros',
          title: `Switch vía router · ${transport}`,
          config:
            via.vendor === 'huawei'
              ? `system-view\ninterface Tunnel10\n nat static global ${horus} inside ${mgmt}\n traffic-filter inbound acl 3991\n quit\nreturn\n`
              : `# ===== Horus Flow · switch vía router · RouterOS v7 =====\n/ip firewall nat remove [find where comment="${tag}"]\n/ip firewall nat add chain=dstnat ${inIf}dst-address=${horus} action=netmap to-addresses=${mgmt} comment="${tag}"\n/ip firewall nat add chain=srcnat src-address=${mgmt} dst-address=10.255.0.0/24 action=netmap to-addresses=${horus} comment="${tag}"\n/ip firewall filter add chain=forward ${inIf}src-address=10.255.0.0/24 dst-address=${mgmt} protocol=udp dst-port=161 action=accept comment="${tag}"\n/ip firewall filter add chain=forward ${inIf}dst-address=${mgmt} action=drop comment="${tag}"\n# Deshacer: /ip firewall filter remove [find where comment="${tag}"]\n`,
          notes: [
            `Dirección Horus del switch: ${horus}. Horus nunca usa la IP privada ${mgmt} como identidad.`,
          ],
          warnings:
            transport === 'gre'
              ? [
                  'El túnel de este router es GRE (SIN CIFRADO): el sFlow del switch viaja en claro.',
                ]
              : [],
        }
      : null
  if (via) {
    config = config.replaceAll('{HORUS_POLLER_IP}', '10.255.0.1')
    notes.push(
      `Vía el router ${via.name} por su túnel ${transport}: Horus consulta ${horus} y el router lo traduce a ${mgmt}.`,
    )
  }
  return {
    connection_mode: via ? 'via_router' : 'direct',
    path:
      via && horus
        ? {
            router_id: via.id,
            router_name: via.name,
            transport,
            protection_level: via.protection_level,
            horus_address: horus,
            block: null,
            poller_address: '10.255.0.1',
            collector_address: '10.255.0.1',
            collector_port: 6343,
            tunnel_status: 'active',
          }
        : null,
    router_config: routerConfig,
    router_id: s.router.id,
    platform: s.platform,
    language,
    snmp_version: s.platform === 'swos' ? '2c' : '3',
    management_address: mgmt,
    snmp_user: s.platform === 'swos' ? null : user,
    sflow_enabled: sflow,
    sampling_rate: sflow ? rate : null,
    config,
    sflow_config: sflowConfig,
    notes,
    placeholders: via
      ? []
      : [{ key: '{HORUS_POLLER_IP}', description: 'IP de origen del sondeo SNMP de Horus.' }],
    warnings,
  }
}

const ADDRESS = /^(\d{1,3}\.){3}\d{1,3}$/

function validation(field: string, code: string, message: string) {
  return problem(
    422,
    'VALIDATION_FAILED',
    'Revisa los campos marcados',
    {},
    { errors: [{ field, code, message }] },
  )
}

/** Rutas de switches (antes que las de routers: `POST /routers` con device_type switch, etc.). */
export function switchRoutes(ctx: TenantRouteContext): Response | undefined {
  const { tenant, now } = ctx
  const list = () => switchesOf(tenant, now)
  const find = (id: string) => list().find((s) => s.router.id === id)
  const path = ctx.path
  const dt = ctx.url.searchParams.get('device_type')

  if (ctx.method === 'GET' && path === '/routers' && dt === 'switch') {
    if (!ctx.can('devices.read')) return forbidden()
    const sites = ctx.url.searchParams.get('site_id')?.split(',').filter(Boolean)
    const via = ctx.url.searchParams.get('via_router_id')
    return json(
      paginate(
        list()
          .filter((s) => !sites?.length || sites.includes(s.router.site_id))
          .filter((s) => !via || s.router.via_router_id === via)
          .map((s) => s.router),
        ctx.url,
      ),
    )
  }
  if (ctx.method === 'POST' && path === '/routers' && ctx.body.device_type === 'switch') {
    if (!ctx.can('devices.create')) return forbidden()
    const b = ctx.body as {
      site_id?: string
      name?: string
      switch_platform?: string
      is_primary?: boolean
    }
    if (!b.site_id || !findSite(tenant, b.site_id))
      return validation('site_id', 'NOT_FOUND', 'El nodo no existe.')
    const name = (b.name ?? '').trim()
    if (!name) return validation('name', 'REQUIRED', 'Nombre obligatorio.')
    if (!['routeros', 'swos', 'vrp'].includes(String(b.switch_platform))) {
      return validation('switch_platform', 'REQUIRED', 'Indique el sistema del switch.')
    }
    if (b.is_primary)
      return validation(
        'is_primary',
        'NOT_APPLICABLE',
        'Un switch nunca es el router principal del nodo.',
      )
    if (list().some((s) => s.router.name === name))
      return problem(409, 'ALREADY_EXISTS', 'Ya existe un equipo con ese nombre')
    const platform = b.switch_platform as Platform
    const id = mockUuid('0194a5c0', hash(name + now.getTime()))
    const sw: MockSwitch = {
      router: newRouter(
        tenant,
        id,
        b.site_id,
        name,
        platform,
        iso(now.getTime()),
        null,
        'pending_configuration',
        false,
      ),
      platform,
      ports: portsFor(platform, 'rt-centro-01'),
      selection: null,
      verifyAt: null,
    }
    list().push(sw)
    return json(sw.router, 201, { ETag: '"1"' })
  }
  const sm = path.match(/^\/routers\/([^/]+)\/management-subnets(?:\/([^/]+))?$/)
  if (sm) {
    const rid = sm[1]!
    if (find(rid))
      return problem(422, 'SWITCH_NOT_SUPPORTED', 'Las redes se declaran en el router.')
    if (!routersOf(tenant, now).some((r) => r.router.id === rid)) return notFound()
    const subs = subnetStore.get(rid) ?? []
    if (ctx.method === 'GET' && !sm[2]) {
      if (!ctx.can('devices.read')) return forbidden()
      return json({ data: subs })
    }
    if (!ctx.can('devices.update')) return forbidden()
    if (ctx.method === 'POST' && !sm[2]) {
      const prefix = String((ctx.body as { prefix?: string }).prefix ?? '').trim()
      const mm = prefix.match(/^(10|172|192)\.(\d+)\.(\d+)\.(\d+)\/(\d+)$/)
      const bits = mm ? Number(mm[5]) : 0
      if (!mm || bits < 22 || bits > 30)
        return validation('prefix', 'INVALID_VALUE', 'Red IPv4 privada de /22 a /30.')
      if (subs.some((x) => x.prefix === prefix))
        return problem(409, 'MANAGEMENT_SUBNET_OVERLAP', 'Se solapa con otra red del router.')
      const sub: ManagementSubnet = {
        id: mockUuid('0194a5d0', hash(rid + prefix)),
        router_id: rid,
        prefix,
        created_at: iso(now.getTime()),
      }
      subnetStore.set(rid, [...subs, sub])
      return json(sub, 201)
    }
    if (ctx.method === 'DELETE' && sm[2]) {
      if (!subs.some((x) => x.id === sm[2])) return notFound()
      subnetStore.set(
        rid,
        subs.filter((x) => x.id !== sm[2]),
      )
      return new Response(null, { status: 204 })
    }
    return
  }
  let m = path.match(/^\/routers\/([^/]+)(\/switch-onboarding)?$/)
  if (m) {
    const s = find(m[1]!)
    if (m[2]) {
      if (ctx.method !== 'POST') return
      if (!s) return problem(422, 'DEVICE_NOT_A_SWITCH', 'Este equipo es un router: use su alta.')
      if (!ctx.can('devices.credentials.write')) return forbidden()
      if (ctx.reauthRequired)
        return problem(
          403,
          'REAUTH_REQUIRED',
          'Vuelve a identificarte para generar la configuración',
        )
      const b = ctx.body as {
        management_address?: string | null
        connection_mode?: 'direct' | 'via_router' | null
        via_router_id?: string | null
        sflow?: boolean | null
        sampling_rate?: number | null
      }
      const mode = b.connection_mode ?? s.router.connection_mode
      let via: Router | null = null
      if (mode === 'via_router') {
        const vid = b.via_router_id ?? s.router.via_router_id
        if (!vid)
          return validation('via_router_id', 'REQUIRED', 'Elija el router del sitio del switch.')
        via =
          routersOf(tenant, now)
            .map((r) => r.router)
            .find(
              (r) => r.id === vid && ['wireguard', 'ipsec', 'gre'].includes(r.connection_mode),
            ) ?? null
        if (!via)
          return validation(
            'via_router_id',
            'VIA_ROUTER_INVALID',
            'Elija un router del mismo ISP con túnel activo con Horus.',
          )
      }
      const mgmt = b.management_address ?? s.router.management_address
      if (!mgmt)
        return validation('management_address', 'REQUIRED', 'Indique la IP de gestión del switch.')
      if (!ADDRESS.test(mgmt) || mgmt.startsWith('127.') || mgmt.startsWith('10.255.')) {
        return validation(
          'management_address',
          'INVALID_VALUE',
          'IP unicast del switch en la red de gestión (sin direcciones de Horus).',
        )
      }
      if (b.sflow && s.platform !== 'vrp')
        return validation(
          'sflow',
          'SFLOW_NOT_SUPPORTED',
          'Solo la serie S de Huawei exporta sFlow.',
        )
      const rate = b.sampling_rate ?? 4096
      if (rate < 256 || rate > 65535)
        return validation('sampling_rate', 'INVALID_VALUE', 'Entre 256 y 65535.')
      if (
        via &&
        list().some(
          (o) =>
            o !== s && o.router.via_router_id === via!.id && o.router.management_address === mgmt,
        )
      )
        return problem(
          409,
          'MANAGEMENT_ADDRESS_IN_USE',
          'Otro switch ya usa esa IP por este router.',
        )
      const sflow = b.sflow ?? s.router.sflow_enabled
      const changed =
        mgmt !== s.router.management_address ||
        mode !== s.router.connection_mode ||
        (via?.id ?? null) !== s.router.via_router_id
      s.router = {
        ...s.router,
        connection_mode: mode,
        via_router_id: via?.id ?? null,
        via_address: via ? (s.router.via_address ?? `10.255.200.${(viaSeq++ % 250) + 2}`) : null,
        via_router: via
          ? {
              id: via.id,
              name: via.name,
              connection_mode: via.connection_mode as 'wireguard' | 'ipsec' | 'gre',
              protection_level: via.protection_level,
            }
          : null,
        management_address: mgmt,
        sflow_enabled: sflow,
        version: s.router.version + 1,
        updated_at: iso(now.getTime()),
        onboarding_state: changed ? 'pending_configuration' : s.router.onboarding_state,
      }
      if (changed || s.router.onboarding_state === 'pending_configuration')
        s.verifyAt = now.getTime() + verifyMs()
      return json(onboardingFor(s, mgmt, sflow, rate, via), 201, { 'Cache-Control': 'no-store' })
    }
    if (!s) return
    if (ctx.method === 'GET') return json(s.router, 200, { ETag: `"${s.router.version}"` })
    if (ctx.method === 'PATCH') {
      if (!ctx.can('devices.update')) return forbidden()
      if (ifMatch(ctx.req) !== s.router.version)
        return problem(412, 'PRECONDITION_FAILED', 'El recurso cambió')
      const b = ctx.body as { name?: string; sflow_enabled?: boolean }
      if (b.sflow_enabled && s.platform !== 'vrp')
        return validation('sflow_enabled', 'SFLOW_NOT_SUPPORTED', 'Solo la serie S exporta sFlow.')
      s.router = {
        ...s.router,
        name: b.name ?? s.router.name,
        sflow_enabled: b.sflow_enabled ?? s.router.sflow_enabled,
        version: s.router.version + 1,
        updated_at: iso(now.getTime()),
      }
      return json(s.router, 200, { ETag: `"${s.router.version}"` })
    }
    if (ctx.method === 'DELETE') {
      if (!ctx.can('devices.delete')) return forbidden()
      store.set(
        tenant.tenant_id,
        list().filter((x) => x !== s),
      )
      return new Response(null, { status: 204 })
    }
    return
  }
  if (ctx.method === 'GET' && path === '/switches/status') {
    if (!ctx.can('devices.read')) return forbidden()
    const site = ctx.url.searchParams.get('site_id')
    const rows = list()
      .filter((s) => !site || s.router.site_id === site)
      .map((s) => switchStatus(tenant, s, now))
    return json({
      data: rows,
      meta: {
        poller: {
          healthy: true,
          last_cycle_at: iso(now.getTime() - 20_000),
          interval_seconds: 60,
          targets: rows.length,
          enabled: true,
        },
        generated_at: iso(now.getTime()),
      },
    })
  }
  m = path.match(/^\/switches\/([^/]+)\/(ports|neighbors|flows\/top)$/)
  if (!m) return
  const s = find(m[1]!)
  if (!s) return notFound('SWITCH_NOT_FOUND')
  const what = m[2]
  if (what === 'ports') {
    if (ctx.method === 'GET') {
      if (!ctx.can('devices.read')) return forbidden()
      const out = switchPorts(s, now)
      return json(out, 200, out.selection.version ? { ETag: `"${out.selection.version}"` } : {})
    }
    if (!ctx.can('snmp.manage')) return forbidden()
    if (ctx.method === 'DELETE') {
      s.selection = null
      return json(switchPorts(s, now))
    }
    if (ctx.method !== 'PUT') return
    const expect = ifMatch(ctx.req)
    if (expect !== null && expect !== (s.selection?.version ?? 0))
      return problem(412, 'PRECONDITION_FAILED', 'La elección cambió')
    const ports = (ctx.body.ports as { name: string; uplink?: boolean }[] | undefined) ?? null
    if (!ports) return validation('ports', 'REQUIRED', 'Lista de puertos a monitorear.')
    if (ports.length > MAX_PORTS)
      return validation('ports', 'TOO_MANY', `Máximo ${MAX_PORTS} puertos por switch.`)
    const names = new Set(s.ports.map((p) => p.name))
    const next = new Map<string, boolean>()
    for (const [i, p] of ports.entries()) {
      if (!names.has(p.name))
        return validation(`ports[${i}].name`, 'NOT_FOUND', 'El switch no tiene ese puerto.')
      next.set(p.name, !!p.uplink)
    }
    s.selection = {
      version: (s.selection?.version ?? 0) + 1,
      at: iso(now.getTime()),
      by: ctx.userId,
      ports: next,
    }
    const out = switchPorts(s, now)
    return json(out, 200, { ETag: `"${s.selection.version}"` })
  }
  if (what === 'neighbors') {
    if (!ctx.can('devices.read')) return forbidden()
    const ports = switchPorts(s, now)
    return json({ data: ports.data.flatMap((p) => p.neighbors), discovered_at: ports.neighbors_at })
  }
  if (!ctx.can('snmp.read')) return forbidden()
  const by = (ctx.url.searchParams.get('by') ?? 'src_ip') as SwitchFlowTop['by']
  const ifIndex = Number(ctx.url.searchParams.get('if_index') ?? 0)
  const minutes = Number(ctx.url.searchParams.get('window_minutes') ?? 60)
  const keys =
    by === 'protocol'
      ? ['6/443', '17/443', '6/80', '17/53', '6/22']
      : by === 'dst_ip'
        ? ['142.250.1.10', '157.240.25.35', '8.8.8.8', '1.1.1.1', '23.0.0.5']
        : ['10.20.1.9', '10.20.1.7', '10.20.3.15', '10.20.2.40', '10.20.1.33']
  const data = s.router.sflow_enabled
    ? keys.map((k, i) => ({
        key: k,
        bytes: String(Math.round((4.2e9 / (i + 1)) * (ifIndex ? 0.6 : 1))),
        packets: String(Math.round(3.1e6 / (i + 1))),
      }))
    : []
  const to = new Date(Math.floor(now.getTime() / 60_000) * 60_000 + 60_000)
  const top: SwitchFlowTop = {
    router_id: s.router.id,
    by,
    if_index: ifIndex || null,
    from: iso(to.getTime() - minutes * 60_000),
    to: iso(to.getTime()),
    total_bytes: String(data.reduce((n, d) => n + Number(d.bytes), 0)),
    samples: String(data.length * 40),
    sflow_enabled: s.router.sflow_enabled,
    source: 'secondary',
    data,
  }
  return json(top)
}

/** Vacía los switches simulados (pruebas). */
export function resetSwitchMocks() {
  subnetStore.clear()
  viaSeq = 0
  store.clear()
}
