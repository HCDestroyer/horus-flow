// Capturas optimizadas por scripts/optimize-images.mjs (public/img/shots). Datos simulados.
export interface Shot {
  name: 'mural' | 'infected' | 'finding' | 'router' | 'kiosk' | 'mobile'
  width: number
  height: number
  widths: number[]
}

export const shots: Record<Shot['name'], Shot> = {
  mural: { name: 'mural', width: 1920, height: 1080, widths: [640, 960, 1440, 1920] },
  infected: { name: 'infected', width: 1440, height: 900, widths: [640, 960, 1440] },
  finding: { name: 'finding', width: 1440, height: 900, widths: [640, 960, 1440] },
  router: { name: 'router', width: 1440, height: 900, widths: [640, 960, 1440] },
  kiosk: { name: 'kiosk', width: 1440, height: 900, widths: [640, 960, 1440] },
  mobile: { name: 'mobile', width: 390, height: 844, widths: [390] },
}

export function srcset(shot: Shot, format: 'avif' | 'webp'): string {
  return shot.widths.map((w) => `/img/shots/${shot.name}-${w}.${format} ${w}w`).join(', ')
}
