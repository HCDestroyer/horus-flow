/**
 * Mock del asistente de IA (assist.yaml): configuración del proveedor por ISP (la clave es
 * write-only: solo se guarda que existe), prueba de conexión, análisis con una respuesta en
 * Markdown predefinida (el servidor simulado añade un retraso), auditoría y tokens MCP. El estado
 * se guarda en `localStorage` para sobrevivir a la navegación con recarga de las pruebas e2e.
 */
import type {
  AssistAuditEntry,
  AssistSettings,
  AssistSettingsUpdate,
  McpToken,
  McpTokenIssued,
} from '~~/types/api'
import { forbidden, json, noContent, notFound, problem } from './http'

interface Ctx {
  method: string
  path: string
  body: Record<string, unknown>
  tenant: { tenant_id: string }
  userId: string
  can: (permission: string) => boolean
  now: Date
}

interface TenantState {
  settings: Omit<AssistSettings, 'usage_today' | 'platform_default' | 'key_store_available'>
  audit: AssistAuditEntry[]
  tokens: McpToken[]
}

const STORAGE_KEY = 'horus.mock.assist'
const DEFAULT_MODELS = { anthropic: 'claude-opus-5-5', openai_compatible: 'llama3.1' } as const
const memory = new Map<string, TenantState>()

function load(): Record<string, TenantState> {
  try {
    return JSON.parse(globalThis.localStorage?.getItem(STORAGE_KEY) ?? '{}') as Record<
      string,
      TenantState
    >
  } catch {
    return {}
  }
}

function persist() {
  try {
    globalThis.localStorage?.setItem(STORAGE_KEY, JSON.stringify(Object.fromEntries(memory)))
  } catch {
    // Sin almacenamiento (pestaña privada): queda en memoria.
  }
}

function state(ctx: Ctx): TenantState {
  let s = memory.get(ctx.tenant.tenant_id)
  if (!s) {
    s = load()[ctx.tenant.tenant_id] ?? {
      settings: {
        enabled: false,
        provider: 'anthropic',
        model: DEFAULT_MODELS.anthropic,
        base_url: null,
        effort: 'medium',
        daily_request_limit: 100,
        api_key_set: false,
        api_key_source: 'none',
        source: 'platform',
        mcp_enabled: true,
        updated_at: null,
        updated_by: null,
        version: 0,
      },
      audit: [],
      tokens: [],
    }
    memory.set(ctx.tenant.tenant_id, s)
  }
  return s
}

const startOfDay = (d: Date) => Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate())

function usageToday(s: TenantState, now: Date) {
  const from = startOfDay(now)
  return s.audit.filter(
    (a) =>
      (a.feature === 'finding' || a.feature === 'router') &&
      ['success', 'refused', 'error'].includes(a.outcome) &&
      Date.parse(a.created_at) >= from,
  ).length
}

function view(s: TenantState, now: Date): AssistSettings {
  return {
    ...s.settings,
    usage_today: usageToday(s, now),
    key_store_available: true,
    platform_default: {
      enabled: false,
      provider: 'anthropic',
      model: DEFAULT_MODELS.anthropic,
      api_key_set: false,
    },
  }
}

function usable(s: TenantState) {
  return (
    s.settings.enabled && (s.settings.provider === 'openai_compatible' || s.settings.api_key_set)
  )
}

function record(s: TenantState, ctx: Ctx, entry: Partial<AssistAuditEntry>) {
  s.audit.unshift({
    id: crypto.randomUUID(),
    user_id: ctx.userId,
    feature: 'finding',
    context_id: '',
    provider: s.settings.provider,
    model: s.settings.model,
    input_tokens: 0,
    output_tokens: 0,
    tool_calls: [],
    duration_ms: 0,
    outcome: 'success',
    error_code: null,
    created_at: ctx.now.toISOString(),
    ...entry,
  })
  persist()
}

const invalid = (errors: { field: string; code: string; message: string }[]) =>
  problem(422, 'VALIDATION_FAILED', 'Revisa los campos marcados', {}, { errors })

