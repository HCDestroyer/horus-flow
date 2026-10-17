import { beforeEach, describe, expect, it } from 'vitest'
import { tokenForPath } from '~/composables/useAuth'
import { ApiError, createAuthFetch, createHorusClient, unwrap } from '~/utils/api-client'
import {
  addCalendarDays,
  dayRangeToPeriod,
  filenameFrom,
  formatInZone,
  formatReportPeriod,
  isReportPending,
  parseIsoWeek,
  REPORT_STATUS_META,
  todayInZone,
  zonedMidnight,
} from '~/utils/reports'
import { MOCK_PASSWORD, MOCK_TOTP_CODE, TENANTS } from '~~/mocks/data'
import { createMockFetch } from '~~/mocks/server'
import { resetMockState } from '~~/mocks/state'

/** Reportes del ISP (I3): utilidades de periodo/zona y API simulada. */

describe('utilidades de reportes', () => {
  it('formatea el periodo en la zona del ISP con el último minuto incluido', () => {
    const run = {
      period_from: '2026-10-05T06:00:00.000Z',
      period_to: '2026-10-12T06:00:00.000Z',
      timezone: 'America/Mexico_City',
    }
    expect(formatReportPeriod(run)).toBe('05/10/2026 00:00 – 11/10/2026 23:59')
    expect(formatInZone('2026-10-12T13:00:00Z', 'America/Bogota')).toBe('12/10/2026 08:00')
  })

  it('convierte días locales a instantes UTC respetando el cambio de horario', () => {
    // Madrid: 25/10/2026 cambia de CEST (UTC+2) a CET (UTC+1).
    expect(zonedMidnight('2026-10-19', 'Europe/Madrid').toISOString()).toBe(
      '2026-10-18T22:00:00.000Z',
    )
    expect(zonedMidnight('2026-10-26', 'Europe/Madrid').toISOString()).toBe(
      '2026-10-25T23:00:00.000Z',
    )
    const p = dayRangeToPeriod('2026-10-19', '2026-10-25', 'Europe/Madrid')
    expect(p).toEqual({ from: '2026-10-18T22:00:00.000Z', to: '2026-10-25T23:00:00.000Z' })
    expect(dayRangeToPeriod('2026-10-10', '2026-10-01', 'UTC')).toBeNull()
    expect(dayRangeToPeriod('2026-08-01', '2026-10-01', 'UTC')).toBeNull()
    expect(addCalendarDays('2026-12-31', 1)).toBe('2027-01-01')
    expect(todayInZone('America/Mexico_City', new Date('2026-10-12T03:00:00Z'))).toBe('2026-10-11')
  })

  it('estados con icono y color (nunca solo color) y utilidades varias', () => {
    expect(REPORT_STATUS_META.completed.color).toBe('success')
    expect(REPORT_STATUS_META.failed.color).toBe('error')
    expect(REPORT_STATUS_META.running.spinning).toBe(true)
    expect(isReportPending({ status: 'queued' })).toBe(true)
    expect(isReportPending({ status: 'completed' })).toBe(false)
    expect(parseIsoWeek('2026-W41')).toEqual({ year: 2026, week: 41 })
    expect(filenameFrom('attachment; filename="horus-seguridad-2026-W41.pdf"', 'x')).toBe(
      'horus-seguridad-2026-W41.pdf',
    )
    expect(filenameFrom(null, 'por-defecto.pdf')).toBe('por-defecto.pdf')
  })
})

const fn = TENANTS.fibraNorte

function memoryStorage() {
  const m = new Map<string, string>()
  return {
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, v),
    removeItem: (k: string) => void m.delete(k),
  }
}

async function adminClient(clock: { now: Date }) {
  let session: string | null = null
  let scoped: { token: string } | null = null
  const api = createHorusClient(
    'http://horus.test/api/v1',
    createAuthFetch({
      fetch: createMockFetch({ latencyMs: 0, storage: memoryStorage(), now: () => clock.now }),
      basePath: '/api/v1',
      getAccessToken: (path) => tokenForPath(path, session, scoped),
    }),
  )
  const login = await unwrap(
    api.POST('/auth/login', {
      body: { username: 'ana.ruiz@fibranorte.example', password: MOCK_PASSWORD },
    }),
  )
  if (!('mfa_token' in login)) throw new Error('se esperaba 2FA')
  const verified = await unwrap(
    api.POST('/auth/mfa/verify', { body: { mfa_token: login.mfa_token, code: MOCK_TOTP_CODE } }),
  )
  session = verified.access_token
  scoped = {
    token: (await unwrap(api.POST('/auth/token', { body: { tenant_id: fn.tenant_id } })))
      .access_token,
  }
  return api
}

