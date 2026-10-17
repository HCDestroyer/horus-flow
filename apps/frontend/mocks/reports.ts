import type { ReportDefinition, ReportFormat, ReportRun } from '~~/types/api'
import type { MockTenant } from './data'
import { forbidden, ifMatch, json, notFound, paginate, problem } from './http'
import { hash, mockUuid } from './random'
import { mockState } from './state'
import type { TenantRouteContext } from './tenant-routes'

/**
 * Reportes del ISP simulados (rol reporting, I3): ejecuciones semanales programadas de las
 * últimas semanas, una a petición fallida, generación a petición (en cola → generándose →
 * lista en unos segundos del reloj simulado), descarga de un PDF / zip mínimos y la
 * programación semanal con If-Match. Sin datos de clientes.
 */

/** Segundos simulados que tarda una ejecución a petición en cada fase. */
const QUEUED_S = 2
const RUNNING_S = 5

export interface ReportsState {
  runs: ReportRun[]
  definition: ReportDefinition
}

const TZ = 'America/Mexico_City'

/** Lunes 00:00 de la semana ISO de `t` en UTC−6 (la zona de los ISP simulados, sin horario de verano). */
function weekStartMx(t: Date) {
  const local = new Date(t.getTime() - 6 * 3_600_000)
  const back = (local.getUTCDay() + 6) % 7
  const day = Date.UTC(local.getUTCFullYear(), local.getUTCMonth(), local.getUTCDate() - back)
  return new Date(day + 6 * 3_600_000)
}

function isoWeek(d: Date) {
  const local = new Date(d.getTime() - 6 * 3_600_000)
  const t = new Date(Date.UTC(local.getUTCFullYear(), local.getUTCMonth(), local.getUTCDate()))
  const day = (t.getUTCDay() + 6) % 7
  t.setUTCDate(t.getUTCDate() - day + 3)
  const firstThursday = new Date(Date.UTC(t.getUTCFullYear(), 0, 4))
  const week =
    1 +
    Math.round(
      ((t.getTime() - firstThursday.getTime()) / 86_400_000 -
        3 +
        ((firstThursday.getUTCDay() + 6) % 7)) /
        7,
    )
  return `${t.getUTCFullYear()}-W${String(week).padStart(2, '0')}`
}

function files(id: string, formats: ReportFormat[], seed: number) {
  return formats.map((format) => ({
    format,
    size_bytes: format === 'pdf' ? 42_000 + (seed % 9000) : 5_200 + (seed % 900),
    sha256: (hash(id + format).toString(16) + randomHexStable(id + format))
      .slice(0, 64)
      .padEnd(64, '0'),
    download_path: `/api/v1/reports/${id}/download?format=${format}`,
  }))
}

function randomHexStable(seed: string) {
  let out = ''
  for (let i = 0; out.length < 64; i++)
    out += hash(seed + i)
      .toString(16)
      .padStart(8, '0')
  return out
}

function iso(ms: number) {
  return new Date(ms).toISOString()
}

function seed(tenant: MockTenant, now: Date): ReportsState {
  const defId = mockUuid('0196a0d0', hash(tenant.tenant_id))
  const created = now.getTime() - 60 * 86_400_000
  const definition: ReportDefinition = {
    id: defId,
    tenant_id: tenant.tenant_id,
    kind: 'security_weekly',
    enabled: true,
    weekday: 1,
    hour: 7,
    minute: 0,
    timezone: TZ,
    formats: ['pdf', 'csv'],
    recipients: [],
    next_run_at: null,
    email_available: true,
    version: 1,
    created_at: iso(created),
    updated_at: iso(created),
  }
  const runs: ReportRun[] = []
  const thisWeek = weekStartMx(now)
  for (let i = 1; i <= 5; i++) {
    const from = thisWeek.getTime() - i * 7 * 86_400_000
    const to = from + 7 * 86_400_000
    const id = mockUuid('0196a1a0', hash(tenant.tenant_id + i))
    const done = to + 7 * 3_600_000 + 41_000
    runs.push({
      id,
      tenant_id: tenant.tenant_id,
      kind: 'security_weekly',
      status: 'completed',
      definition_id: defId,
      scheduled: true,
      period_from: iso(from),
      period_to: iso(to),
      timezone: TZ,
      iso_week: isoWeek(new Date(from)),
      formats: ['pdf', 'csv'],
      files: files(id, ['pdf', 'csv'], hash(id)),
      size_bytes: 0,
      error: null,
      created_by: null,
      created_at: iso(to + 7 * 3_600_000),
      started_at: iso(to + 7 * 3_600_000 + 1000),
      completed_at: iso(done),
      expires_at: iso(done + 365 * 86_400_000),
      expired: false,
    })
  }
  const failedId = mockUuid('0196a1f0', hash(tenant.tenant_id))
  const fFrom = thisWeek.getTime() - 9 * 86_400_000
  runs.push({
    id: failedId,
    tenant_id: tenant.tenant_id,
    kind: 'security_weekly',
    status: 'failed',
    definition_id: null,
    scheduled: false,
    period_from: iso(fFrom),
    period_to: iso(fFrom + 3 * 86_400_000),
    timezone: TZ,
    iso_week: isoWeek(new Date(fFrom)),
    formats: ['pdf'],
    files: [],
    size_bytes: 0,
    error: 'No se pudo generar el archivo PDF (error interno o de escritura en el almacén local).',
    created_by: mockUuid('0192aaaa', 1),
    created_at: iso(thisWeek.getTime() - 2 * 86_400_000),
    started_at: iso(thisWeek.getTime() - 2 * 86_400_000 + 1000),
    completed_at: iso(thisWeek.getTime() - 2 * 86_400_000 + 9000),
    expires_at: null,
    expired: false,
  })
  for (const r of runs) r.size_bytes = r.files.reduce((n, f) => n + f.size_bytes, 0)
  return { runs, definition }
}

