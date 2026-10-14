import type {
  RouterThresholdValues,
  RouterThresholds,
  InterfaceSample,
  RouterDeviceSample,
  RouterEffectiveStatus,
  RouterInterface,
  RouterInterfaceList,
  RouterLiveMetrics,
  RouterSignals,
  RouterStatus,
  RouterStatusList,
  RouterThresholdHit,
  SeriesResponse,
} from '~~/types/api'
import type { RealtimeEventMessage, RealtimeStateMessage } from '~/utils/realtime'
import { analyticsDown, analyticsMeta, rangeMs, stepFor } from './analytics-meta'
import { dailyCurve, tenantScale } from './base'
import type { MockTenant } from './data'
import { forbidden, json, notFound, problem } from './http'
import { routersOf, seededSites, type MockRouter } from './inventory'
import { hash, iso, mockUuid, rng } from './random'
import { mockState } from './state'
import type { TenantRouteContext } from './tenant-routes'

/**
 * Monitoreo de routers simulado (EP-08, `snmp.yaml` + series de `analytics.yaml`): estado
 * efectivo con razón y señales, última muestra (CPU, memoria, temperatura, uptime, latencia,
 * enlace de subida), interfaces descubiertas y series históricas, derivados del mismo
 * inventario que las fichas (`routersOf`), para que ninguna pantalla contradiga a otra.
 *
 * Reparto determinista por ISP (api.md §2.6): la mayoría `online`; el segundo router activo
 * `warning/cpu_high`; el tercero `stale` (última observación hace 9 min); el cuarto
 * `critical/temperature_high` solo en el escenario degradado (y el primero de Red Andina,
 * para tener siempre un ISP con un crítico que mirar); el router con
 * exportador silencioso → `offline` (túnel caído u host inalcanzable); el pendiente de alta →
 * `unknown`. Escenario `snmp-off` (`localStorage['horus.mock.scenario']`): el sondeador está
 * desactivado y todo pasa a `stale`/`unknown`. `POST /routers/{id}/poll` encola un sondeo que
 * concluye 2 s después: cambia `sampled_at` y se avisa por `routers.status` y
 * `router.{id}.metrics` (ver `mocks/realtime.ts`).
 */

const DAY = 86_400_000
const MINUTE = 60_000
const POLL_INTERVAL = 60
const POLL_DELAY_MS = 2000
const UPLINK_SPEED = 10e9

export interface SnmpOptions {
  degraded?: boolean
  stepMs?: number
  scenario?: string
}

type Profile = 'online' | 'warning' | 'critical' | 'offline' | 'stale' | 'unknown'

export interface SnmpRouter {
  item: MockRouter
  profile: Profile
  status: RouterStatus
  /** Tráfico del enlace de subida ahora (bps, bajada hacia clientes = rx del uplink). */
  rx: number
  tx: number
  /** Semilla estable del router. */
  seed: number
}

const SILENT_SUFFIX = 'snmp'

/** Ciclo del sondeador: el último concluyó hace 20 s. */
function lastCycleAt(t: number) {
  return t - 20_000
}

/** Rango de carga diaria del nodo (0,22–1) para que CPU y tráfico respiren a la vez. */
function load(at: number) {
  return dailyCurve(1, at)
}

function bootAt(seed: number) {
  const r = rng(seed)
  return Date.UTC(2026, 5, 1) + Math.floor(r() * 120 * DAY)
}

/** Parte del tráfico del ISP que pasa por cada nodo (reparto uniforme entre los nodos activos). */
function siteShare(tenant: MockTenant) {
  const active = seededSites(tenant).filter((s) => s.role !== 'pending').length
  return 1 / Math.max(1, active)
}

/**
 * Perfil de cada router del ISP: `k` es su posición entre los activos (exportando y no
 * silenciosos), que fija quién avisa, quién está `stale` y quién crítico.
 */
function profileOf(tenant: MockTenant, item: MockRouter, k: number, options: SnmpOptions): Profile {
  if (item.router.onboarding_state !== 'exporting') return 'unknown'
  if (item.exporter.state === 'silent') return 'offline'
  if (k === 1) return 'warning'
  if (k === 2) return 'stale'
  if (k === 3 && options.degraded) return 'critical'
  if (k === 0 && tenant.tenant_slug === 'red-andina') return 'critical'
  return 'online'
}

/** Sondeo pedido desde la UI ya concluido (más reciente que el último ciclo). */
function pollSampleAt(routerId: string, t: number) {
  const at = mockState.snmpPolls.get(routerId)
  return at !== undefined && at <= t ? at : null
}

