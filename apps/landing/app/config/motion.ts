// Animaciones neón renderizadas con Remotion (apps/landing-motion) y servidas desde
// public/motion/. Cada clip tiene WebM VP9 + MP4 H.264 a 1920 y 960 px de ancho y póster
// AVIF/WebP en los mismos anchos. Si cambias una composición, vuelve a renderizarla
// (`pnpm render:one <id>` en apps/landing-motion) y ajusta aquí lo que cambie.
export const motionClips = {
  'hero-network': {
    width: 1920,
    height: 1920,
    // Columna del hero: ~28rem en escritorio, ancho completo en móvil.
    sizes: '(min-width: 1024px) 28rem, calc(100vw - 2rem)',
    // El vídeo de 1920 solo cuando la columna en píxeles físicos pasa de ~1000.
    largeMedia: '(min-width: 1024px) and (min-resolution: 2.5dppx)',
  },
  'how-it-works': {
    width: 1920,
    height: 640,
    sizes: '(min-width: 1152px) 1104px, calc(100vw - 3rem)',
    largeMedia: '(min-width: 1200px), (min-width: 768px) and (min-resolution: 1.5dppx)',
  },
  resilience: {
    width: 1920,
    height: 640,
    sizes: '(min-width: 1152px) 1104px, calc(100vw - 2rem)',
    largeMedia: '(min-width: 1200px), (min-width: 768px) and (min-resolution: 1.5dppx)',
  },
} as const

export type MotionClip = keyof typeof motionClips

export const MOTION_WIDTHS = [960, 1920] as const

export function motionFile(clip: MotionClip, kind: 'poster' | 'video', w: number, ext: string) {
  return kind === 'poster' ? `/motion/${clip}-poster-${w}.${ext}` : `/motion/${clip}-${w}.${ext}`
}

export function posterSrcset(clip: MotionClip, ext: 'avif' | 'webp') {
  return MOTION_WIDTHS.map((w) => `${motionFile(clip, 'poster', w, ext)} ${w}w`).join(', ')
}
