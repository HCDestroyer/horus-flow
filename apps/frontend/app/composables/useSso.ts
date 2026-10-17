import type { SsoTarget } from '~~/types/api'

/**
 * Autologin desde ArgosISP (contrato §1.5): tras abrir la sesión, lleva al recurso pedido con el
 * ACL del usuario. Activa el token del ISP del destino, pide a Horus qué recurso es
 * (`POST /integrations/argosisp/sso/resolve`) y navega a una ruta construida aquí. Si no puede
 * verlo, deja al usuario en el inicio del ISP con el error de acceso de siempre.
 */
export function useSso() {
  const { $api } = useNuxtApp()
  const auth = useAuth()

  /** Devuelve la ruta a abrir y, si el recurso no se pudo resolver, el error. */
  async function targetRoute(target: SsoTarget): Promise<{ path: string; error: unknown }> {
    const home = `/t/${encodeURIComponent(target.tenant_slug)}`
    try {
      await auth.activateScope({ kind: 'tenant', tenantId: target.tenant_id })
      const resolved = await unwrap(
        $api.POST('/integrations/argosisp/sso/resolve', {
          body: { type: target.type, external_id: target.external_id },
        }),
      )
      return { path: ssoTargetPath(target.tenant_slug, resolved), error: null }
    } catch (error) {
      return { path: home, error }
    }
  }

  return { targetRoute }
}