export function snmpRouters(
  tenant: MockTenant,
  now: Date,
  options: SnmpOptions = {},
): SnmpRouter[] {
  const t = now.getTime()
  const items = routersOf(tenant, now, { degraded: options.degraded, stepMs: options.stepMs })
  const snmpOff = options.scenario === 'snmp-off'
  const scale = 4.2e9 * tenantScale(tenant) * siteShare(tenant)
  let k = 0
  return items.map((item) => {
    const active = item.router.onboarding_state === 'exporting' && item.exporter.state !== 'silent'
    const profile = profileOf(tenant, item, active ? k++ : -1, options)
    const seed = hash(item.router.id + SILENT_SUFFIX)
    const r = rng(seed)
    // Variación lenta (cada 10 s) para que los números vivos se muevan sin saltar.
    const wobble = rng(hash(item.router.id + Math.floor(t / 10_000)))
    const jitter = 0.96 + wobble() * 0.08
    const rx = Math.round(dailyCurve(scale, t) * jitter)
    const tx = Math.round(rx * 0.19)

    const baseCpu = 8 + r() * 27
    const mem = 40 + r() * 20
    const baseTemp = 38 + r() * 14
    const rtt = 2 + r() * 7
    const cpu =
      profile === 'warning' ? 88 + wobble() * 2 : baseCpu * (0.6 + 0.8 * load(t)) + wobble()
    const temp = profile === 'critical' ? 86 + wobble() : baseTemp + (cpu - baseCpu) * 0.12
    const uptime = Math.floor((t - bootAt(seed)) / 1000)
    const cores = item.router.model?.includes('2116') ? 16 : 4

    const cycleAt = lastCycleAt(t)
    const polled = pollSampleAt(item.router.id, t)
    const sampledAt = polled !== null && polled > cycleAt ? polled : cycleAt
    const silentSince = new Date(item.exporter.state_since).getTime()
    const staleObservedAt = t - 9 * MINUTE

    const observedAt =
      profile === 'unknown'
        ? null
        : profile === 'offline'
          ? silentSince
          : profile === 'stale'
            ? staleObservedAt
            : sampledAt

    const wireguard: RouterSignals['wireguard'] =
      profile === 'unknown'
        ? item.peer.handshake_state === 'ok'
          ? 'ok'
          : 'unknown'
        : item.peer.handshake_state === 'stale'
          ? 'stale'
          : 'ok'
    const signals: RouterSignals = {
      icmp: profile === 'unknown' ? 'unknown' : profile === 'offline' ? 'fail' : 'ok',
      snmp: profile === 'unknown' ? 'unknown' : profile === 'offline' ? 'fail' : 'ok',
      wireguard,
      exporter: item.exporter.state,
    }

    const sample = (full: boolean): RouterDeviceSample => ({
      sampled_at: observedAt === null ? null : iso(observedAt),
      poll_duration_ms: full ? Math.round(180 + r() * 220 + wobble() * 40) : null,
      uptime_seconds: full ? uptime : null,
      cpu_percent: full ? round1(cpu) : null,
      cpu_max_core_percent: full ? round1(Math.min(100, cpu + 7 + r() * 5)) : null,
      cpu_cores: full ? cores : null,
      memory_percent: full ? round1(mem + wobble() * 0.4) : null,
      memory_used_bytes: full ? String(Math.round((mem / 100) * 16e9)) : null,
      memory_total_bytes: full ? String(16e9) : null,
      temperature_celsius: full ? round1(temp) : null,
      cpu_temperature_celsius: full ? round1(temp + 11) : null,
      voltage: full ? round1(23.6 + r() * 0.8) : null,
      firmware: profile === 'unknown' ? null : (item.router.routeros_version_detected ?? '7.16.2'),
      model: profile === 'unknown' ? null : item.router.model,
      serial_number: profile === 'unknown' ? null : `HF${String(seed).slice(0, 9)}`,
      icmp_rtt_ms: full ? round1(rtt + wobble() * 0.6) : null,
      icmp_loss_ratio: full ? 0 : profile === 'offline' ? 1 : null,
      sensors: full
        ? [
            { name: 'fan1-speed', value: Math.round(4200 + r() * 800), unit: 'rpm' },
            { name: 'psu1-state', value: 1, unit: null },
          ]
        : [],
    })

    const full =
      profile === 'online' || profile === 'warning' || profile === 'critical' || profile === 'stale'
    const observed: RouterStatus['observed_state'] =
      profile === 'unknown'
        ? null
        : profile === 'offline'
          ? 'offline'
          : profile === 'stale'
            ? 'online'
            : profile
    const reason: RouterStatus['reason'] =
      profile === 'unknown'
        ? null
        : profile === 'offline'
          ? wireguard === 'stale'
            ? 'tunnel_down'
            : 'host_unreachable_via_tunnel'
          : profile === 'warning'
            ? 'cpu_high'
            : profile === 'critical'
              ? 'temperature_high'
              : 'ok'
    const thresholds: RouterThresholdHit[] =
      profile === 'warning'
        ? [
            { metric: 'cpu_percent', level: 'warning', value: round1(cpu), threshold: 80 },
            {
              metric: 'cpu_max_core_percent',
              level: 'warning',
              value: round1(Math.min(100, cpu + 9)),
              threshold: 90,
            },
          ]
        : profile === 'critical'
          ? [
              {
                metric: 'temperature_celsius',
                level: 'critical',
                value: round1(temp),
                threshold: 85,
              },
            ]
          : []

    let status: RouterEffectiveStatus =
      profile === 'stale' ? 'stale' : profile === 'unknown' ? 'unknown' : profile
    let since: number | null =
      profile === 'unknown'
        ? null
        : profile === 'offline'
          ? silentSince
          : profile === 'stale'
            ? staleObservedAt + 3 * POLL_INTERVAL * 1000
            : profile === 'warning'
              ? t - 23 * MINUTE
              : profile === 'critical'
                ? t - 6 * MINUTE
                : new Date(item.exporter.state_since).getTime()
    if (snmpOff && status !== 'unknown') {
      status = 'stale'
      since = t - 3 * DAY
    }

    const uplink: RouterStatus['uplink'] =
      profile === 'unknown'
        ? null
        : {
            interface_id: interfaceId(item.router.id, 9),
            name: 'sfp-sfpplus1',
            rx_bps: full ? rx : null,
            tx_bps: full ? tx : null,
            speed_bps: UPLINK_SPEED,
            utilization: full ? round3(Math.max(rx, tx) / UPLINK_SPEED) : null,
          }

    const status_: RouterStatus = {
      router_id: item.router.id,
      site_id: item.site.id,
      name: item.router.name,
      site_name: item.site.name,
      status,
      observed_state: observed,
      reason,
      since: since === null ? null : iso(since),
      last_observed_at: observedAt === null ? null : iso(observedAt),
      signals,
      device: sample(full),
      uplink,
      interfaces_up:
        profile === 'unknown' ? null : INTERFACES.filter((d) => d.oper === 'up').length,
      interfaces_total: profile === 'unknown' ? null : INTERFACES.length,
      thresholds,
    }
    return { item, profile, status: status_, rx, tx, seed }
  })
}

