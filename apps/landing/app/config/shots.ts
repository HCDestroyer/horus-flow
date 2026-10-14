// Capturas optimizadas por scripts/optimize-images.mjs (public/img/shots). Datos simulados.
export type ShotName =
  | 'mural'
  | 'infected'
  | 'finding'
  | 'router'
  | 'kiosk'
  | 'mobile'
  | 'security'
  | 'noc720'
  | 'kioskNoc'
  | 'kioskSec'
  | 'c2'
  | 'customer'
  | 'traffic'
  | 'ai'

export interface Shot {
  name: ShotName
  width: number
  height: number
  widths: number[]
}

const w1440 = [640, 960, 1440]
const w1280 = [640, 960, 1280]

export const shots: Record<ShotName, Shot> = {
  mural: { name: 'mural', width: 1920, height: 1080, widths: [640, 960, 1440, 1920] },
  infected: { name: 'infected', width: 1440, height: 900, widths: w1440 },
  finding: { name: 'finding', width: 1440, height: 900, widths: w1440 },
  router: { name: 'router', width: 1440, height: 900, widths: w1440 },
  kiosk: { name: 'kiosk', width: 1440, height: 900, widths: w1440 },
  mobile: { name: 'mobile', width: 390, height: 844, widths: [390] },
  // Murales a 1280×720 (la resolución más pequeña de una TV) y kiosco a 1920×1080.
  security: { name: 'security', width: 1280, height: 720, widths: w1280 },
  noc720: { name: 'noc720', width: 1280, height: 720, widths: w1280 },
  kioskNoc: { name: 'kioskNoc', width: 1920, height: 1080, widths: w1440 },
  kioskSec: { name: 'kioskSec', width: 1920, height: 1080, widths: w1440 },
  c2: { name: 'c2', width: 1440, height: 900, widths: w1440 },
  customer: { name: 'customer', width: 1440, height: 900, widths: w1440 },
  traffic: { name: 'traffic', width: 1440, height: 900, widths: w1440 },
  ai: { name: 'ai', width: 1440, height: 900, widths: w1440 },
}

export function srcset(shot: Shot, format: 'avif' | 'webp'): string {
  return shot.widths.map((w) => `/img/shots/${shot.name}-${w}.${format} ${w}w`).join(', ')
}
