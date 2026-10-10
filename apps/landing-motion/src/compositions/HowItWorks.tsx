// HowItWorks — versión animada del diagrama "Cómo funciona" (FlowDiagram.vue):
//
//   router MikroTik → túnel WireGuard (cifrado, candado) → Horus (collector → detección)
//   → NOC/kiosco y alertas (email, Telegram, LibreNMS).
//
// Las cuatro columnas coinciden con las cuatro etapas de la lista HTML que va debajo (router,
// túnel, Horus, NOC), que lleva los textos ES/EN. Cada etapa se ilumina en orden mientras un
// paquete la recorre; por debajo circula un flujo tenue y continuo. Loop de 12 s.
import { useCurrentFrame, useVideoConfig } from 'remotion'
import {
  ActivityIcon,
  HorusEye,
  LockIcon,
  MailIcon,
  RouterIcon,
  ScanIcon,
  SendIcon,
  ServerIcon,
} from '../lib/icons'
import { along, envelope, mix, phase, polyPath, ramp, springAt, type Pt } from '../lib/motion'
import { Glow, Stage } from '../lib/Neon'
import { NEON } from '../lib/theme'

export const HOW_FPS = 30
export const HOW_FRAMES = 360 // 12 s
const W = 1920
const H = 640

const Y = 300
const COL = [240, 720, 1200, 1680] as const
const ROUTER: Pt = [COL[0], Y]
const TUN_A = COL[0] + 110
const TUN_B = COL[2] - 210
const PANEL = { x: COL[2] - 190, y: Y - 150, w: 380, h: 300 }
const COLLECTOR: Pt = [COL[2] - 95, Y + 20]
const DETECT: Pt = [COL[2] + 95, Y + 20]
const MONITOR: Pt = [COL[3], Y - 50]
const ALERTS: Pt[] = [
  [COL[3] - 110, Y + 150],
  [COL[3], Y + 150],
  [COL[3] + 110, Y + 150],
]

// Camino principal y ramas de salida.
const MAIN: Pt[] = [
  [ROUTER[0] + 70, Y],
  [TUN_A, Y],
  [TUN_B, Y],
  [PANEL.x, Y],
  [COLLECTOR[0] - 48, Y + 20],
]
const INNER: Pt[] = [
  [COLLECTOR[0] + 48, Y + 20],
  [DETECT[0] - 48, Y + 20],
]
const TO_NOC: Pt[] = [
  [DETECT[0] + 48, Y + 20],
  [PANEL.x + PANEL.w, Y + 20],
  [COL[3] - 140, Y + 20],
  [COL[3] - 140, MONITOR[1]],
  [MONITOR[0] - 96, MONITOR[1]],
]
const TO_ALERT = (k: number): Pt[] => [
  [COL[3] - 140, Y + 20],
  [COL[3] - 140, Y + 150],
  [ALERTS[k]![0] - 34, Y + 150],
]

// Guion: cada etapa tiene su ventana de luz (frames).
const S = {
  router: [8, 22, 56, 92],
  tunnel: [50, 66, 120, 150],
  collector: [118, 132, 168, 200],
  detect: [160, 176, 226, 262],
  noc: [222, 236, 304, 336],
  alerts: [238, 250, 306, 338],
} as const
const HERO = { main: [14, 120], inner: [134, 166], noc: [214, 238], alert: [220, 248] } as const

function lit(frame: number, w: readonly [number, number, number, number]) {
  return envelope(frame, w[0], w[1], w[2], w[3])
}

function Dot({ p, r, color, core }: { p: Pt; r: number; color: string; core: string }) {
  return (
    <>
      <circle cx={p[0]} cy={p[1]} r={r * 1.9} fill={color} opacity={0.35} />
      <circle cx={p[0]} cy={p[1]} r={r} fill={core} />
    </>
  )
}

