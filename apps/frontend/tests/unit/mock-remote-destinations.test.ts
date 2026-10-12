import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { tokenForPath } from '~/composables/useAuth'
import { ApiError, createAuthFetch, createHorusClient, unwrap } from '~/utils/api-client'
import { MOCK_PASSWORD, MOCK_TOTP_CODE } from '~~/mocks/data'
import { createMockFetch } from '~~/mocks/server'
import { resetMockState } from '~~/mocks/state'

/**
 * API simulada de destinos remotos SFTP: alta, credenciales write-only, prueba con TOFU de la
 * huella (y cambio de clave), activación, copias (`pending` → `ok`, `SYNC_RUNNING`,
 * `NOT_READY`), edición con If-Match (412/428), cambio de servidor, borrado y re-autenticación.
 */

const ADMIN = 'ana.ruiz@fibranorte.example'
const KEY = '-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----\n'

function memoryStorage() {
  const map = new Map<string, string>()
  return {
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => void map.set(k, v),
    removeItem: (k: string) => void map.delete(k),
  }
}

let local: ReturnType<typeof memoryStorage>
let clock: number
beforeEach(() => {
  local = memoryStorage()
  vi.stubGlobal('localStorage', local)
  clock = Date.parse('2026-10-12T10:00:00Z')
})
afterEach(() => {
  vi.unstubAllGlobals()
  resetMockState()
})

async function client(storage = memoryStorage()) {
  const tokens: { session: string | null; scoped: { token: string } | null } = {
    session: null,
    scoped: null,
  }
  const api = createHorusClient(
    'http://horus.test/api/v1',
    createAuthFetch({
      fetch: createMockFetch({
        latencyMs: 0,
        storage,
        now: () => new Date(clock),
        tokenTtlMs: 86_400_000,
      }),
      basePath: '/api/v1',
      getAccessToken: (path) => tokenForPath(path, tokens.session, tokens.scoped),
    }),
  )
  const res = await unwrap(
    api.POST('/auth/login', { body: { username: ADMIN, password: MOCK_PASSWORD } }),
  )
  if (!('mfa_required' in res)) throw new Error('se esperaba 2FA')
  const session = await unwrap(
    api.POST('/auth/mfa/verify', { body: { mfa_token: res.mfa_token, code: MOCK_TOTP_CODE } }),
  )
  tokens.session = session.access_token
  const scoped = await unwrap(api.POST('/auth/token', { body: { scope: 'platform' } }))
  tokens.scoped = { token: scoped.access_token }
  return { api, storage }
}
type Api = Awaited<ReturnType<typeof client>>['api']

async function fails(p: Promise<unknown>) {
  try {
    await p
  } catch (e) {
    if (e instanceof ApiError) return e
    throw e
  }
  throw new Error('se esperaba un error')
}

const list = async (api: Api) => (await unwrap(api.GET('/platform/remote-destinations'))).data

function create(api: Api, body: Record<string, unknown> = {}) {
  return unwrap(
    api.POST('/platform/remote-destinations', {
      body: {
        kind: 'sftp',
        name: 'NAS',
        host: 'nas.fibranorte.example',
        username: 'horus',
        path: '/srv/copias',
        ...body,
      },
    }),
  )
}

const path = (id: string) => ({ params: { path: { destination_id: id } } })
const test = (api: Api, id: string, confirm?: string) =>
  unwrap(
    api.POST('/platform/remote-destinations/{destination_id}/test', {
      ...path(id),
      body: confirm ? { confirm_host_key: confirm } : {},
    }),
  )
const patch = (api: Api, id: string, version: number, body: Record<string, unknown>) =>
  unwrap(
    api.PATCH('/platform/remote-destinations/{destination_id}', {
      params: { path: { destination_id: id }, header: { 'If-Match': `"${version}"` } },
      body,
    }),
  )
const sync = (api: Api, id: string, key = crypto.randomUUID()) =>
  unwrap(
    api.POST('/platform/remote-destinations/{destination_id}/sync', {
      params: { path: { destination_id: id }, header: { 'Idempotency-Key': key } },
    }),
  )

/** Destino listo: credenciales, huella confirmada y activado. */
async function ready(api: Api) {
  const d = await create(api)
  await unwrap(
    api.PUT('/platform/remote-destinations/{destination_id}/credentials', {
      ...path(d.id),
      body: { private_key: KEY },
    }),
  )
  const first = await test(api, d.id)
  const ok = await test(api, d.id, first.host_key_fingerprint!)
  return patch(api, d.id, ok.destination!.version, { enabled: true })
}