const round1 = (n: number) => Math.round(n * 10) / 10
const round3 = (n: number) => Math.round(n * 1000) / 1000

/** Orden de la vista NOC: problemas primero, después por nombre (snmp.yaml). */
const STATUS_ORDER: Record<RouterEffectiveStatus, number> = {
  critical: 0,
  offline: 1,
  degraded: 2,
  warning: 3,
  stale: 4,
  unknown: 5,
  maintenance: 6,
  online: 7,
}

export function sortRouterStatus<T extends { status: RouterEffectiveStatus; name: string }>(
  list: T[],
) {
  return [...list].sort(
    (a, b) => STATUS_ORDER[a.status] - STATUS_ORDER[b.status] || a.name.localeCompare(b.name),
  )
}

export function routerStatusList(
  tenant: MockTenant,
  now: Date,
  options: SnmpOptions = {},
): RouterStatusList {
  const list = snmpRouters(tenant, now, options)
  const snmpOff = options.scenario === 'snmp-off'
  const t = now.getTime()
  return {
    data: sortRouterStatus(list.map((r) => r.status)),
    meta: {
      poller: {
        healthy: !snmpOff,
        enabled: !snmpOff,
        last_cycle_at: snmpOff ? iso(t - 3 * DAY) : iso(lastCycleAt(t)),
        interval_seconds: POLL_INTERVAL,
        targets: snmpOff ? 0 : list.filter((r) => r.profile !== 'unknown').length,
      },
      generated_at: now.toISOString(),
    },
  }
}

export function findSnmpRouter(
  tenant: MockTenant,
  routerId: string,
  now: Date,
  options: SnmpOptions = {},
) {
  return snmpRouters(tenant, now, options).find((r) => r.item.router.id === routerId)
}

// --- Interfaces ------------------------------------------------------------------------------

