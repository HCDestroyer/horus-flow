/**
 * Aviso para la pantalla de login tras aceptar una invitación (correo para rellenar y ISP).
 * Solo en memoria de la pestaña: el correo no pasa por la URL ni por el historial. El login lo
 * lee una vez y lo borra.
 */
export interface LoginNotice {
  kind: 'invitation-new' | 'invitation-existing'
  email: string
  tenantName: string
}

export function useLoginNotice() {
  return useState<LoginNotice | null>('login:notice', () => null)
}
