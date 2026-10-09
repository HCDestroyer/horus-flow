/**
 * Plugin `realtime` (frontend.md §10.5): registro único de suscripciones de la pestaña.
 * Al cambiar de ISP (`horus:tenant-changed`) se cierran todas las del ISP anterior.
 */
declare module '#app' {
  interface RuntimeNuxtHooks {
    'horus:tenant-changed': (change: { from?: string; to?: string }) => void | Promise<void>
  }
}

export default defineNuxtPlugin({
  name: 'realtime',
  dependsOn: ['api'],
  async setup(nuxtApp) {
    const config = useRuntimeConfig()
    const source: RealtimeSource = config.public.apiMock
      ? (await import('~~/mocks/realtime')).createMockRealtime()
      : noopRealtime
    const realtime = createRealtimeRegistry(source)

    nuxtApp.hook('horus:tenant-changed', () => realtime.closeTenant())

    return { provide: { realtime } }
  },
})