interface InterfaceDef {
  ifIndex: number
  name: string
  alias: string | null
  type: string
  speed: number | null
  oper: 'up' | 'down'
  /** Tráfico relativo al enlace de subida: [rx, tx] como fracción de [rx, tx] del uplink. */
  share: [number, number]
  /** Sentido invertido (de cara al cliente: lo que baja por el uplink sale por aquí). */
  mirror?: boolean
  uplink?: boolean
}

/** Interfaces descubiertas por IF-MIB. Las `<pppoe-…>` dinámicas no se sondean ni aparecen. */
const INTERFACES: InterfaceDef[] = [
  {
    ifIndex: 1,
    name: 'ether1',
    alias: 'MGMT',
    type: 'ethernetCsmacd',
    speed: 1e9,
    oper: 'up',
    share: [0.002, 0.004],
  },
  {
    ifIndex: 2,
    name: 'ether2',
    alias: 'OLT-1',
    type: 'ethernetCsmacd',
    speed: 1e9,
    oper: 'up',
    share: [0.46, 0.46],
    mirror: true,
  },
  {
    ifIndex: 3,
    name: 'ether3',
    alias: 'OLT-2',
    type: 'ethernetCsmacd',
    speed: 1e9,
    oper: 'up',
    share: [0.41, 0.41],
    mirror: true,
  },
  {
    ifIndex: 4,
    name: 'ether4',
    alias: null,
    type: 'ethernetCsmacd',
    speed: 1e9,
    oper: 'up',
    share: [0.05, 0.05],
    mirror: true,
  },
  {
    ifIndex: 5,
    name: 'ether5',
    alias: null,
    type: 'ethernetCsmacd',
    speed: 1e9,
    oper: 'down',
    share: [0, 0],
  },
  {
    ifIndex: 6,
    name: 'ether6',
    alias: null,
    type: 'ethernetCsmacd',
    speed: 1e9,
    oper: 'down',
    share: [0, 0],
  },
  {
    ifIndex: 9,
    name: 'sfp-sfpplus1',
    alias: 'UPLINK-TRANSIT-A',
    type: 'ethernetCsmacd',
    speed: UPLINK_SPEED,
    oper: 'up',
    share: [1, 1],
    uplink: true,
  },
  {
    ifIndex: 10,
    name: 'sfp-sfpplus2',
    alias: 'UPLINK-TRANSIT-B',
    type: 'ethernetCsmacd',
    speed: UPLINK_SPEED,
    oper: 'up',
    share: [0.12, 0.12],
  },
  {
    ifIndex: 12,
    name: 'bridge-clientes',
    alias: 'Clientes',
    type: 'bridge',
    speed: null,
    oper: 'up',
    share: [0.98, 0.98],
    mirror: true,
  },
  {
    ifIndex: 13,
    name: 'vlan100-uplink',
    alias: 'VLAN 100 tránsito',
    type: 'l2vlan',
    speed: null,
    oper: 'up',
    share: [0.97, 0.97],
  },
]

export function interfaceId(routerId: string, ifIndex: number) {
  return mockUuid('0192eaaa', (hash(routerId) % 1_000_000) * 100 + ifIndex)
}

function interfaceRates(sr: SnmpRouter, def: InterfaceDef, at: number, rxAt: number, txAt: number) {
  const r = rng(hash(`${sr.item.router.id}${def.ifIndex}${Math.floor(at / 10_000)}`))
  // El uplink coincide exactamente con el resumen del router; las demás respiran un poco.
  const j = def.uplink ? 1 : 0.97 + r() * 0.06
  const [sRx, sTx] = def.share
  const rx = Math.round((def.mirror ? txAt * sRx : rxAt * sRx) * j)
  const tx = Math.round((def.mirror ? rxAt * sTx : txAt * sTx) * j)
  return { rx, tx }
}

/** Interfaces con errores o descartes en el último intervalo (deterministas por router). */
function errorsFor(sr: SnmpRouter, def: InterfaceDef, at: number) {
  const r = rng(sr.seed + def.ifIndex)
  const flaky = r() < 0.45
  const w = rng(hash(`${sr.item.router.id}err${def.ifIndex}${Math.floor(at / 60_000)}`))
  const errors = flaky && def.name === 'ether3' ? 2 + Math.round(w() * 5) : 0
  const drops = flaky && def.name === 'sfp-sfpplus2' ? 8 + Math.round(w() * 20) : 0
  return { errors, drops }
}

