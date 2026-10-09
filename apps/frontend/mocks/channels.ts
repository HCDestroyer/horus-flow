import type {
  ConnectionTestResult,
  LibreNmsChannelConfig,
  NotificationChannel,
  NotificationChannelInput,
  NotificationDelivery,
} from '~~/types/api'
import type { MockTenant } from './data'
import { forbidden, ifMatch, json, noContent, notFound, problem } from './http'
import { hash, mockUuid } from './random'
import { mockState } from './state'

/**
 * Canales de notificación por ISP (D13, D17): email, Telegram y LibreNMS por su API. Las
 * credenciales son write-only: se guardan aparte y nunca se devuelven (solo `has_credentials`).
 */

export interface ChannelRouteContext {
  req: Request
  path: string
  method: string
  body: Record<string, unknown>
  tenant: MockTenant
  can: (permission: string) => boolean
  now: Date
}

const credentials = new Map<string, Record<string, string>>()

function seed(tenant: MockTenant, now: Date): NotificationChannel[] {
  const base = {
    tenant_id: tenant.tenant_id,
    created_at: '2026-09-12T15:00:00.000Z',
    updated_at: '2026-09-12T15:00:00.000Z',
    version: 1,
  }
  return [
    {
      ...base,
      id: mockUuid('0194c000', hash(tenant.tenant_id + 'email')),
      kind: 'email',
      name: 'Guardia NOC por correo',
      enabled: true,
      include_personal_data: false,
      config: {
        recipients: ['noc@fibranorte.example', 'guardia@fibranorte.example'],
        language: 'es',
        subject_prefix: '[Horus]',
      },
      subscription: {
        event_types: ['finding_opened', 'exporter_silent', 'exporter_recovered', 'tunnel_down'],
        min_severity: 'high',
        site_ids: [],
        throttle_minutes: 15,
      },
      has_credentials: true,
      status: 'ok',
      last_delivery_at: new Date(now.getTime() - 47 * 60_000).toISOString(),
      last_error: null,
    },
    {
      ...base,
      id: mockUuid('0194c000', hash(tenant.tenant_id + 'telegram')),
      kind: 'telegram',
      name: 'Grupo de seguridad (Telegram)',
      enabled: true,
      include_personal_data: false,
      config: { chat_id: '-1001234567890', language: 'es' },
      subscription: {
        event_types: ['finding_opened', 'finding_reopened'],
        min_severity: 'critical',
        site_ids: [],
        throttle_minutes: 15,
      },
      has_credentials: false,
      status: 'unverified',
      last_delivery_at: null,
      last_error: null,
    },
  ]
}

function channelsOf(tenant: MockTenant, now: Date) {
  let list = mockState.channels.get(tenant.tenant_id)
  if (!list) {
    list = seed(tenant, now)
    mockState.channels.set(tenant.tenant_id, list)
  }
  return list
}

function invalid(field: string, message: string) {
  return problem(
    422,
    'VALIDATION_FAILED',
    'Revisa los campos marcados',
    {},
    {
      errors: [{ field, code: 'INVALID', message }],
    },
  )
}

function validate(input: NotificationChannelInput) {
  if (!input.name?.trim()) return invalid('name', 'Escribe un nombre')
  if (!input.subscription?.event_types?.length) {
    return invalid('subscription.event_types', 'Elige al menos un tipo de evento')
  }
  const c = input.config as Record<string, unknown>
  if (input.kind === 'email') {
    const list = (c.recipients as string[] | undefined) ?? []
    if (!list.length || list.some((r) => !/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(r))) {
      return invalid('config.recipients', 'Escribe uno o más correos válidos, separados por comas')
    }
  }
  if (input.kind === 'telegram' && !/^-?\d{5,20}$/.test(String(c.chat_id ?? ''))) {
    return invalid('config.chat_id', 'El chat_id es un número (los grupos empiezan por -100)')
  }
  if (input.kind === 'librenms') {
    try {
      const url = new URL(String(c.base_url ?? ''))
      if (url.username || url.password)
        return invalid('config.base_url', 'Sin credenciales en la URL')
    } catch {
      return invalid('config.base_url', 'Escribe la URL base, p. ej. https://librenms.tu-isp.com')
    }
    if (!String(c.username ?? '').trim())
      return invalid('config.username', 'Escribe el usuario de LibreNMS')
  }
  return null
}

function testResult(channel: NotificationChannel, now: Date): ConnectionTestResult {
  const creds = credentials.get(channel.id) ?? {}
  const base = { channel_id: channel.id, channel_kind: channel.kind, checked_at: now.toISOString() }
  const missing =
    (channel.kind === 'telegram' && !channel.has_credentials) ||
    (channel.kind === 'librenms' && !creds.password && !creds.api_token)
  if (missing) {
    return {
      ...base,
      ok: false,
      error_code: 'missing_credentials',
      error: 'Faltan las credenciales del canal',
    }
  }
  if (channel.kind === 'librenms') {
    const cfg = channel.config as LibreNmsChannelConfig
    if (/fail|invalid/.test(cfg.base_url) || creds.password === 'incorrecta') {
      return {
        ...base,
        ok: false,
        error_code: 'auth_failed',
        error: 'LibreNMS rechazó el usuario o la contraseña (401)',
      }
    }
    return { ...base, ok: true, latency_ms: 142, remote_version: '24.9.1' }
  }
  return { ...base, ok: true, latency_ms: channel.kind === 'email' ? 310 : 95 }
}

