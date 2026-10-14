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
        event_types: ['finding_opened'],
        categories: ['routers', 'exporter_tunnel'],
        min_severity: 'high',
        site_ids: [],
        throttle_minutes: 15,
        notify_recovery: true,
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
      config: { chat_id: '-1001234567890', language: 'es', uses_own_bot: false },
      subscription: {
        event_types: [],
        categories: ['security'],
        min_severity: 'critical',
        site_ids: [],
        throttle_minutes: 15,
        notify_recovery: true,
      },
      has_credentials: false,
      status: 'unverified',
      last_delivery_at: null,
      last_error: null,
    },
  ]
}

/** Envíos de ejemplo: una caída de router avisada y su recuperación (info). */
function seedDeliveries(
  tenant: MockTenant,
  channels: NotificationChannel[],
  now: Date,
): NotificationDelivery[] {
  const email = channels[0]!
  const at = (min: number) => new Date(now.getTime() - min * 60_000).toISOString()
  const base = {
    tenant_id: tenant.tenant_id,
    channel_id: email.id,
    channel_kind: email.kind,
    is_test: false,
    error: null,
  }
  return [
    {
      ...base,
      id: mockUuid('0194c300', hash(tenant.tenant_id + 'd2')),
      status: 'sent',
      event_type: 'router_recovered',
      severity: 'info',
      summary: 'Router «rt-norte-02» recuperado',
      source_event_type: 'horus.snmp.router.state_changed',
      source_event_id: mockUuid('0194c301', hash(tenant.tenant_id + 'e2')),
      created_at: at(47),
      sent_at: at(47),
    },
    {
      ...base,
      id: mockUuid('0194c300', hash(tenant.tenant_id + 'd1')),
      status: 'sent',
      event_type: 'router_offline',
      severity: 'critical',
      summary: 'Router «rt-norte-02» fuera de línea',
      source_event_type: 'horus.snmp.router.state_changed',
      source_event_id: mockUuid('0194c301', hash(tenant.tenant_id + 'e1')),
      created_at: at(63),
      sent_at: at(63),
    },
  ]
}