export function routerInterfaces(sr: SnmpRouter, now: Date): RouterInterfaceList {
  if (sr.profile === 'unknown') return { data: [], discovered_at: null }
  const t = now.getTime()
  const discovered = Math.floor(t / 3_600_000) * 3_600_000
  const seen = sr.status.last_observed_at
  return {
    discovered_at: iso(discovered),
    data: INTERFACES.map((d): RouterInterface => ({
      id: interfaceId(sr.item.router.id, d.ifIndex),
      if_index: d.ifIndex,
      name: d.name,
      alias: d.alias,
      type: d.type,
      speed_bps: d.speed,
      monitored: true,
      uplink: !!d.uplink,
      oper_status: d.oper,
      admin_up: d.name !== 'ether6',
      last_seen_at: seen,
    })),
  }
}

export function routerLiveMetrics(sr: SnmpRouter, now: Date): RouterLiveMetrics {
  const t = now.getTime()
  const live = sr.profile !== 'offline' && sr.profile !== 'unknown'
  const interfaces: InterfaceSample[] =
    sr.profile === 'unknown'
      ? []
      : INTERFACES.map((d) => {
          const { rx, tx } = interfaceRates(sr, d, t, sr.rx, sr.tx)
          const { errors, drops } = errorsFor(sr, d, t)
          const up = d.oper === 'up'
          const val = (n: number) => (live && up ? n : null)
          return {
            interface_id: interfaceId(sr.item.router.id, d.ifIndex),
            if_index: d.ifIndex,
            name: d.name,
            alias: d.alias,
            uplink: !!d.uplink,
            oper_status: live ? d.oper : null,
            speed_bps: d.speed,
            rx_bps: val(rx),
            tx_bps: val(tx),
            rx_pps: val(Math.round(rx / (8 * 700))),
            tx_pps: val(Math.round(tx / (8 * 700))),
            rx_errors_delta: val(errors),
            tx_errors_delta: val(0),
            rx_drops_delta: val(drops),
            tx_drops_delta: val(0),
            utilization_rx: d.speed && live && up ? round3(rx / d.speed) : null,
            utilization_tx: d.speed && live && up ? round3(tx / d.speed) : null,
            counter_discontinuity: false,
          }
        })
  return { status: sr.status, interval_seconds: POLL_INTERVAL, interfaces }
}

// --- Series históricas (analytics) -------------------------------------------------------------

const DEVICE_METRICS = [
  'cpu_percent',
  'cpu_max_core_percent',
  'memory_percent',
  'temperature_celsius',
  'cpu_temperature_celsius',
  'icmp_rtt_ms',
  'icmp_loss_ratio',
  'availability_ratio',
  'poll_duration_ms',
  'uptime_seconds',
] as const
const DEVICE_UNITS: Record<string, string> = {
  cpu_percent: 'percent',
  cpu_max_core_percent: 'percent',
  memory_percent: 'percent',
  temperature_celsius: 'celsius',
  cpu_temperature_celsius: 'celsius',
  icmp_rtt_ms: 'ms',
  icmp_loss_ratio: 'ratio',
  availability_ratio: 'ratio',
  poll_duration_ms: 'ms',
  uptime_seconds: 'seconds',
}

/** Rampa suave 0 → 1 en los últimos `minutes` minutos antes de `now`. */
function ramp(at: number, now: number, minutes: number) {
  const x = 1 - Math.min(1, Math.max(0, (now - at) / (minutes * MINUTE)))
  return x * x * (3 - 2 * x)
}

/** Buckets sin observación: router apagado (desde que calló) o sondeador atrasado (stale). */
function gapAt(sr: SnmpRouter, at: number, now: number) {
  if (sr.profile === 'unknown') return true
  if (sr.profile === 'offline') return at >= new Date(sr.item.exporter.state_since).getTime()
  if (sr.profile === 'stale') return at > now - 9 * MINUTE
  return false
}

