// Páginas del panel: exigen una sesión completa (contraseña + TOTP); si no, al login.
export default defineNuxtRouteMiddleware(async (to) => {
  if (import.meta.server) return
  const s = useAdminSession().value?.authenticated
    ? useAdminSession().value!
    : await refreshAdminSession()
  if (to.path === '/admin/login') {
    if (s.authenticated && s.stage === 'full') return navigateTo('/admin/solicitudes')
    return
  }
  if (!s.authenticated || s.stage !== 'full') return navigateTo('/admin/login')
})
