import type {
  LibreNmsIntegration,
  LibreNmsObject,
  LibreNmsRouter,
  LibreNmsSyncStep,
  LibreNmsTestResult,
  LibreNmsTestStep,
  NotificationChannel,
} from '~~/types/api'
import type { MockTenant } from './data'
import { forbidden, json, noContent, notFound, problem } from './http'
import { routersOf } from './inventory'
import { mockState } from './state'

/**
 * Integración con LibreNMS (D17): Horus aprovisiona LibreNMS (dispositivo «Horus Flow», grupos,
 * plantillas y reglas «Horus · ») y le delega el alertado por syslog. Simulación determinista:
 * - URL base con `fail` → token rechazado (`auth_failed`).
 * - URL base con `nosyslog` → la prueba falla en `syslog_stored` (syslog de LibreNMS desactivado).
 * - Sin credenciales en el canal → `missing_credentials`.
 */

export interface LibreNmsRouteContext {
  req: Request
  path: string
  method: string
  body: Record<string, unknown>
  tenant: MockTenant
  can: (permission: string) => boolean
  now: Date
  reauthRequired?: boolean
}

export interface LibreNmsTenantState {
  integrations: Map<string, LibreNmsIntegration>
  routers: Map<
    string,
    { mgmt_ip: string; mode: LibreNmsRouter['mode']; secret: boolean; consent: boolean; at: string }
  >
  nextId: number
}

function stateOf(tenant: MockTenant): LibreNmsTenantState {
  let st = mockState.librenms.get(tenant.tenant_id)
  if (!st) {
    st = { integrations: new Map(), routers: new Map(), nextId: 40 }
    mockState.librenms.set(tenant.tenant_id, st)
  }
  return st
}

const TEMPLATE_RULES: [string, string][] = [
  ['router_down', 'Router caído'],
  ['cpu_high', 'CPU alta'],
  ['memory_high', 'Memoria alta'],
  ['port_down', 'Interfaz caída'],
  ['reboot', 'Reinicio'],
  ['infra_down', 'Servicio de infraestructura caído'],
  ['horus_event', 'Evento de seguridad Horus'],
]

function emptyIntegration(channelId: string): LibreNmsIntegration {
  return {
    channel_id: channelId,
    status: 'not_provisioned',
    horus_hostname: null,
    horus_hostname_default: 'horus.fibranorte.example',
    cpu_threshold: 85,
    memory_threshold: 90,
    tz_offset_minutes: null,
    remote_version: null,
    last_sync_at: null,
    last_error_code: null,
    last_error: null,
    warnings: [],
    objects: [],
    last_sync_steps: [],
    last_test: null,
  }
}

function hasCredentials(channel: NotificationChannel) {
  return channel.has_credentials
}

function baseUrl(channel: NotificationChannel) {
  return String((channel.config as Record<string, unknown>).base_url ?? '')
}