function update(ctx: Ctx, s: TenantState) {
  const b = ctx.body as Partial<AssistSettingsUpdate>
  const provider = b.provider
  const errors: { field: string; code: string; message: string }[] = []
  if (provider !== 'anthropic' && provider !== 'openai_compatible') {
    errors.push({ field: 'provider', code: 'INVALID_VALUE', message: 'Proveedor no admitido.' })
  }
  const limit = Number(b.daily_request_limit)
  if (!Number.isInteger(limit) || limit < 1 || limit > 10000) {
    errors.push({
      field: 'daily_request_limit',
      code: 'OUT_OF_RANGE',
      message: 'Entre 1 y 10 000 consultas por día.',
    })
  }
  const base = typeof b.base_url === 'string' ? b.base_url.trim().replace(/\/+$/, '') : ''
  if (provider === 'openai_compatible' && !/^https?:\/\/[^\s/@]+/.test(base)) {
    errors.push({
      field: 'base_url',
      code: base ? 'INVALID_VALUE' : 'REQUIRED',
      message: base
        ? 'URL no válida'
        : 'Indica la URL del servidor (p. ej. http://10.0.0.5:11434).',
    })
  }
  const key = typeof b.api_key === 'string' ? b.api_key.trim() : undefined
  if (key !== undefined && (key.length < 8 || key.length > 512)) {
    errors.push({ field: 'api_key', code: 'INVALID_VALUE', message: 'Entre 8 y 512 caracteres.' })
  }
  let keySet = s.settings.api_key_set && s.settings.provider === provider && !b.clear_api_key
  if (key) keySet = true
  if (b.enabled && provider === 'anthropic' && !keySet) {
    errors.push({
      field: 'api_key',
      code: 'REQUIRED',
      message: 'Para activar Claude hace falta una clave de API (del ISP o de plataforma).',
    })
  }
  if (errors.length) return invalid(errors)
  const p = provider as 'anthropic' | 'openai_compatible'
  s.settings = {
    ...s.settings,
    enabled: !!b.enabled,
    provider: p,
    model: (b.model ?? '').trim() || DEFAULT_MODELS[p],
    base_url: p === 'openai_compatible' ? base : null,
    effort: b.effort ?? 'medium',
    daily_request_limit: limit,
    api_key_set: keySet,
    api_key_source: keySet ? 'tenant' : 'none',
    source: 'tenant',
    updated_at: ctx.now.toISOString(),
    updated_by: ctx.userId,
    version: s.settings.version + 1,
  }
  persist()
  return json(view(s, ctx.now))
}

