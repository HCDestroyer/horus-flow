/**
 * Nombre en español del área de un permiso (`roles.areas.<prefijo>`); un área nueva del
 * catálogo que aún no tenga traducción se muestra con su prefijo.
 */
export function usePermissionAreaLabel() {
  const { t, te } = useI18n()
  return (area: string) => (te(`roles.areas.${area}`) ? t(`roles.areas.${area}`) : area)
}