describe('API simulada de reportes', () => {
  beforeEach(() => resetMockState())

  it('lista, genera a petición (idempotente), avanza con el reloj y descarga', async () => {
    const clock = { now: new Date('2026-10-14T15:00:00Z') }
    const api = await adminClient(clock)
    const list = await unwrap(api.GET('/reports'))
    expect(list.data.length).toBeGreaterThanOrEqual(5)
    expect(list.data.some((r) => r.status === 'failed' && r.error)).toBe(true)
    expect(list.data.every((r) => r.tenant_id === fn.tenant_id)).toBe(true)

    const key = crypto.randomUUID()
    const created = await api.POST('/reports', {
      params: { header: { 'Idempotency-Key': key } },
      body: { kind: 'security_weekly' },
    })
    expect(created.response.status).toBe(202)
    const run = created.data!
    expect(run.status).toBe('queued')
    expect(created.response.headers.get('Location')).toBe(`/api/v1/reports/${run.id}`)
    const again = await api.POST('/reports', {
      params: { header: { 'Idempotency-Key': key } },
      body: { kind: 'security_weekly' },
    })
    expect(again.data!.id).toBe(run.id)
    expect(again.response.headers.get('Idempotency-Replayed')).toBe('true')

    // Sin archivo todavía → 409.
    const early = await api.GET('/reports/{report_id}/download', {
      params: { path: { report_id: run.id }, query: { format: 'pdf' } },
      parseAs: 'blob',
    })
    expect(early.response.status).toBe(409)

    clock.now = new Date(clock.now.getTime() + 10_000)
    const done = await unwrap(
      api.GET('/reports/{report_id}', { params: { path: { report_id: run.id } } }),
    )
    expect(done.status).toBe('completed')
    expect(done.files.map((f) => f.format)).toEqual(['pdf', 'csv'])
    const file = await api.GET('/reports/{report_id}/download', {
      params: { path: { report_id: run.id }, query: { format: 'pdf' } },
      parseAs: 'blob',
    })
    expect(file.response.headers.get('Content-Disposition')).toMatch(
      /attachment; filename=".+\.pdf"/,
    )
    const text = await (file.data as unknown as Blob).text()
    expect(text.startsWith('%PDF-')).toBe(true)

    // Rango de más de 31 días → 422.
    const bad = api.POST('/reports', {
      params: { header: { 'Idempotency-Key': crypto.randomUUID() } },
      body: { from: '2026-08-01T00:00:00Z', to: '2026-10-01T00:00:00Z' },
    })
    await expect(unwrap(bad)).rejects.toBeInstanceOf(ApiError)
  })

  it('programación semanal con If-Match y validación de correos', async () => {
    const clock = { now: new Date('2026-10-14T15:00:00Z') }
    const api = await adminClient(clock)
    const defs = await unwrap(api.GET('/report-definitions'))
    const def = defs.data[0]!
    expect(def.enabled).toBe(true)
    expect(def.next_run_at).toBe('2026-10-19T13:00:00.000Z') // lunes 07:00 en México
    const updated = await unwrap(
      api.PATCH('/report-definitions/{definition_id}', {
        params: { path: { definition_id: def.id }, header: { 'If-Match': `"${def.version}"` } },
        body: { weekday: 3, recipients: ['NOC@fibranorte.example'] },
      }),
    )
    expect(updated.version).toBe(def.version + 1)
    expect(updated.recipients).toEqual(['noc@fibranorte.example'])
    const stale = api.PATCH('/report-definitions/{definition_id}', {
      params: { path: { definition_id: def.id }, header: { 'If-Match': `"${def.version}"` } },
      body: { enabled: false },
    })
    await expect(unwrap(stale)).rejects.toMatchObject({ status: 412 })
    const invalid = api.PATCH('/report-definitions/{definition_id}', {
      params: { path: { definition_id: def.id }, header: { 'If-Match': `"${updated.version}"` } },
      body: { recipients: ['no-es-correo'] },
    })
    await expect(unwrap(invalid)).rejects.toMatchObject({ status: 422 })
  })
})