export function HowItWorks() {
  const frame = useCurrentFrame()
  const { fps } = useVideoConfig()

  const L = {
    router: lit(frame, S.router),
    tunnel: lit(frame, S.tunnel),
    collector: lit(frame, S.collector),
    detect: lit(frame, S.detect),
    noc: lit(frame, S.noc),
    alerts: lit(frame, S.alerts),
  }
  const stroke = (k: number) => mix(NEON.borderStrong, NEON.cyan, k)
  const icon = (k: number) => mix(NEON.lapisDeep, NEON.cyanCore, k)
  const base = 0.35

  // Flujo tenue continuo: un paquete cada 30 frames por el camino principal (30 | 360).
  const ambient = Array.from({ length: 4 }, (_, k) => phase(frame - k * 30, 120))
  const tunnelDash = -phase(frame, 30) * 26

  // Hallazgo: punto rojo en la detección, en el kiosco y en la alerta.
  const found = springAt(frame, fps, 196, { damping: 15 }) * (1 - ramp(frame, 318, 340))
  const alertOn = (k: number) =>
    springAt(frame, fps, 250 + k * 16, { damping: 16 }) * (1 - ramp(frame, 312 + k * 4, 338))

  // Barrido de la detección: periódico, más visible mientras la etapa está encendida.
  const sweep = phase(frame, 60)

  return (
    <Stage width={W} height={H}>
      {/* Conexiones de fondo */}
      <g fill="none" strokeWidth={2} strokeLinecap="round">
        <path d={polyPath(MAIN)} stroke={NEON.border} />
        <path d={polyPath(INNER)} stroke={stroke(L.collector * 0.6)} />
        <path d={polyPath(TO_NOC)} stroke={stroke(L.noc * 0.6)} />
        {ALERTS.map((_, k) => (
          <path key={k} d={polyPath(TO_ALERT(k))} stroke={stroke(L.alerts * 0.6)} />
        ))}
      </g>

      {/* 1 · Router */}
      <Glow blur={12} halo={base + 0.65 * L.router} wide>
        <rect x={ROUTER[0] - 70} y={Y - 70} width={140} height={140} rx={32} fill={NEON.bgPanel} stroke={stroke(base + 0.65 * L.router)} strokeWidth={3} />
        <RouterIcon x={ROUTER[0]} y={Y} size={72} color={icon(L.router)} />
      </Glow>

      {/* 2 · Túnel WireGuard */}
      <Glow blur={6} halo={base + 0.65 * L.tunnel}>
        <rect x={TUN_A} y={Y - 40} width={TUN_B - TUN_A} height={80} rx={40} fill="none" stroke={stroke(base + 0.65 * L.tunnel)} strokeWidth={3} />
        <line x1={TUN_A + 40} y1={Y} x2={TUN_B - 40} y2={Y} stroke={NEON.cyan} strokeWidth={2.5} strokeDasharray="5 21" strokeDashoffset={tunnelDash} strokeLinecap="round" opacity={0.35 + 0.6 * L.tunnel} />
      </Glow>
      <rect x={COL[1] - 40} y={Y - 40} width={80} height={80} rx={20} fill={NEON.bg} />
      <Glow blur={6} halo={0.5 + 0.5 * L.tunnel}>
        <LockIcon x={COL[1]} y={Y} size={52 * (1 + 0.08 * L.tunnel)} color={icon(0.3 + 0.7 * L.tunnel)} />
      </Glow>

      {/* 3 · Horus: collector → detección */}
      <Glow blur={6} halo={base + 0.4 * Math.max(L.collector, L.detect)}>
        <rect x={PANEL.x} y={PANEL.y} width={PANEL.w} height={PANEL.h} rx={28} fill={NEON.bgPanel} stroke={stroke(base + 0.5 * Math.max(L.collector, L.detect))} strokeWidth={3} />
      </Glow>
      <HorusEye x={COL[2]} y={PANEL.y + 52} size={44} color={mix(NEON.dotStrong, NEON.lapis, 0.5 + 0.5 * Math.max(L.collector, L.detect))} />
      <Glow blur={6} halo={base + 0.65 * L.collector}>
        <rect x={COLLECTOR[0] - 48} y={COLLECTOR[1] - 48} width={96} height={96} rx={22} fill={NEON.bgRaised} stroke={stroke(base + 0.65 * L.collector)} strokeWidth={2.5} />
        <ServerIcon x={COLLECTOR[0]} y={COLLECTOR[1]} size={48} color={icon(L.collector)} />
      </Glow>
      <Glow blur={6} halo={base + 0.65 * L.detect}>
        <rect x={DETECT[0] - 48} y={DETECT[1] - 48} width={96} height={96} rx={22} fill={NEON.bgRaised} stroke={stroke(base + 0.65 * L.detect)} strokeWidth={2.5} />
        <ScanIcon x={DETECT[0]} y={DETECT[1]} size={48} color={icon(L.detect)} />
      </Glow>
      {L.detect > 0.01 ? (
        <line x1={DETECT[0] - 40} x2={DETECT[0] + 40} y1={DETECT[1] - 40 + 80 * sweep} y2={DETECT[1] - 40 + 80 * sweep} stroke={NEON.cyan} strokeWidth={2} opacity={0.6 * L.detect} />
      ) : null}
      {found > 0.01 ? (
        <Glow blur={6} halo={1}>
          <circle cx={DETECT[0] + 40} cy={DETECT[1] - 40} r={9 * found} fill={NEON.infectedCore} stroke={NEON.infected} strokeWidth={2} />
        </Glow>
      ) : null}

      {/* 4 · NOC / kiosco y alertas */}
      <Glow blur={12} halo={base + 0.65 * L.noc} wide>
        <rect x={MONITOR[0] - 96} y={MONITOR[1] - 70} width={192} height={130} rx={18} fill={NEON.bgPanel} stroke={stroke(base + 0.65 * L.noc)} strokeWidth={3} />
        <path d={`M${MONITOR[0] - 30} ${MONITOR[1] + 96} H${MONITOR[0] + 30} M${MONITOR[0]} ${MONITOR[1] + 60} V${MONITOR[1] + 96}`} stroke={stroke(base + 0.65 * L.noc)} strokeWidth={3} strokeLinecap="round" fill="none" />
      </Glow>
      {/* Mini cuadrícula del kiosco: 10 × 5 clientes, uno señalado */}
      {Array.from({ length: 50 }, (_, i) => {
        const cx = MONITOR[0] - 72 + (i % 10) * 16
        const cy = MONITOR[1] - 44 + Math.floor(i / 10) * 18
        const isFound = i === 17
        return (
          <circle
            key={i}
            cx={cx}
            cy={cy}
            r={isFound ? 3.4 + 2.4 * found : 3.4}
            fill={isFound ? mix(NEON.dotStrong, NEON.infectedCore, found) : mix(NEON.dot, NEON.dotStrong, 0.4 + 0.6 * L.noc)}
          />
        )
      })}
      {found > 0.01 ? (
        <circle cx={MONITOR[0] - 72 + 7 * 16} cy={MONITOR[1] - 44 + 18} r={11} fill={NEON.infected} opacity={0.45 * found * L.noc} filter="url(#blur6)" />
      ) : null}
      {[MailIcon, SendIcon, ActivityIcon].map((Icon, k) => {
        const a = alertOn(k)
        const [x, y] = ALERTS[k]!
        return (
          <g key={k}>
            <Glow blur={6} halo={base + 0.65 * a}>
              <circle cx={x} cy={y} r={34} fill={NEON.bgPanel} stroke={stroke(base + 0.65 * a)} strokeWidth={2.5} />
              <Icon x={x} y={y} size={34} color={icon(a)} />
            </Glow>
            {a > 0.01 ? <circle cx={x + 24} cy={y - 24} r={7 * a} fill={NEON.infectedCore} /> : null}
          </g>
        )
      })}

      {/* Flujo tenue */}
      {ambient.map((t, k) => (
        <g key={k} opacity={0.55 * (1 - ramp(t, 0.9, 1)) * ramp(t, 0, 0.05)}>
          <Dot p={along(MAIN, t)} r={4} color={NEON.cyan} core={NEON.cyanCore} />
        </g>
      ))}

      {/* Paquete protagonista que enciende cada etapa */}
      <Glow blur={6} halo={1}>
        {frame >= HERO.main[0] && frame <= HERO.main[1] ? (
          <Dot p={along(MAIN, ramp(frame, HERO.main[0], HERO.main[1], (x) => x))} r={7} color={NEON.cyan} core={NEON.cyanCore} />
        ) : null}
        {frame >= HERO.inner[0] && frame <= HERO.inner[1] ? (
          <Dot p={along(INNER, ramp(frame, HERO.inner[0], HERO.inner[1]))} r={7} color={NEON.cyan} core={NEON.cyanCore} />
        ) : null}
        {frame >= HERO.noc[0] && frame <= HERO.noc[1] ? (
          <Dot p={along(TO_NOC, ramp(frame, HERO.noc[0], HERO.noc[1]))} r={6} color={NEON.cyan} core={NEON.cyanCore} />
        ) : null}
        {ALERTS.map((_, k) => {
          const a = HERO.alert[0] + k * 12
          const b = HERO.alert[1] + k * 12
          if (frame < a || frame > b) return null
          return <Dot key={k} p={along(TO_ALERT(k), ramp(frame, a, b))} r={5} color={NEON.cyan} core={NEON.cyanCore} />
        })}
      </Glow>
    </Stage>
  )
}