export function reportsOf(tenant: MockTenant, now: Date) {
  let st = mockState.reports.get(tenant.tenant_id)
  if (!st) {
    st = seed(tenant, now)
    mockState.reports.set(tenant.tenant_id, st)
  }
  return st
}

/** Avanza las ejecuciones a petición con el reloj simulado. */
function advance(st: ReportsState, now: Date) {
  for (const r of st.runs) {
    if (r.status !== 'queued' && r.status !== 'running') continue
    const age = (now.getTime() - new Date(r.created_at).getTime()) / 1000
    if (age >= QUEUED_S + RUNNING_S) {
      r.status = 'completed'
      r.started_at ??= iso(new Date(r.created_at).getTime() + QUEUED_S * 1000)
      r.completed_at = now.toISOString()
      r.expires_at = iso(now.getTime() + 365 * 86_400_000)
      r.files = files(r.id, r.formats, hash(r.id))
      r.size_bytes = r.files.reduce((n, f) => n + f.size_bytes, 0)
    } else if (age >= QUEUED_S) {
      r.status = 'running'
      r.started_at ??= now.toISOString()
    }
  }
}

/** Próxima ejecución (lunes 07:00 por defecto) en UTC−6. */
function nextRun(d: ReportDefinition, now: Date) {
  if (!d.enabled) return null
  const ws = weekStartMx(now).getTime()
  const at = (week: number) =>
    ws + week * 7 * 86_400_000 + (d.weekday - 1) * 86_400_000 + (d.hour * 60 + d.minute) * 60_000
  const run = at(0) > now.getTime() ? at(0) : at(1)
  return iso(run)
}

function definitionView(st: ReportsState, now: Date): ReportDefinition {
  return { ...st.definition, next_run_at: nextRun(st.definition, now) }
}

const PDF_BYTES = `%PDF-1.4
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj
3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] >> endobj
trailer << /Root 1 0 R >>
%%EOF
`

/** Zip vacío válido (registro de fin de directorio central). */
const EMPTY_ZIP = new Uint8Array([0x50, 0x4b, 0x05, 0x06, ...new Array(18).fill(0)])

