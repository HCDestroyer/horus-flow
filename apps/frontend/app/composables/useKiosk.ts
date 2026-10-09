import type { Dashboard, KioskConfig } from '~~/types/api'

/**
 * Pantalla NOC (kiosco, I1-21, frontend.md §7; api.md §2.12). La TV es un **dispositivo**,
 * no un usuario: canjea un código de un solo uso por una credencial de dispositivo (cookie
 * HttpOnly que pone el gateway) y con ella pide un JWT de kiosco de 10 min sin escritura,
 * que vive solo en memoria (el mismo hueco que el token de ISP de la pestaña).
 *
 * - `GET /kiosk/config` cada 30 s: playlist, datos personales, versión mínima del frontend.
 *   Un `401` (credencial revocada o caducada, cierre WS `4409`) devuelve a la pantalla de
 *   código en < 1 min.
 * - Corte de red: se mantienen los últimos datos; a los 60 s, banda "Sin conexión desde
 *   HH:MM"; reintento con backoff 1 → 30 s y vuelta sola al recuperar.
 */
export type KioskPhase = 'starting' | 'enroll' | 'playing'

const POLL_MS = 30_000
const OFFLINE_BAND_MS = 60_000

export function useKiosk() {
  const { $api } = useNuxtApp()
  const { scoped } = useAuthTokens()

  const phase = ref<KioskPhase>('starting')
  const config = shallowRef<KioskConfig | null>(null)
  const dashboards = shallowRef(new Map<string, Dashboard>())
  /** Primer fallo de red de la racha actual (ms) o `null` si hay conexión. */
  const failingSince = ref<number | null>(null)
  const now = ref(Date.now())
  let timer: ReturnType<typeof setTimeout> | undefined
  let clock: ReturnType<typeof setInterval> | undefined
  let backoff = 1000
  let tokenExpires = 0

  const offlineSince = computed(() =>
    failingSince.value !== null && now.value - failingSince.value >= OFFLINE_BAND_MS
      ? failingSince.value
      : null,
  )

  /**
   * JWT de kiosco con la credencial de dispositivo. `revoked` solo si el servidor la rechaza
   * (401/403): cualquier otro fallo es transitorio y no devuelve la TV a la pantalla de código.
   */
  async function issueToken(): Promise<'ok' | 'revoked' | 'error'> {
    const res = await $api.POST('/kiosk/token', {
      params: { header: { 'X-Requested-With': 'horus' } },
    })
    if (res.response.status === 401 || res.response.status === 403) return 'revoked'
    if (!res.response.ok || !res.data) return 'error'
    tokenExpires = new Date(res.data.expires_at).getTime()
    scoped.value = {
      scope: { kind: 'tenant', tenantId: res.data.tenant_id ?? '' },
      token: res.data.access_token,
      expiresAt: res.data.expires_at,
    }
    return 'ok'
  }

  function toEnroll() {
    scoped.value = null
    config.value = null
    dashboards.value = new Map()
    phase.value = 'enroll'
  }

  async function loadDashboards(cfg: KioskConfig) {
    const next = new Map(dashboards.value)
    for (const item of cfg.items) {
      if (next.has(item.dashboard_id)) continue
      const d = await unwrap(
        $api.GET('/dashboards/{dashboard_id}', {
          params: { path: { dashboard_id: item.dashboard_id } },
        }),
      )
      next.set(d.id, d)
    }
    dashboards.value = next
  }

  /** Un ciclo: token vigente, configuración y dashboards. Devuelve si el servidor respondió. */
  async function sync(): Promise<boolean> {
    try {
      if (!scoped.value || tokenExpires - Date.now() < 2 * 60_000) {
        const issued = await issueToken()
        if (issued === 'revoked') {
          toEnroll()
          return true
        }
        if (issued === 'error') return false
      }
      let res = await $api.GET('/kiosk/config')
      if (res.response.status === 401) {
        // Token caducado o credencial revocada: lo decide /kiosk/token.
        const issued = await issueToken()
        if (issued === 'revoked') {
          toEnroll()
          return true
        }
        if (issued === 'error') return false
        res = await $api.GET('/kiosk/config')
      }
      if (!res.response.ok || !res.data) return res.response.status < 500
      config.value = res.data
      await loadDashboards(res.data)
      phase.value = 'playing'
      failingSince.value = null
      backoff = 1000
      return true
    } catch (e) {
      if (e instanceof NetworkError || e instanceof TypeError) {
        failingSince.value ??= Date.now()
        return false
      }
      throw e
    }
  }

  async function loop() {
    const ok = await sync().catch(() => false)
    clearTimeout(timer)
    if (phase.value === 'enroll') return
    // Sin conexión: backoff 1 → 30 s con jitter; con conexión, cada 30 s.
    const wait = ok ? POLL_MS : Math.min(30_000, backoff) * (0.8 + Math.random() * 0.4)
    backoff = ok ? 1000 : Math.min(30_000, backoff * 2)
    timer = setTimeout(loop, wait)
  }

  async function enroll(code: string): Promise<'ok' | 'invalid' | 'rate_limited' | 'network'> {
    try {
      const res = await $api.POST('/kiosk/enroll', {
        params: { header: { 'X-Requested-With': 'horus' } },
        body: { code: code.replace(/[\s-]/g, '').toUpperCase() },
      })
      if (res.response.status === 429) return 'rate_limited'
      if (!res.response.ok) return 'invalid'
      phase.value = 'starting'
      await loop()
      return 'ok'
    } catch {
      return 'network'
    }
  }

  /** ¿Responde el servidor ahora? (antes de recargar nunca se deja la pantalla en blanco). */
  async function serverResponds() {
    try {
      const res = await $api.GET('/kiosk/config')
      return res.response.ok
    } catch {
      return false
    }
  }

  onMounted(() => {
    clock = setInterval(() => (now.value = Date.now()), 1000)
    loop()
  })
  onBeforeUnmount(() => {
    clearTimeout(timer)
    clearInterval(clock)
  })

  return { phase, config, dashboards, offlineSince, failingSince, now, enroll, serverResponds }
}