function sync(
  ctx: LibreNmsRouteContext,
  channel: NotificationChannel,
  st: LibreNmsTenantState,
): Response {
  const prev = st.integrations.get(channel.id) ?? emptyIntegration(channel.id)
  const body = ctx.body as {
    horus_hostname?: string
    cpu_threshold?: number
    memory_threshold?: number
  }
  for (const k of ['cpu_threshold', 'memory_threshold'] as const) {
    const v = body[k]
    if (v !== undefined && (v < 50 || v > 99)) {
      return problem(
        422,
        'VALIDATION_FAILED',
        'Revisa los campos marcados',
        {},
        { errors: [{ field: k, code: 'OUT_OF_RANGE', message: 'Entre 50 y 99.' }] },
      )
    }
  }
  const integ: LibreNmsIntegration = {
    ...prev,
    horus_hostname:
      body.horus_hostname?.trim() || prev.horus_hostname || prev.horus_hostname_default,
    cpu_threshold: body.cpu_threshold ?? prev.cpu_threshold,
    memory_threshold: body.memory_threshold ?? prev.memory_threshold,
    last_sync_at: ctx.now.toISOString(),
  }
  const fail = (code: LibreNmsSyncStep['error_code'], error: string) => {
    Object.assign(integ, {
      status: 'failed',
      last_error_code: code,
      last_error: error,
      warnings: [],
      last_sync_steps: [],
    })
    st.integrations.set(channel.id, integ)
    return json({ ok: false, error_code: code, error, steps: [], warnings: [], integration: integ })
  }
  if (!hasCredentials(channel)) {
    return fail(
      'missing_credentials',
      'Configure el token de API de LibreNMS en el canal (credenciales).',
    )
  }
  if (/fail|invalid/.test(baseUrl(channel))) return fail('auth_failed', 'Unauthenticated.')

  const known = new Map(prev.objects.map((o) => [`${o.kind}/${o.key}`, o]))
  const steps: LibreNmsSyncStep[] = []
  const objects: LibreNmsObject[] = []
  const put = (
    kind: LibreNmsObject['kind'],
    key: string,
    name: string,
    extra: Partial<LibreNmsObject> = {},
  ) => {
    const cur = known.get(`${kind}/${key}`)
    const remote_id = cur?.remote_id ?? st.nextId++
    const action = cur
      ? kind === 'template' || kind === 'rule'
        ? 'updated'
        : 'unchanged'
      : 'created'
    const obj: LibreNmsObject = {
      kind,
      key,
      name,
      remote_id,
      hostname: null,
      created_by_horus: true,
      active: true,
      ...cur,
      ...extra,
    }
    objects.push(obj)
    steps.push({ kind, key, name, action, remote_id })
  }
  put('device', 'horus', 'Horus Flow', { hostname: integ.horus_hostname })
  put('group', 'routers', 'Horus · Routers')
  put('group', 'infra', 'Horus · Infraestructura')
  const routers = routersOf(ctx.tenant, ctx.now)
  for (const [routerId, cfg] of st.routers) {
    const r = routers.find((x) => x.router.id === routerId)
    put('router_device', routerId, r?.router.name ?? routerId, {
      hostname: cfg.mgmt_ip,
      active: true,
    })
  }
  for (const o of prev.objects) {
    if (o.kind === 'router_device' && !st.routers.has(o.key)) {
      objects.push({ ...o, active: false })
      if (o.active)
        steps.push({
          kind: o.kind,
          key: o.key,
          name: o.name,
          action: 'removed',
          remote_id: o.remote_id,
        })
    }
  }
  for (const [key, name] of TEMPLATE_RULES) put('rule', key, `Horus · ${name}`)
  for (const [key, name] of TEMPLATE_RULES) put('template', key, `Horus · ${name}`)
  Object.assign(integ, {
    status: 'ok',
    tz_offset_minutes: -360,
    remote_version: '25.10.0',
    last_error_code: null,
    last_error: null,
    warnings: [],
    objects,
    last_sync_steps: steps,
  })
  st.integrations.set(channel.id, integ)
  return json({ ok: true, error_code: null, error: null, steps, warnings: [], integration: integ })
}

function endToEnd(
  ctx: LibreNmsRouteContext,
  channel: NotificationChannel,
  st: LibreNmsTenantState,
): Response {
  const integ = st.integrations.get(channel.id)
  const horus = integ?.objects.find((o) => o.kind === 'device' && o.key === 'horus')
  if (!integ || !horus) {
    return problem(409, 'LIBRENMS_NOT_PROVISIONED', 'Primero conecte Horus con LibreNMS')
  }
  const steps: LibreNmsTestStep[] = [
    { step: 'auth', ok: true, detail: '25.10.0' },
    { step: 'device', ok: true, detail: horus.hostname ?? '' },
    { step: 'rule', ok: true },
    { step: 'syslog_send', ok: true },
  ]
  const line = `HORUS INFO test Prueba de integración de Horus Flow | Si ves esta alerta en LibreNMS, la integración funciona (se resuelve sola en 5 minutos). | id=${ctx.now.getTime()}`
  const result: LibreNmsTestResult = {
    ok: true,
    channel_id: channel.id,
    checked_at: ctx.now.toISOString(),
    latency_ms: 2140,
    remote_version: '25.10.0',
    failed_step: null,
    error_code: null,
    error: null,
    line,
    steps,
  }
  if (/nosyslog/.test(baseUrl(channel))) {
    const error =
      'LibreNMS aceptó la línea pero no la guardó en el syslog de «Horus Flow»: activa el syslog (lnms config:set enable_syslog true) y comprueba que el host del dispositivo coincide.'
    steps.push({ step: 'syslog_stored', ok: false, error_code: 'syslog_not_stored', detail: error })
    Object.assign(result, {
      ok: false,
      failed_step: 'syslog_stored',
      error_code: 'syslog_not_stored',
      error,
    })
  } else {
    steps.push({ step: 'syslog_stored', ok: true, detail: '2026-10-14 06:00:00' })
  }
  integ.last_test = result
  return json(result)
}

function routerView(
  ctx: LibreNmsRouteContext,
  st: LibreNmsTenantState,
  item: ReturnType<typeof routersOf>[number],
): LibreNmsRouter {
  const cfg = st.routers.get(item.router.id)
  return {
    router_id: item.router.id,
    router_name: item.router.name,
    site_id: item.router.site_id,
    onboarding_state: item.router.onboarding_state,
    mgmt_ip: cfg?.mgmt_ip ?? null,
    mode: cfg?.mode ?? 'ping',
    has_snmp_credentials: cfg?.secret ?? false,
    horus_snmp_reuse_consent: cfg?.consent ?? false,
    updated_at: cfg?.at ?? null,
  }
}