function channelsOf(tenant: MockTenant, now: Date) {
  let list = mockState.channels.get(tenant.tenant_id)
  if (!list) {
    list = seed(tenant, now)
    mockState.channels.set(tenant.tenant_id, list)
    if (!mockState.deliveries.has(tenant.tenant_id)) {
      mockState.deliveries.set(tenant.tenant_id, seedDeliveries(tenant, list, now))
    }
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
  if (!input.subscription?.event_types?.length && !input.subscription?.categories?.length) {
    return invalid('subscription.event_types', 'Elige al menos una categoría o un tipo de aviso')
  }
  const c = input.config as Record<string, unknown>
  if (input.kind === 'email') {
    const list = (c.recipients as string[] | undefined) ?? []
    if (!list.length || list.some((r) => !/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(r))) {
      return invalid('config.recipients', 'Escribe uno o más correos válidos, separados por comas')
    }
  }
  if (
    input.kind === 'telegram' &&
    !/^-?\d{1,20}$|^@[A-Za-z0-9_]{5,32}$/.test(String(c.chat_id ?? ''))
  ) {
    return invalid(
      'config.chat_id',
      'El chat_id es un número (grupos y canales empiezan por -100) o @canal',
    )
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
  if (channel.kind === 'telegram') {
    return {
      ...base,
      ok: true,
      latency_ms: 95,
      bot_username: '@horus_alertas_bot',
      chat_title: 'NOC Fibra Norte',
    }
  }
  return { ...base, ok: true, latency_ms: 310 }
}

const TOKEN_RE = /^[0-9]+:[A-Za-z0-9_-]{30,}$/
const linkCodes = new Map<string, string>()

/** Asistentes de Telegram: validar bot, código de vinculación y «Detectar chat». */
function telegramRoute(ctx: ChannelRouteContext): Response | undefined {
  const body = ctx.body as Record<string, string | undefined>
  if (ctx.path === '/notification-channels/telegram/bot-validation' && ctx.method === 'POST') {
    if (!TOKEN_RE.test(body.bot_token ?? '')) {
      return invalid(
        'bot_token',
        'Token de bot no válido: es el texto que da @BotFather tras /newbot (número:letras).',
      )
    }
    if (body.bot_token!.includes('rechazado')) {
      return problem(
        422,
        'NOTIFICATION_CHANNEL_AUTH_FAILED',
        'Token rechazado',
        {},
        {
          detail: 'Telegram rechazó el token del bot: Unauthorized',
        },
      )
    }
    return json({
      id: 7012345678,
      username: 'noc_fibranorte_bot',
      first_name: 'NOC Fibra Norte',
      can_join_groups: true,
    })
  }
  if (ctx.path === '/notification-channels/telegram/link-codes' && ctx.method === 'POST') {
    const code =
      'hf' +
      hash(ctx.tenant.tenant_id + ctx.now.getTime())
        .toString(36)
        .padEnd(10, '7')
        .slice(0, 10)
    linkCodes.set(code, ctx.tenant.tenant_id)
    return json(
      {
        code,
        expires_at: new Date(ctx.now.getTime() + 15 * 60_000).toISOString(),
        bot_username: '@horus_alertas_bot',
        start_url: `https://t.me/horus_alertas_bot?start=${code}`,
      },
      201,
    )
  }
  if (ctx.path === '/notification-channels/telegram/chat-detection' && ctx.method === 'POST') {
    if (body.bot_token || body.channel_id) {
      if (body.bot_token?.includes('webhook')) {
        return problem(
          422,
          'NOTIFICATION_TELEGRAM_WEBHOOK_ACTIVE',
          'Webhook activo',
          {},
          {
            detail:
              'El bot tiene un webhook configurado y Telegram no permite leer sus mensajes con getUpdates: elimínelo (deleteWebhook) o use otro bot.',
          },
        )
      }
      return json({
        data: [
          {
            id: '-1002233445566',
            type: 'channel',
            title: 'Alertas NOC',
            username: 'alertas_noc_fn',
          },
          { id: '-1009988776655', type: 'supergroup', title: 'Guardia de red' },
        ],
        bot_username: '@noc_fibranorte_bot',
        shared_bot: false,
      })
    }
    if (!body.code || linkCodes.get(body.code) !== ctx.tenant.tenant_id) {
      return problem(
        422,
        'NOTIFICATION_TELEGRAM_LINK_CODE_INVALID',
        'Código no válido',
        {},
        {
          detail: 'El código no existe o caducó: genere otro.',
        },
      )
    }
    return json({
      data: [{ id: '-1004455667788', type: 'supergroup', title: 'NOC Fibra Norte' }],
      bot_username: null,
      shared_bot: true,
    })
  }
}

export function channelRoute(ctx: ChannelRouteContext): Response | undefined {
  if (!ctx.path.startsWith('/notification-')) return
  if (!ctx.can('alerts.read')) return forbidden()
  const list = channelsOf(ctx.tenant, ctx.now)
  const write = () => ctx.can('alerts.manage')

  if (ctx.path === '/notification-deliveries' && ctx.method === 'GET') {
    const status = new URL(ctx.req.url).searchParams.get('status')
    const deliveries = (mockState.deliveries.get(ctx.tenant.tenant_id) ?? []).filter(
      (d) => !status || d.status === status,
    )
    return json({
      data: deliveries,
      page: { limit: 50, has_more: false, next_cursor: null, prev_cursor: null },
    })
  }
  if (ctx.path.startsWith('/notification-channels/telegram/')) {
    if (!write()) return forbidden()
    return telegramRoute(ctx) ?? notFound('NOT_FOUND')
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
        subscription: { notify_recovery: true, categories: [], ...input.subscription },
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
      severity: 'info',
      summary: 'Notificación de prueba',
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