export function reportRoutes(ctx: TenantRouteContext): Response | undefined {
  if (!ctx.path.startsWith('/reports') && !ctx.path.startsWith('/report-definitions')) {
    return undefined
  }
  if (!ctx.can('reports.read')) return forbidden()
  const st = reportsOf(ctx.tenant, ctx.now)
  advance(st, ctx.now)

  if (ctx.path === '/report-definitions' && ctx.method === 'GET') {
    return json({ data: [definitionView(st, ctx.now)] })
  }
  const defMatch = ctx.path.match(/^\/report-definitions\/([^/]+)$/)
  if (defMatch && ctx.method === 'PATCH') {
    if (defMatch[1] !== st.definition.id) return notFound('REPORT_DEFINITION_NOT_FOUND')
    if (!ctx.can('settings.manage')) return forbidden()
    const v = ifMatch(ctx.req)
    if (v === null) return problem(428, 'PRECONDITION_REQUIRED', 'Falta If-Match')
    if (v !== st.definition.version) {
      return problem(
        412,
        'PRECONDITION_FAILED',
        'El recurso cambió mientras lo editabas',
        {},
        { current: definitionView(st, ctx.now) as unknown as Record<string, unknown> },
      )
    }
    const b = ctx.body as Partial<ReportDefinition>
    const errors: { field: string; code: string; message: string }[] = []
    if (b.hour !== undefined && (b.hour < 0 || b.hour > 23)) {
      errors.push({ field: 'hour', code: 'INVALID_VALUE', message: 'Hora de 0 a 23.' })
    }
    if (b.formats !== undefined && !b.formats.length) {
      errors.push({ field: 'formats', code: 'INVALID_VALUE', message: 'Uno o más de: pdf, csv.' })
    }
    for (const [i, r] of (b.recipients ?? []).entries()) {
      if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(r)) {
        errors.push({
          field: `recipients.${i}`,
          code: 'INVALID_EMAIL',
          message: 'Correo no válido.',
        })
      }
    }
    if (errors.length) {
      return problem(422, 'VALIDATION_FAILED', 'Revisa los campos marcados', {}, { errors })
    }
    st.definition = {
      ...st.definition,
      ...b,
      recipients: b.recipients
        ? [...new Set(b.recipients.map((r) => r.trim().toLowerCase()))]
        : st.definition.recipients,
      version: st.definition.version + 1,
      updated_at: ctx.now.toISOString(),
    }
    return json(definitionView(st, ctx.now), 200, { ETag: `"${st.definition.version}"` })
  }

  if (ctx.path === '/reports') {
    if (ctx.method === 'GET') {
      const kind = ctx.url.searchParams.get('kind')
      const list = [...st.runs]
        .filter((r) => !kind || r.kind === kind)
        .sort((a, b) => b.created_at.localeCompare(a.created_at))
      return json(paginate(ctx.empty ? [] : list, ctx.url, 20))
    }
    if (ctx.method === 'POST') {
      if (!ctx.can('reports.export')) return forbidden()
      const key = ctx.req.headers.get('Idempotency-Key')
      if (!key) return problem(428, 'PRECONDITION_REQUIRED', 'Falta Idempotency-Key')
      const b = ctx.body as { from?: string; to?: string; formats?: ReportFormat[] }
      const thisWeek = weekStartMx(ctx.now).getTime()
      let from = thisWeek - 7 * 86_400_000
      let to = thisWeek
      if (b.from || b.to) {
        from = new Date(b.from ?? '').getTime()
        to = Math.min(new Date(b.to ?? '').getTime(), ctx.now.getTime())
        if (!(from < to) || to - from > 31 * 86_400_000) {
          return problem(
            422,
            'VALIDATION_FAILED',
            'Revisa los campos marcados',
            {},
            {
              errors: [
                {
                  field: 'to',
                  code: 'INVALID_RANGE',
                  message: 'El rango no puede superar 31 días.',
                },
              ],
            },
          )
        }
      }
      const id = mockUuid('0196a2a0', hash(key))
      const existing = st.runs.find((r) => r.id === id)
      if (existing)
        return json(existing, 202, {
          Location: `/api/v1/reports/${id}`,
          'Idempotency-Replayed': 'true',
        })
      const run: ReportRun = {
        id,
        tenant_id: ctx.tenant.tenant_id,
        kind: 'security_weekly',
        status: 'queued',
        definition_id: null,
        scheduled: false,
        period_from: iso(from),
        period_to: iso(to),
        timezone: TZ,
        iso_week: isoWeek(new Date(from)),
        formats: b.formats?.length ? b.formats : ['pdf', 'csv'],
        files: [],
        size_bytes: 0,
        error: null,
        created_by: ctx.userId,
        created_at: ctx.now.toISOString(),
        started_at: null,
        completed_at: null,
        expires_at: null,
        expired: false,
      }
      st.runs.unshift(run)
      return json(run, 202, { Location: `/api/v1/reports/${id}` })
    }
  }

  const m = ctx.path.match(/^\/reports\/([^/]+)(\/download)?$/)
  if (m && ctx.method === 'GET') {
    const run = st.runs.find((r) => r.id === m[1])
    if (!run) return notFound('REPORT_NOT_FOUND')
    if (!m[2]) return json(run)
    const format = (ctx.url.searchParams.get('format') ?? 'pdf') as ReportFormat
    if (run.status !== 'completed' || !run.files.some((f) => f.format === format)) {
      return problem(409, 'REPORT_NOT_READY', 'Reporte no disponible')
    }
    const body = format === 'pdf' ? PDF_BYTES : EMPTY_ZIP
    const name = `horus-seguridad-${run.iso_week}-${run.id.slice(0, 8)}${format === 'pdf' ? '.pdf' : '-csv.zip'}`
    return new Response(body, {
      status: 200,
      headers: {
        'Content-Type': format === 'pdf' ? 'application/pdf' : 'application/zip',
        'Content-Disposition': `attachment; filename="${name}"`,
        'Cache-Control': 'no-store',
      },
    })
  }
  return undefined
}