/** Respuesta predefinida (Markdown) con la forma que pide el prompt del asistente. */
function cannedMarkdown(feature: string) {
  if (feature === 'router') {
    return [
      '## Qué está pasando',
      'El exportador del router está **silencioso**: el último flujo llegó hace más de 10 minutos, aunque el túnel WireGuard sigue activo (último handshake hace 40 s). El alta está completa (`exporting`).',
      '',
      '## Posibles causas',
      '- Traffic Flow se desactivó o perdió el destino (`traffic_flow_target_drift`).',
      '- Un cambio de firewall bloquea UDP 2055 hacia el colector por el túnel.',
      '- Reinicio del router con una configuración antigua.',
      '',
      '## Qué hacer',
      '1. Comprueba Traffic Flow y su destino (solo lectura):',
      '```routeros',
      '/ip traffic-flow print',
      '/ip traffic-flow target print',
      '```',
      '2. Si el destino no es el colector de Horus, corrígelo:',
      '```routeros',
      '/ip traffic-flow target set [find] dst-address=10.255.0.1 port=2055',
      '```',
      '',
      '## Para deshacer',
      '- Vuelve a poner el destino anterior que anotaste con `/ip traffic-flow target print`.',
    ].join('\n')
  }
  return [
    '## Qué está pasando',
    'Cliente 1 (10.20.x.x) muestra **señales compatibles con** un equipo infectado que contacta con un servidor de mando y control: 42 conexiones a 203.0.113.9:443 (AS64500, listado en Feodo Tracker) en 2 horas, con un intervalo muy regular.',
    '',
    '## Posibles causas',
    '- Un dispositivo del hogar (cámara IP, router doméstico) con malware.',
    '- Falso positivo: un servicio legítimo alojado en la misma IP compartida. Es poco probable por la regularidad del patrón.',
    '',
    '## Qué hacer',
    '1. Avisa al cliente y pídele que reinicie y actualice sus dispositivos.',
    '2. Si el patrón sigue, bloquea solo el destino C2 para ese cliente:',
    '```routeros',
    '/ip firewall address-list add list=horus-c2 address=203.0.113.9 comment="horus hallazgo"',
    '/ip firewall filter add chain=forward dst-address-list=horus-c2 action=drop comment="horus hallazgo"',
    '```',
    '',
    '> Mensaje sugerido: «Detectamos actividad inusual en un equipo de tu red. Te recomendamos reiniciarlo y actualizarlo.»',
    '',
    '## Para deshacer',
    '```routeros',
    '/ip firewall filter remove [find comment="horus hallazgo"]',
    '/ip firewall address-list remove [find list=horus-c2]',
    '```',
  ].join('\n')
}

const READ: Record<string, string> = { finding: 'security.findings.read', router: 'devices.read' }

function analyze(ctx: Ctx, s: TenantState) {
  const feature = String(ctx.body.feature ?? '')
  const contextId = String(ctx.body.context_id ?? '')
  if (!READ[feature]) {
    return invalid([{ field: 'feature', code: 'INVALID_VALUE', message: 'Feature no admitida.' }])
  }
  if (!ctx.can(READ[feature]!)) return forbidden()
  if (!s.settings.enabled) {
    return problem(
      409,
      'ASSIST_DISABLED',
      'Asistente desactivado',
      {},
      {
        detail:
          'El asistente de IA no está activado en este ISP (Administración › Inteligencia artificial).',
      },
    )
  }
  if (!usable(s)) return problem(409, 'ASSIST_NOT_CONFIGURED', 'Falta la clave de API')
  if (usageToday(s, ctx.now) >= s.settings.daily_request_limit) {
    record(s, ctx, {
      feature: feature as 'finding',
      context_id: contextId,
      outcome: 'limited',
      error_code: 'ASSIST_LIMIT_REACHED',
    })
    return problem(
      429,
      'ASSIST_LIMIT_REACHED',
      'Límite diario alcanzado',
      { 'Retry-After': '3600' },
      {
        detail: `Se alcanzó el límite de ${s.settings.daily_request_limit} consultas de hoy en este ISP.`,
      },
    )
  }
  if (s.settings.model.includes('fail')) {
    record(s, ctx, {
      feature: feature as 'finding',
      context_id: contextId,
      outcome: 'error',
      error_code: 'PROVIDER_UNAVAILABLE',
    })
    return problem(
      502,
      'ASSIST_PROVIDER_UNAVAILABLE',
      'Proveedor no disponible',
      {},
      {
        detail: 'No se pudo contactar con el proveedor.',
      },
    )
  }
  const tools =
    feature === 'router'
      ? [
          { name: 'get_router', count: 1 },
          { name: 'get_exporter_status', count: 1 },
        ]
      : [
          { name: 'get_finding', count: 1 },
          { name: 'reputation_lookup', count: 1 },
        ]
  const usage = { input_tokens: 6120, output_tokens: 840 }
  const id = crypto.randomUUID()
  record(s, ctx, {
    id,
    feature: feature as 'finding',
    context_id: contextId,
    tool_calls: tools,
    duration_ms: 9400,
    ...usage,
  })
  return json({
    id,
    feature,
    context_id: contextId,
    markdown: cannedMarkdown(feature),
    provider: s.settings.provider,
    model: s.settings.model,
    generated_at: ctx.now.toISOString(),
    tool_calls: tools,
    usage,
    duration_ms: 9400,
    truncated: false,
    disclaimer: 'Generado por IA; revisa antes de aplicar. Horus no ejecuta nada en tus routers.',
  })
}

