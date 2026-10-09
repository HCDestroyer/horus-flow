import type { WatchSource } from 'vue'

/**
 * Consultas de una página del ISP actual (frontend.md §16): `useAsyncData` con la clave
 * indexada por el ámbito del token (ISP o plataforma), para que al cambiar de ISP nunca se
 * muestren datos del anterior (`resetTenantData` las vacía). Solo cliente (SPA).
 */
export function useTenantQuery<T>(
  key: MaybeRefOrGetter<string>,
  fetcher: () => Promise<T>,
  options: { watch?: WatchSource[]; immediate?: boolean } = {},
) {
  const { activeScope } = useAuth()
  const fullKey = computed(() => {
    const scope = activeScope.value
    const owner = !scope ? 'none' : scope.kind === 'tenant' ? scope.tenantId : 'platform'
    return `q:${owner}:${toValue(key)}`
  })
  return useAsyncData(fullKey, fetcher, {
    server: false,
    watch: options.watch,
    immediate: options.immediate ?? true,
  })
}

/** ¿Tiene el usuario este permiso en el ISP actual? (ocultar por permiso, §12). */
export function useCan() {
  const { membership } = useTenant()
  return (permission: string) => hasTenantPermission(membership.value, permission)
}

/** ¿Tiene el usuario este permiso de plataforma? */
export function usePlatformCan() {
  const { me } = useAuth()
  return (permission: string) => hasPlatformPermission(me.value, permission)
}

/** `Idempotency-Key` para acciones con efecto externo (scripts, códigos, pruebas). */
export function idempotencyKey() {
  return crypto.randomUUID()
}

/** ETag `"<version>"` para `If-Match`. */
export function ifMatch(version: number) {
  return `"${version}"`
}