export function routerDeviceSeries(
  sr: SnmpRouter,
  now: Date,
  range: string | null,
  metrics: string[],
): SeriesResponse {
  const span = rangeMs(range)
  const step = stepFor(span)
  const t = Math.floor(now.getTime() / (step * 1000)) * step * 1000
  const r = rng(sr.seed)
  const baseCpu = 8 + r() * 27
  const mem = 40 + r() * 20
  const baseTemp = 38 + r() * 14
  const rtt = 2 + r() * 7
  const boot = bootAt(sr.seed)
  const wanted = metrics.filter((m): m is (typeof DEVICE_METRICS)[number] =>
    (DEVICE_METRICS as readonly string[]).includes(m),
  )
  const series: SeriesResponse['data']['series'] = wanted.map((metric) => ({
    metric,
    unit: DEVICE_UNITS[metric] ?? 'number',
    group: null,
    points: [],
  }))
  for (let at = t - span; at <= t; at += step * 1000) {
    const noise = rng(hash(`${sr.item.router.id}${at}`))
    const gap = gapAt(sr, at, now.getTime())
    const cpu =
      baseCpu * (0.6 + 0.8 * load(at)) +
      (noise() - 0.5) * 4 +
      (sr.profile === 'warning' ? 58 * ramp(at, now.getTime(), 45) : 0)
    const temp =
      baseTemp +
      (cpu - baseCpu) * 0.12 +
      (sr.profile === 'critical' ? 30 * ramp(at, now.getTime(), 25) : 0)
    const spike = noise() > 0.985 ? 20 + noise() * 40 : 0
    const values: Record<string, number | null> = {
      cpu_percent: round1(Math.min(100, cpu)),
      cpu_max_core_percent: round1(Math.min(100, cpu + 7 + noise() * 5)),
      memory_percent: round1(mem + Math.sin(at / (6 * 3_600_000)) * 1.5 + (noise() - 0.5) * 0.4),
      temperature_celsius: round1(temp),
      cpu_temperature_celsius: round1(temp + 11),
      icmp_rtt_ms: round1(rtt + noise() * 1.2 + spike),
      icmp_loss_ratio: spike ? round3(noise() * 0.05) : 0,
      availability_ratio: 1,
      poll_duration_ms: Math.round(180 + noise() * 260),
      uptime_seconds: at > boot ? Math.floor((at - boot) / 1000) : null,
    }
    for (const s of series) {
      const v = gap
        ? s.metric === 'availability_ratio' && sr.profile === 'offline'
          ? 0
          : null
        : values[s.metric]!
      s.points.push([iso(at), v])
    }
  }
  return { data: { series }, meta: analyticsMeta(now, range, step, 'snmp') }
}

const INTERFACE_METRICS = [
  'rx_bps',
  'tx_bps',
  'rx_pps',
  'tx_pps',
  'rx_errors',
  'tx_errors',
  'rx_drops',
  'tx_drops',
  'utilization_rx',
  'utilization_tx',
] as const
const INTERFACE_UNITS: Record<string, string> = {
  rx_bps: 'bps',
  tx_bps: 'bps',
  rx_pps: 'pps',
  tx_pps: 'pps',
  rx_errors: 'count',
  tx_errors: 'count',
  rx_drops: 'count',
  tx_drops: 'count',
  utilization_rx: 'ratio',
  utilization_tx: 'ratio',
}

export function routerInterfaceSeries(
  sr: SnmpRouter,
  now: Date,
  range: string | null,
  interfaceIds: string[],
  metrics: string[],
): SeriesResponse {
  const span = rangeMs(range)
  const step = stepFor(span)
  const t = Math.floor(now.getTime() / (step * 1000)) * step * 1000
  const defs = (
    interfaceIds.length
      ? INTERFACES.filter((d) => interfaceIds.includes(interfaceId(sr.item.router.id, d.ifIndex)))
      : INTERFACES.filter((d) => d.uplink)
  ).slice(0, 8)
  const wanted = metrics.filter((m): m is (typeof INTERFACE_METRICS)[number] =>
    (INTERFACE_METRICS as readonly string[]).includes(m),
  )
  const series: SeriesResponse['data']['series'] = []
  for (const d of defs) {
    const per = wanted.map((metric) => ({
      metric,
      unit: INTERFACE_UNITS[metric] ?? 'number',
      group: d.name,
      points: [] as [string, number | null][],
    }))
    for (let at = t - span; at <= t; at += step * 1000) {
      const gap = gapAt(sr, at, now.getTime()) || d.oper !== 'up'
      const base = dailyCurve(sr.rx / dailyCurve(1, now.getTime()), at)
      const noise = rng(hash(`${sr.item.router.id}${d.ifIndex}${at}`))
      const { rx, tx } = interfaceRates(sr, d, at, base * (0.95 + noise() * 0.1), base * 0.19)
      const { errors, drops } = errorsFor(sr, d, at)
      const values: Record<string, number | null> = {
        rx_bps: rx,
        tx_bps: tx,
        rx_pps: Math.round(rx / (8 * 700)),
        tx_pps: Math.round(tx / (8 * 700)),
        rx_errors: errors,
        tx_errors: 0,
        rx_drops: drops,
        tx_drops: 0,
        utilization_rx: d.speed ? round3(rx / d.speed) : null,
        utilization_tx: d.speed ? round3(tx / d.speed) : null,
      }
      for (const s of per) s.points.push([iso(at), gap ? null : values[s.metric]!])
    }
    series.push(...per)
  }
  return { data: { series }, meta: analyticsMeta(now, range, step, 'snmp') }
}