function invalid(field: string, message: string) {
  return problem(
    422,
    'VALIDATION_FAILED',
    'Revisa los campos marcados',
    {},
    { errors: [{ field, code: 'INVALID', message }] },
  )
}

const IPV4 = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/

export function librenmsRoute(ctx: LibreNmsRouteContext): Response | undefined {
  const channelMatch = ctx.path.match(
    /^\/notification-channels\/([^/]+)\/librenms(?:\/(sync|test|devices\/([^/]+)))?$/,
  )
  const routerMatch = ctx.path.match(/^\/librenms\/routers(?:\/([^/]+)(\/snmp-credentials)?)?$/)
  if (!channelMatch && !routerMatch) return
  if (!ctx.can('alerts.read')) return forbidden()
  const st = stateOf(ctx.tenant)
  const write = () => ctx.can('alerts.manage')

  if (channelMatch) {
    const channel = mockState.channels
      .get(ctx.tenant.tenant_id)
      ?.find((c) => c.id === channelMatch[1])
    if (!channel) return notFound('NOTIFICATION_CHANNEL_NOT_FOUND')
    if (channel.kind !== 'librenms') {
      return problem(422, 'LIBRENMS_CHANNEL_REQUIRED', 'Solo canales LibreNMS')
    }
    const action = channelMatch[2]
    if (!action && ctx.method === 'GET') {
      return json(st.integrations.get(channel.id) ?? emptyIntegration(channel.id))
    }
    if (!write()) return forbidden()
    if (action === 'sync' && ctx.method === 'POST') return sync(ctx, channel, st)
    if (action === 'test' && ctx.method === 'POST') return endToEnd(ctx, channel, st)
    if (channelMatch[3] && ctx.method === 'DELETE') {
      const integ = st.integrations.get(channel.id)
      const obj = integ?.objects.find(
        (o) => o.kind === 'router_device' && o.key === channelMatch[3],
      )
      if (!integ || !obj) return notFound()
      if (!obj.created_by_horus) {
        return problem(409, 'LIBRENMS_DEVICE_NOT_OWNED', 'Ese dispositivo ya existía en LibreNMS')
      }
      integ.objects = integ.objects.filter((o) => o !== obj)
      return noContent()
    }
    return notFound()
  }

  const routers = routersOf(ctx.tenant, ctx.now)
  const id = routerMatch![1]
  if (!id) {
    if (ctx.method !== 'GET') return notFound()
    return json({ data: routers.map((r) => routerView(ctx, st, r)) })
  }
  const item = routers.find((r) => r.router.id === id)
  if (!item) return notFound('ROUTER_NOT_FOUND')
  if (!write()) return forbidden()
  if (routerMatch![2] && ctx.method === 'PUT') {
    if (ctx.reauthRequired) {
      return problem(403, 'REAUTH_REQUIRED', 'Confirma tu identidad para continuar')
    }
    const cfg = st.routers.get(id)
    if (!cfg || (cfg.mode !== 'snmp_v2c' && cfg.mode !== 'snmp_v3')) {
      return invalid('mode', 'Primero fije la IP de gestión y un modo SNMP propio.')
    }
    const b = ctx.body as Record<string, string>
    if (cfg.mode === 'snmp_v2c' && !b.community)
      return invalid('community', 'Escribe la community.')
    if (cfg.mode === 'snmp_v3' && !b.v3_username)
      return invalid('v3_username', 'Escribe el usuario SNMPv3.')
    cfg.secret = true
    return noContent()
  }
  if (ctx.method === 'PUT') {
    const b = ctx.body as {
      mgmt_ip?: string
      mode?: LibreNmsRouter['mode']
      reuse_horus_snmp_consent?: boolean
    }
    const ip = String(b.mgmt_ip ?? '').trim()
    if (!IPV4.test(ip) && !/^[0-9a-f:]+:[0-9a-f:]*$/i.test(ip)) {
      return invalid('mgmt_ip', 'IPv4 o IPv6 de gestión alcanzable desde LibreNMS.')
    }
    const mode = b.mode ?? 'ping'
    if (mode === 'horus_snmp_v3' && !b.reuse_horus_snmp_consent) {
      return invalid(
        'reuse_horus_snmp_consent',
        'Confirme que LibreNMS usará las credenciales SNMPv3 de Horus para este router.',
      )
    }
    const prev = st.routers.get(id)
    st.routers.set(id, {
      mgmt_ip: ip,
      mode,
      secret: prev?.mode === mode ? prev.secret : false,
      consent: mode === 'horus_snmp_v3',
      at: ctx.now.toISOString(),
    })
    return json(routerView(ctx, st, item))
  }
  if (ctx.method === 'DELETE') {
    st.routers.delete(id)
    return noContent()
  }
  return notFound()
}