export function channelRoute(ctx: ChannelRouteContext): Response | undefined {
  if (!ctx.path.startsWith('/notification-')) return
  if (!ctx.can('alerts.read')) return forbidden()
  const list = channelsOf(ctx.tenant, ctx.now)
  const write = () => ctx.can('alerts.manage')

  if (ctx.path === '/notification-deliveries' && ctx.method === 'GET') {
    const deliveries = mockState.deliveries.get(ctx.tenant.tenant_id) ?? []
    return json({
      data: deliveries,
      page: { limit: 50, has_more: false, next_cursor: null, prev_cursor: null },
    })
  }
  if (ctx.path === '/notification-channels') {
    if (ctx.method === 'GET') {
      return json({
        data: list,
        page: { limit: 50, has_more: false, next_cursor: null, prev_cursor: null },
      })
    }
    if (ctx.method === 'POST') {
      if (!write()) return forbidden()
      const input = ctx.body as unknown as NotificationChannelInput
      const err = validate(input)
      if (err) return err
      const now = ctx.now.toISOString()
      const channel: NotificationChannel = {
        ...input,
        enabled: input.enabled ?? true,
        include_personal_data: input.include_personal_data ?? false,
        id: mockUuid('0194c100', hash(input.name + now)),
        tenant_id: ctx.tenant.tenant_id,
        created_at: now,
        updated_at: now,
        version: 1,
        has_credentials: input.kind === 'email',
        status: 'unverified',
        last_delivery_at: null,
        last_error: null,
      }
      list.push(channel)
      return json(channel, 201, { ETag: '"1"' })
    }
  }
  const m = ctx.path.match(
    /^\/notification-channels\/([^/]+)(?:\/(credentials|connection-test|test))?$/,
  )
  const channel = m && list.find((c) => c.id === m[1])
  if (!m || !channel) return notFound('NOTIFICATION_CHANNEL_NOT_FOUND')
  const action = m[2]
  if (!action && ctx.method === 'GET') return json(channel, 200, { ETag: `"${channel.version}"` })
  if (!write()) return forbidden()

  if (action === 'credentials' && ctx.method === 'PUT') {
    const body = ctx.body as Record<string, string>
    const clean = Object.fromEntries(
      Object.entries(body).filter(([, v]) => typeof v === 'string' && v),
    )
    if (!Object.keys(clean).length) return invalid('password', 'Escribe al menos una credencial')
    credentials.set(channel.id, { ...(credentials.get(channel.id) ?? {}), ...clean })
    channel.has_credentials = true
    channel.status = 'unverified'
    return noContent()
  }
  if (action === 'connection-test' && ctx.method === 'POST') {
    const result = testResult(channel, ctx.now)
    channel.status = result.ok ? 'ok' : 'failing'
    channel.last_error = result.ok ? null : (result.error ?? null)
    return json(result)
  }
  if (action === 'test' && ctx.method === 'POST') {
    if (!ctx.req.headers.get('Idempotency-Key')) {
      return problem(428, 'PRECONDITION_REQUIRED', 'Falta Idempotency-Key')
    }
    const result = testResult(channel, ctx.now)
    const delivery: NotificationDelivery = {
      id: mockUuid('0194c200', hash(channel.id + ctx.now.getTime())),
      tenant_id: ctx.tenant.tenant_id,
      channel_id: channel.id,
      channel_kind: channel.kind,
      created_at: ctx.now.toISOString(),
      sent_at: result.ok ? ctx.now.toISOString() : null,
      status: result.ok ? 'sent' : 'failed',
      error: result.ok ? null : (result.error ?? null),
      is_test: true,
      source_event_id: null,
      source_event_type: null,
    }
    mockState.deliveries.set(ctx.tenant.tenant_id, [
      delivery,
      ...(mockState.deliveries.get(ctx.tenant.tenant_id) ?? []),
    ])
    if (result.ok) channel.last_delivery_at = ctx.now.toISOString()
    return json({ ...delivery, status: 'queued', sent_at: null }, 202)
  }
  if (!action && (ctx.method === 'PATCH' || ctx.method === 'DELETE')) {
    if (ifMatch(ctx.req) !== channel.version) {
      return problem(
        412,
        'PRECONDITION_FAILED',
        'El canal cambió',
        {},
        {
          current: channel as unknown as Record<string, unknown>,
        },
      )
    }
    if (ctx.method === 'DELETE') {
      list.splice(list.indexOf(channel), 1)
      credentials.delete(channel.id)
      return noContent()
    }
    const merged = { ...channel, ...(ctx.body as Partial<NotificationChannel>) }
    const err = validate(merged as NotificationChannelInput)
    if (err) return err
    Object.assign(channel, ctx.body, {
      version: channel.version + 1,
      updated_at: ctx.now.toISOString(),
    })
    return json(channel, 200, { ETag: `"${channel.version}"` })
  }
}