// --- Tiempo real: avisos de cambio (sondeo concluido) ----------------------------------------

type SnmpListener = (tenantId: string, routerId: string) => void
const listeners = new Set<SnmpListener>()

/** Avisa cuando cambia la observación de un router (p. ej. concluye un sondeo pedido). */
export function onSnmpChange(listener: SnmpListener) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function notifySnmpChange(tenantId: string, routerId: string) {
  for (const l of [...listeners]) l(tenantId, routerId)
}

/** Mensaje `event` del tema `routers.status` (proyección de `horus.snmp.router.state_changed`). */
export function routerStatusEvent(status: RouterStatus, now: Date): RealtimeEventMessage {
  const {
    router_id,
    site_id,
    observed_state,
    reason,
    since,
    last_observed_at,
    signals,
    device,
    uplink,
  } = status
  return {
    type: 'event',
    topic: 'routers.status',
    event: {
      id: `evt-snmp-${router_id}-${now.getTime()}`,
      type: 'horus.snmp.router.state_changed',
      time: now.toISOString(),
      data: {
        router_id,
        site_id,
        status: status.status,
        observed_state,
        reason,
        since,
        last_observed_at,
        signals,
        device,
        uplink,
        interfaces_up: status.interfaces_up,
        interfaces_total: status.interfaces_total,
        thresholds: status.thresholds,
      },
    },
  }
}

/** Mensaje `state` del tema `router.{id}.metrics` (forma de `GET /routers/{id}/metrics/live`). */
export function routerMetricsMessage(live: RouterLiveMetrics, now: Date): RealtimeStateMessage {
  return {
    type: 'state',
    topic: `router.${live.status.router_id}.metrics`,
    key: live.status.router_id,
    time: now.toISOString(),
    data: live as unknown as Record<string, unknown>,
  }
}

// --- Rutas -----------------------------------------------------------------------------------

const DEFAULT_THRESHOLDS: RouterThresholdValues = {
  cpu_warning: 80,
  cpu_critical: 95,
  memory_warning: 85,
  memory_critical: 95,
  temperature_warning: 70,
  temperature_critical: 85,
}
const tenantThresholds = new Map<
  string,
  { values: RouterThresholdValues; version: number; at: string }
>()

/** Umbrales por ISP (GET/PUT/DELETE /routers/thresholds); sin ajustes, los de la instalación. */
function thresholdsRoute(ctx: TenantRouteContext): Response | undefined {
  const view = (): RouterThresholds => {
    const own = tenantThresholds.get(ctx.tenant.tenant_id)
    return {
      ...(own?.values ?? DEFAULT_THRESHOLDS),
      defaults: DEFAULT_THRESHOLDS,
      customized: !!own,
      version: own?.version ?? 0,
      updated_at: own?.at ?? null,
      sustained_cycles: 3,
    }
  }
  if (ctx.method === 'GET') {
    if (!ctx.can('snmp.read')) return forbidden()
    return json(view(), 200, { ETag: `"${view().version}"` })
  }
  if (!ctx.can('snmp.manage')) return forbidden()
  if (ctx.method === 'DELETE') {
    tenantThresholds.delete(ctx.tenant.tenant_id)
    return json(view())
  }
  if (ctx.method !== 'PUT') return
  const v = ctx.body as unknown as RouterThresholdValues
  const errors: { field: string; code: string; message: string }[] = []
  for (const [key, lo, hi, unit] of [
    ['cpu', 1, 100, ' %'],
    ['memory', 1, 100, ' %'],
    ['temperature', 20, 150, ' °C'],
  ] as const) {
    const w = v[`${key}_warning`]
    const c = v[`${key}_critical`]
    if (!(w >= lo && w <= hi))
      errors.push({
        field: `${key}_warning`,
        code: 'OUT_OF_RANGE',
        message: `Entre ${lo} y ${hi}${unit}.`,
      })
    if (!(c >= lo && c <= hi))
      errors.push({
        field: `${key}_critical`,
        code: 'OUT_OF_RANGE',
        message: `Entre ${lo} y ${hi}${unit}.`,
      })
    if (w > c)
      errors.push({
        field: `${key}_warning`,
        code: 'OUT_OF_RANGE',
        message: 'El aviso no puede superar al crítico.',
      })
  }
  if (errors.length)
    return problem(422, 'VALIDATION_FAILED', 'Revisa los campos marcados', {}, { errors })
  const prev = tenantThresholds.get(ctx.tenant.tenant_id)
  const match = ctx.req.headers.get('If-Match')
  if (match && prev && match.replaceAll('"', '') !== String(prev.version)) {
    return problem(412, 'PRECONDITION_FAILED', 'Los umbrales cambiaron')
  }
  tenantThresholds.set(ctx.tenant.tenant_id, {
    values: {
      cpu_warning: v.cpu_warning,
      cpu_critical: v.cpu_critical,
      memory_warning: v.memory_warning,
      memory_critical: v.memory_critical,
      temperature_warning: v.temperature_warning,
      temperature_critical: v.temperature_critical,
    },
    version: (prev?.version ?? 0) + 1,
    at: ctx.now.toISOString(),
  })
  return json(view(), 200, { ETag: `"${view().version}"` })
}