describe('API simulada: destinos remotos SFTP', () => {
  it('cero destinos por defecto; system/status lo marca sin configurar', async () => {
    const { api } = await client()
    expect(await list(api)).toEqual([])
    const status = await unwrap(api.GET('/system/status'))
    expect(status.components.find((c) => c.name === 'remote_storage')?.status).toBe(
      'not_configured',
    )
  })

  it('alta: deshabilitado, sin credenciales ni huella, puerto 22 y 30 días', async () => {
    const { api } = await client()
    const d = await create(api)
    expect(d).toMatchObject({
      kind: 'sftp',
      status: 'disabled',
      enabled: false,
      has_credentials: false,
      host_key_fingerprint: null,
      port: 22,
      retention_days: 30,
      version: 1,
    })
    const dup = await fails(create(api, { name: 'nas' }))
    expect(dup.status).toBe(409)
    const bad = await fails(create(api, { host: 'sftp://x', port: 0 }))
    expect(bad.status).toBe(422)
    expect(bad.problem.errors?.map((e) => e.field).sort()).toEqual(['host', 'port'])
  })

  it('credenciales: una de dos, clave OpenSSH; nunca se devuelven', async () => {
    const { api } = await client()
    const d = await create(api)
    const put = (body: Record<string, unknown>) =>
      unwrap(
        api.PUT('/platform/remote-destinations/{destination_id}/credentials', {
          ...path(d.id),
          body,
        }),
      )
    expect((await fails(put({ password: 'x', private_key: KEY }))).status).toBe(422)
    expect((await fails(put({ private_key: 'no es una clave' }))).status).toBe(422)
    const saved = await put({ private_key: KEY, passphrase: 'pp' })
    expect(saved.has_credentials).toBe(true)
    expect(JSON.stringify(saved)).not.toContain('PRIVATE KEY')
  })

  it('prueba: TOFU de la huella, luego auth y escritura; activar exige huella', async () => {
    const { api } = await client()
    const d = await create(api)
    const early = await fails(patch(api, d.id, 1, { enabled: true }))
    expect(early.code).toBe('REMOTE_DESTINATION_NOT_READY')

    const first = await test(api, d.id)
    expect(first).toMatchObject({ ok: false, stage: 'host_key', host_key_confirmed: false })
    expect(first.host_key_fingerprint).toMatch(/^SHA256:[A-Za-z0-9+/]{43}$/)
    const wrong = await test(api, d.id, 'SHA256:otra')
    expect(wrong.stage).toBe('host_key')

    // Huella confirmada pero sin credenciales: para en auth.
    const noCreds = await test(api, d.id, first.host_key_fingerprint!)
    expect(noCreds).toMatchObject({ ok: false, stage: 'auth', host_key_confirmed: true })
    expect(noCreds.destination?.host_key_fingerprint).toBe(first.host_key_fingerprint)
  })

  it('fallos simulados por paso (horus.mock.sftpTestFail)', async () => {
    const { api } = await client()
    const d = await ready(api)
    local.setItem('horus.mock.sftpTestFail', 'write')
    expect(await test(api, d.id)).toMatchObject({ ok: false, stage: 'write' })
    local.setItem('horus.mock.sftpTestFail', 'connect')
    expect(await test(api, d.id)).toMatchObject({ ok: false, stage: 'connect' })
  })

  it('la clave del servidor cambió: mismatch hasta confirmar la nueva', async () => {
    const { api } = await client()
    const d = await ready(api)
    const pinned = d.host_key_fingerprint
    local.setItem('horus.mock.sftpHostKeyChanged', '1')
    const r = await test(api, d.id)
    expect(r).toMatchObject({ ok: false, stage: 'host_key', host_key_confirmed: false })
    expect(r.host_key_fingerprint).not.toBe(pinned)
    expect(r.destination?.host_key_fingerprint).toBe(pinned)
    expect(r.error).toContain('no coincide')
    const accepted = await test(api, d.id, r.host_key_fingerprint!)
    expect(accepted.ok).toBe(true)
    expect(accepted.destination?.host_key_fingerprint).toBe(r.host_key_fingerprint)
  })

  it('copia: pending → en curso (SYNC_RUNNING) → ok; idempotente por clave', async () => {
    const { api } = await client()
    const d = await ready(api)
    expect(d.status).toBe('pending')
    const key = crypto.randomUUID()
    const queued = await sync(api, d.id, key)
    expect(queued.status).toBe('pending')
    expect((await sync(api, d.id, key)).id).toBe(d.id)
    const running = await fails(sync(api, d.id))
    expect(running.code).toBe('REMOTE_DESTINATION_SYNC_RUNNING')
    clock += 3500
    const [after] = await list(api)
    expect(after).toMatchObject({ status: 'ok', files_pending: 0, bytes_pending: 0 })
    expect(after!.last_success_at).not.toBeNull()
    const status = await unwrap(api.GET('/system/status'))
    expect(status.components.find((c) => c.name === 'remote_storage')?.status).toBe('up')
    // Una hora y pico después: con retraso.
    clock += 2 * 3600_000
    expect((await list(api))[0]!.status).toBe('lagging')
  })

  it('copiar sin estar listo: NOT_READY; sin Idempotency-Key: 428', async () => {
    const { api } = await client()
    const d = await create(api)
    expect((await fails(sync(api, d.id))).code).toBe('REMOTE_DESTINATION_NOT_READY')
    const missing = await fails(
      unwrap(
        api.POST('/platform/remote-destinations/{destination_id}/sync', {
          params: { path: { destination_id: d.id }, header: { 'Idempotency-Key': '' } },
        }),
      ),
    )
    expect(missing.status).toBe(428)
  })

  it('editar: If-Match (428/412) y cambiar host borra la huella y deshabilita', async () => {
    const { api } = await client()
    const d = await ready(api)
    const noMatch = await fails(
      unwrap(
        api.PATCH('/platform/remote-destinations/{destination_id}', {
          params: { path: { destination_id: d.id }, header: { 'If-Match': '' } },
          body: { name: 'x' },
        }),
      ),
    )
    expect(noMatch.status).toBe(428)
    const stale = await fails(patch(api, d.id, d.version - 1, { name: 'x' }))
    expect(stale.status).toBe(412)
    expect(stale.problem.current).toMatchObject({ version: d.version })

    const renamed = await patch(api, d.id, d.version, { retention_days: 60 })
    expect(renamed).toMatchObject({ retention_days: 60, enabled: true })
    const moved = await patch(api, d.id, renamed.version, { host: 'nas2.fibranorte.example' })
    expect(moved).toMatchObject({ enabled: false, host_key_fingerprint: null, status: 'disabled' })
  })

  it('412 simulado (horus.mock.sftpConflict): otra persona cambió la retención', async () => {
    const { api } = await client()
    const d = await create(api)
    local.setItem('horus.mock.sftpConflict', '1')
    const e = await fails(patch(api, d.id, d.version, { name: 'Otro' }))
    expect(e.status).toBe(412)
    expect(e.problem.current).toMatchObject({ retention_days: 14, version: 2 })
    expect((await patch(api, d.id, 2, { name: 'Otro' })).name).toBe('Otro')
  })

  it('borrar con If-Match; persistido en el storage de la sesión simulada', async () => {
    const storage = memoryStorage()
    const { api } = await client(storage)
    const d = await create(api)
    resetMockState()
    const again = await client(storage)
    expect((await list(again.api)).map((x) => x.id)).toEqual([d.id])
    const del = await again.api.DELETE('/platform/remote-destinations/{destination_id}', {
      params: { path: { destination_id: d.id }, header: { 'If-Match': `"${d.version}"` } },
    })
    expect(del.response.status).toBe(204)
    expect(await list(again.api)).toEqual([])
    const gone = await fails(test(again.api, d.id))
    expect(gone.code).toBe('REMOTE_DESTINATION_NOT_FOUND')
  })

  it('las escrituras x-reauth piden confirmar la identidad (la copia no)', async () => {
    local.setItem('horus.mock.reauthRequired', '1')
    const { api } = await client()
    const e = await fails(create(api))
    expect(e.code).toBe('REAUTH_REQUIRED')
  })

  it('semilla de demo (horus.mock.sftpSeed): un destino de cada estado', async () => {
    local.setItem('horus.mock.sftpSeed', '1')
    const { api } = await client()
    expect((await list(api)).map((d) => d.status)).toEqual(['ok', 'lagging', 'failing', 'disabled'])
  })
})
