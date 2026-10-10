// Iconos de trazo (24 × 24, como Lucide, que usa la landing) dibujados como SVG para poder
// pintarlos con glow. Todos aceptan centro, tamaño y color.
import type { ReactNode } from 'react'

type IconProps ={ x: number; y: number; size: number; color: string; stroke?: number }

function Frame({ x, y, size, color, stroke = 2, children }: IconProps & { children: ReactNode }) {
  const s = size / 24
  return (
    <g
      transform={`translate(${x - size / 2} ${y - size / 2}) scale(${s})`}
      fill="none"
      stroke={color}
      strokeWidth={stroke}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      {children}
    </g>
  )
}

/** Ojo de Horus (la marca de la landing, HorusMark.vue, en 24 × 24). */
export function HorusEye(p: IconProps) {
  return (
    <Frame {...p}>
      <path d="M2.25 11.6C4.9 7.5 8.25 5.6 12 5.6s7.1 1.9 9.75 6c-2.65 4.1-6 6-9.75 6s-7.1-1.9-9.75-6Z" />
      <circle cx="12" cy="11.6" r="2.7" fill={p.color} />
      <path d="M9.75 17.4c-.45 1.95-1.65 3.3-3.45 3.98" />
    </Frame>
  )
}

export function RouterIcon(p: IconProps) {
  return (
    <Frame {...p}>
      <rect x="2" y="14" width="20" height="8" rx="2" />
      <path d="M6.01 18H6M10.01 18H10M15 10v4M17.84 7.17a4 4 0 0 0-5.66 0M20.66 4.34a8 8 0 0 0-11.31 0" />
    </Frame>
  )
}

export function LockIcon(p: IconProps) {
  return (
    <Frame {...p}>
      <rect x="4" y="11" width="16" height="10" rx="2" />
      <path d="M8 11V7a4 4 0 0 1 8 0v4" />
      <circle cx="12" cy="16" r="1" />
    </Frame>
  )
}

export function ServerIcon(p: IconProps) {
  return (
    <Frame {...p}>
      <rect x="3" y="3" width="18" height="8" rx="2" />
      <rect x="3" y="13" width="18" height="8" rx="2" />
      <path d="M7 7h.01M7 17h.01" />
    </Frame>
  )
}

export function MonitorIcon(p: IconProps) {
  return (
    <Frame {...p}>
      <rect x="2" y="3" width="20" height="14" rx="2" />
      <path d="M8 21h8M12 17v4" />
    </Frame>
  )
}

export function MailIcon(p: IconProps) {
  return (
    <Frame {...p}>
      <rect x="2" y="4" width="20" height="16" rx="2" />
      <path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7" />
    </Frame>
  )
}

/** Avión de papel (Telegram), sin el logotipo de la marca. */
export function SendIcon(p: IconProps) {
  return (
    <Frame {...p}>
      <path d="M14.54 21.69a.5.5 0 0 0 .94-.03l6.5-19a.5.5 0 0 0-.64-.63l-19 6.5a.5.5 0 0 0-.02.93l7.93 3.18a2 2 0 0 1 1.11 1.11Z" />
      <path d="m21.85 2.15-10.94 10.94" />
    </Frame>
  )
}

/** Gráfico de actividad (LibreNMS u otro NMS), genérico. */
export function ActivityIcon(p: IconProps) {
  return (
    <Frame {...p}>
      <path d="M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2" />
    </Frame>
  )
}

export function DatabaseIcon(p: IconProps) {
  return (
    <Frame {...p}>
      <ellipse cx="12" cy="5" rx="9" ry="3" />
      <path d="M3 5v14a9 3 0 0 0 18 0V5" />
      <path d="M3 12a9 3 0 0 0 18 0" />
    </Frame>
  )
}

export function ScanIcon(p: IconProps) {
  return (
    <Frame {...p}>
      <path d="M3 7V5a2 2 0 0 1 2-2h2M17 3h2a2 2 0 0 1 2 2v2M21 17v2a2 2 0 0 1-2 2h-2M7 21H5a2 2 0 0 1-2-2v-2" />
      <circle cx="12" cy="12" r="3" />
      <path d="m16 16-1.9-1.9" />
    </Frame>
  )
}