export function snmpRoutes(ctx: TenantRouteContext): Response | undefined {
  const { tenant, now } = ctx
  const options: SnmpOptions = {
    degraded: ctx.degraded,
    stepMs: ctx.stepMs,
    scenario: ctx.scenario,
  }

  if (ctx.method === 'GET' && ctx.path === '/routers/status') {
    if (!ctx.can('devices.read')) return forbidden()
    const q = ctx.url.searchParams
    const sites = q.get('site_id')?.split(',').filter(Boolean)
    const statuses = q.get('status')?.split(',').filter(Boolean)
    const list = routerStatusList(tenant, now, options)
    list.data = list.data.filter(
      (r) =>
        (!sites?.length || sites.includes(r.site_id)) &&
        (!statuses?.length || statuses.includes(r.status)),
    )
    return json(list)
  }

  if (ctx.path === '/routers/thresholds') return thresholdsRoute(ctx)

  const m = ctx.path.match(
    /^\/(analytics\/)?routers\/([^/]+)\/(metrics\/live|interfaces|poll|metrics|interfaces\/metrics)$/,
  )
  if (!m) return
  const [, analytics, routerId, action] = m
  if (analytics && !['metrics', 'interfaces/metrics'].includes(action!)) return
  if (!analytics && !['metrics/live', 'interfaces', 'poll'].includes(action!)) return

  const sr = findSnmpRouter(tenant, routerId!, now, options)
  if (!sr) return notFound('ROUTER_NOT_FOUND')

  if (analytics) {
    if (ctx.method !== 'GET') return
    if (!ctx.can('snmp.read')) return forbidden()
    const down = analyticsDown(ctx.degraded)
    if (down) return down
    const q = ctx.url.searchParams
    const range = q.get('range')
    if (action === 'metrics') {
      const metrics = (q.get('metrics') ?? 'cpu_percent,memory_percent,temperature_celsius')
        .split(',')
        .filter(Boolean)
      return json(routerDeviceSeries(sr, now, range, metrics))
    }
    const ids = q.get('interface_id')?.split(',').filter(Boolean) ?? []
    if (ids.length > 8) {
      return problem(
        422,
        'VALIDATION_FAILED',
        'Revisa los campos marcados',
        {},
        { errors: [{ field: 'interface_id', code: 'TOO_MANY', message: 'Máximo 8 interfaces' }] },
      )
    }
    const metrics = (q.get('metrics') ?? 'rx_bps,tx_bps').split(',').filter(Boolean)
    return json(routerInterfaceSeries(sr, now, range, ids, metrics))
  }

  if (ctx.method === 'GET' && action === 'metrics/live') {
    if (!ctx.can('snmp.read')) return forbidden()
    return json(routerLiveMetrics(sr, now))
  }
  if (ctx.method === 'GET' && action === 'interfaces') {
    if (!ctx.can('devices.read')) return forbidden()
    return json(routerInterfaces(sr, now))
  }
  if (ctx.method === 'POST' && action === 'poll') {
    if (!ctx.can('devices.update')) return forbidden()
    if (!ctx.req.headers.get('Idempotency-Key')) {
      return problem(428, 'PRECONDITION_REQUIRED', 'Falta Idempotency-Key')
    }
    if (ctx.scenario === 'snmp-off') return json({ queued: false, reason: 'poller_disabled' }, 202)
    if (sr.profile === 'unknown') return json({ queued: false, reason: 'no_tunnel' }, 202)
    const doneAt = now.getTime() + POLL_DELAY_MS
    mockState.snmpPolls.set(sr.item.router.id, doneAt)
    const tenantId = tenant.tenant_id
    const id = sr.item.router.id
    setTimeout(() => notifySnmpChange(tenantId, id), POLL_DELAY_MS)
    return json({ queued: true, reason: null }, 202)
  }
}