export function assistRoute(ctx: Ctx): Response | undefined {
  if (!ctx.path.startsWith('/assist/')) return undefined
  const s = state(ctx)
  const route = `${ctx.method} ${ctx.path}`
  switch (route) {
    case 'GET /assist/status':
      return json({
        enabled: usable(s),
        provider: s.settings.provider,
        model: s.settings.model,
        remaining_today: Math.max(0, s.settings.daily_request_limit - usageToday(s, ctx.now)),
      })
    case 'GET /assist/settings':
      return ctx.can('settings.read') ? json(view(s, ctx.now)) : forbidden()
    case 'PUT /assist/settings':
      return ctx.can('settings.manage') ? update(ctx, s) : forbidden()
    case 'POST /assist/settings/test': {
      if (!ctx.can('settings.manage')) return forbidden()
      const ok =
        !s.settings.model.includes('fail') &&
        (s.settings.provider !== 'anthropic' || s.settings.api_key_set)
      record(s, ctx, {
        feature: 'test',
        outcome: ok ? 'success' : 'error',
        duration_ms: 420,
        error_code: ok ? null : 'PROVIDER_UNAUTHORIZED',
      })
      return json({
        ok,
        provider: s.settings.provider,
        model: s.settings.model,
        latency_ms: 420,
        ...(ok ? {} : { error_code: 'PROVIDER_UNAUTHORIZED' }),
        message: ok ? 'Conexión correcta.' : 'El proveedor rechazó la clave de API.',
      })
    }
    case 'POST /assist/analyses':
      return analyze(ctx, s)
    case 'GET /assist/audit':
      if (!ctx.can('settings.read')) return forbidden()
      return json({
        data: s.audit.slice(0, 50),
        page: { limit: 50, has_more: false, next_cursor: null, prev_cursor: null },
      })
    case 'GET /assist/mcp-tokens':
      return ctx.can('api_tokens.manage') ? json({ data: s.tokens }) : forbidden()
    case 'POST /assist/mcp-tokens': {
      if (!ctx.can('api_tokens.manage')) return forbidden()
      const name = typeof ctx.body.name === 'string' ? ctx.body.name.trim() : ''
      if (!name || name.length > 80) {
        return invalid([
          { field: 'name', code: 'INVALID_VALUE', message: 'Entre 1 y 80 caracteres.' },
        ])
      }
      const token = `hmcp_${crypto.randomUUID().replaceAll('-', '')}`
      const row: McpToken = {
        id: crypto.randomUUID(),
        name,
        prefix: token.slice(0, 9),
        user_id: ctx.userId,
        created_at: ctx.now.toISOString(),
        expires_at: new Date(ctx.now.getTime() + 90 * 86_400_000).toISOString(),
        last_used_at: null,
        revoked_at: null,
        active: true,
      }
      s.tokens.unshift(row)
      persist()
      return json({ ...row, token } satisfies McpTokenIssued, 201)
    }
  }
  const del = ctx.path.match(/^\/assist\/mcp-tokens\/([^/]+)$/)
  if (del && ctx.method === 'DELETE') {
    if (!ctx.can('api_tokens.manage')) return forbidden()
    const tk = s.tokens.find((x) => x.id === del[1])
    if (!tk) return notFound('MCP_TOKEN_NOT_FOUND')
    tk.revoked_at = ctx.now.toISOString()
    tk.active = false
    persist()
    return noContent()
  }
  return undefined
}

/** Solo tests: vacía el estado simulado. */
export function resetAssistMock() {
  memory.clear()
  try {
    globalThis.localStorage?.removeItem(STORAGE_KEY)
  } catch {
    // sin almacenamiento
  }
}
