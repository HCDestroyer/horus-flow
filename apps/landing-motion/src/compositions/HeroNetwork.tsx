// HeroNetwork — el elemento distintivo de la landing (SubnetGrid.vue: la red 10.20.1.0/24 como
// 256 puntos, con 10.20.1.47 señalado como Infectado), con vida:
//
//   1. Calma: la cuadrícula es una malla; los clientes activos titilan y emiten paquetes de luz
//      que bajan por la malla al router MikroTik, cruzan el túnel WireGuard y llegan a Horus.
//   2. 10.20.1.47 empieza a contactar un servidor de mando y control (C2): línea roja.
//   3. Su flujo llega a Horus por el mismo camino; Horus lo detecta y señala la IP: el punto se
//      enciende en rojo neón con un pulso y aparece su IP. La razón (texto) va en HTML debajo.
//   4. Vuelta a la calma: el último frame es igual al primero.
//
// Sin texto traducible dentro del vídeo: solo nombres propios e IPs. El texto (Infectado,
// confianza, razón) vive en el HTML de la landing, accesible y traducido.
import { useCurrentFrame, useVideoConfig } from 'remotion'
import { HorusEye, LockIcon, RouterIcon, ServerIcon } from '../lib/icons'
import {
  along,
  breathe,
  envelope,
  hash01,
  mix,
  phase,
  polyPath,
  ramp,
  springAt,
  type Pt,
} from '../lib/motion'
import { Glow, Stage } from '../lib/Neon'
import { FONT_MONO, NEON } from '../lib/theme'

export const HERO_FPS = 30
export const HERO_FRAMES = 300 // 10 s
const W = 1000
const H = 1000

// Cuadrícula: misma lógica que SubnetGrid.vue (16 × 16, .0 y .255 huecos, .47 señalado).
const COLS = 16
const STEP = 40
const GX = 200
const GY = 70
const FLAGGED = 47
const active = (i: number) => ((i * 2654435761) >>> 0) % 7 < 2
const dotXY = (i: number): Pt => [GX + (i % COLS) * STEP, GY + Math.floor(i / COLS) * STEP]
const GRID_RIGHT = GX + 15 * STEP
const GRID_BOTTOM = GY + 15 * STEP

// Camino de los datos: bus bajo la cuadrícula → router → túnel → Horus.
const BUS_Y = GRID_BOTTOM + 44
const ROUTER: Pt = [150, 850]
const TUNNEL_A: Pt = [228, 850]
const TUNNEL_B: Pt = [720, 850]
const HORUS: Pt = [850, 850]
const C2: Pt = [905, 118]

const flaggedXY = dotXY(FLAGGED)

function routeFrom(i: number): Pt[] {
  const [x, y] = dotXY(i)
  return [
    [x, y],
    [x, BUS_Y],
    [ROUTER[0], BUS_Y],
    [ROUTER[0], ROUTER[1] - 46],
    [ROUTER[0], ROUTER[1]],
    TUNNEL_A,
    TUNNEL_B,
    [HORUS[0] - 52, HORUS[1]],
  ]
}

// Paquetes de calma: uno cada 20 frames desde clientes activos (orden determinista).
const ACTIVE = Array.from({ length: 256 }, (_, i) => i).filter(
  (i) => active(i) && i !== 0 && i !== 255 && i !== FLAGGED,
)
const PACKETS = Array.from({ length: 15 }, (_, k) => {
  const dot = ACTIVE[Math.floor(hash01(k * 7 + 3) * ACTIVE.length)]!
  return { dot, start: k * 20, dur: 96, route: routeFrom(dot) }
})

// Guion del incidente (frames).
const T = {
  beaconIn: 62, // aparece la línea a C2
  beacons: [72, 98, 124], // paquetes rojos hacia C2
  flowStart: 96, // flujo de .47 hacia Horus
  flowDur: 66,
  detect: 162, // Horus detecta
  flag: 174, // la IP se enciende
  rings: [174, 206],
  calmA: 242,
  calmB: 284,
}

function Packet({
  route,
  t,
  color,
  core,
  size = 1,
}: {
  route: Pt[]
  t: number
  color: string
  core: string
  size?: number
}) {
  // Estela continua: un trazo que se estrecha detrás del núcleo (la forma la da el trazo nítido).
  const N = 10
  const tail = Array.from({ length: N }, (_, j) => along(route, t - 0.05 * (1 - j / (N - 1))))
  const fadeIn = ramp(t, 0, 0.04)
  const fadeOut = 1 - ramp(t, 0.94, 1)
  const [hx, hy] = along(route, t)
  return (
    <g opacity={fadeIn * fadeOut}>
      {tail.slice(1).map(([x, y], j) => {
        const [px, py] = tail[j]!
        const k = (j + 1) / (N - 1)
        return (
          <line
            key={j}
            x1={px}
            y1={py}
            x2={x}
            y2={y}
            stroke={color}
            strokeWidth={(1 + 4 * k) * size}
            strokeLinecap="round"
            opacity={0.15 + 0.7 * k}
          />
        )
      })}
      <circle cx={hx} cy={hy} r={5.5 * size} fill={core} />
    </g>
  )
}

export function HeroNetwork() {
  const frame = useCurrentFrame()
  const { fps } = useVideoConfig()

  // Estado del incidente.
  const beacon = envelope(frame, T.beaconIn, T.beaconIn + 18, T.calmA, T.calmB - 12)
  const detect = springAt(frame, fps, T.detect, { damping: 14, stiffness: 160 })
  const detectGlow = envelope(frame, T.detect, T.detect + 6, T.detect + 14, T.detect + 40)
  const flagIn = springAt(frame, fps, T.flag, { damping: 13, stiffness: 170 })
  const flagged = flagIn * (1 - ramp(frame, T.calmA, T.calmB))
  const label = envelope(frame, T.flag + 6, T.flag + 22, T.calmA - 6, T.calmA + 20)
  const beam = envelope(frame, T.detect, T.detect + 10, T.detect + 14, T.flag + 18)

  // Malla: líneas que unen la cuadrícula y bajan al bus.
  const mesh: string[] = []
  for (let c = 0; c < COLS; c++) mesh.push(`M${GX + c * STEP} ${GY} V${BUS_Y}`)
  for (let r = 0; r < COLS; r++) mesh.push(`M${GX} ${GY + r * STEP} H${GRID_RIGHT}`)
  mesh.push(`M${GX} ${BUS_Y} H${GRID_RIGHT}`)
  mesh.push(`M${ROUTER[0]} ${BUS_Y} H${GX}`)
  mesh.push(`M${ROUTER[0]} ${BUS_Y} V${ROUTER[1] - 46}`)

  // Destello de salida de cada cliente activo cuando emite un paquete.
  const emitGlow = new Map<number, number>()
  for (const p of PACKETS) {
    const ph = phase(frame - p.start, HERO_FRAMES) * HERO_FRAMES
    const g = ph < 12 ? 1 - ph / 12 : 0
    emitGlow.set(p.dot, Math.max(emitGlow.get(p.dot) ?? 0, g))
  }

  const tunnelDash = -phase(frame, 30) * 22 // marcha continua del cifrado (30 | 300)

  const flowT = ramp(frame, T.flowStart, T.flowStart + T.flowDur, (x) => x)
  const flowOn = frame >= T.flowStart && frame <= T.flowStart + T.flowDur

  return (
    <Stage width={W} height={H}>
      {/* Malla tenue */}
      <path d={mesh.join(' ')} stroke={NEON.border} strokeWidth={1.2} fill="none" opacity={0.7} />

      {/* Clientes */}
      <g>
        {Array.from({ length: 256 }, (_, i) => {
          const [x, y] = dotXY(i)
          if (i === FLAGGED) return null
          if (i === 0 || i === 255) {
            return (
              <circle
                key={i}
                cx={x}
                cy={y}
                r={5}
                fill="none"
                stroke={NEON.dotStrong}
                strokeWidth={1.6}
              />
            )
          }
          if (!active(i)) return <circle key={i} cx={x} cy={y} r={5.2} fill={NEON.dot} />
          const tw = breathe(frame, HERO_FRAMES, 1 + (i % 3), hash01(i) * 6.283)
          const e = emitGlow.get(i) ?? 0
          return (
            <g key={i}>
              {e > 0.01 ? (
                <circle
                  cx={x}
                  cy={y}
                  r={14}
                  fill={NEON.cyan}
                  opacity={0.35 * e}
                  filter="url(#blur6)"
                />
              ) : null}
              <circle
                cx={x}
                cy={y}
                r={5.4}
                fill={mix(NEON.dotStrong, NEON.lapis, 0.25 + 0.35 * tw + 0.4 * e)}
              />
            </g>
          )
        })}
      </g>

      {/* Paquetes de calma */}
      {PACKETS.map((p, k) => {
        const ph = phase(frame - p.start, HERO_FRAMES) * HERO_FRAMES
        if (ph > p.dur) return null
        return (
          <Glow key={k} blur={6} halo={1}>
            <Packet route={p.route} t={ph / p.dur} color={NEON.cyan} core={NEON.cyanCore} />
          </Glow>
        )
      })}

      {/* Router MikroTik */}
      <Glow blur={6} halo={0.7}>
        <rect
          x={ROUTER[0] - 46}
          y={ROUTER[1] - 46}
          width={92}
          height={92}
          rx={22}
          fill={NEON.bgPanel}
          stroke={NEON.lapisDeep}
          strokeWidth={2.5}
        />
        <RouterIcon x={ROUTER[0]} y={ROUTER[1]} size={48} color={NEON.lapis} />
      </Glow>
      <text
        x={ROUTER[0]}
        y={ROUTER[1] + 84}
        textAnchor="middle"
        fontFamily={FONT_MONO}
        fontSize={26}
        fill={NEON.textMuted}
      >
        MikroTik
      </text>

      {/* Túnel WireGuard: dos carriles con un trazo cifrado que marcha y un candado */}
      <Glow blur={6} halo={0.8}>
        <rect
          x={TUNNEL_A[0]}
          y={TUNNEL_A[1] - 26}
          width={TUNNEL_B[0] - TUNNEL_A[0]}
          height={52}
          rx={26}
          fill="none"
          stroke={NEON.lapisDeep}
          strokeWidth={2.5}
        />
        <line
          x1={TUNNEL_A[0] + 30}
          y1={TUNNEL_A[1]}
          x2={TUNNEL_B[0] - 30}
          y2={TUNNEL_B[1]}
          stroke={NEON.cyan}
          strokeWidth={2}
          strokeDasharray="4 18"
          strokeDashoffset={tunnelDash}
          strokeLinecap="round"
          opacity={0.8}
        />
      </Glow>
      <rect
        x={(TUNNEL_A[0] + TUNNEL_B[0]) / 2 - 26}
        y={TUNNEL_A[1] - 26}
        width={52}
        height={52}
        rx={14}
        fill={NEON.bg}
      />
      <Glow blur={3} halo={0.9}>
        <LockIcon
          x={(TUNNEL_A[0] + TUNNEL_B[0]) / 2}
          y={TUNNEL_A[1]}
          size={34}
          color={NEON.lapis}
        />
      </Glow>
      <text
        x={(TUNNEL_A[0] + TUNNEL_B[0]) / 2}
        y={TUNNEL_A[1] + 84}
        textAnchor="middle"
        fontFamily={FONT_MONO}
        fontSize={26}
        fill={NEON.textMuted}
      >
        WireGuard
      </text>

      {/* Horus */}
      <Glow blur={12} halo={0.55 + 0.45 * detectGlow} wide>
        <circle
          cx={HORUS[0]}
          cy={HORUS[1]}
          r={52 * (1 + 0.08 * detectGlow)}
          fill={NEON.bgPanel}
          stroke={mix(NEON.lapisDeep, NEON.cyan, detectGlow)}
          strokeWidth={3}
        />
        <HorusEye
          x={HORUS[0]}
          y={HORUS[1]}
          size={60 * (1 + 0.06 * detect * detectGlow)}
          color={mix(NEON.lapis, NEON.cyanCore, detectGlow)}
          stroke={1.8}
        />
      </Glow>
      <text
        x={HORUS[0]}
        y={HORUS[1] + 90}
        textAnchor="middle"
        fontFamily={FONT_MONO}
        fontSize={26}
        fill={NEON.textMuted}
      >
        Horus
      </text>

      {/* Incidente: 10.20.1.47 contacta un C2 */}
      {beacon > 0.001 ? (
        <g>
          <Glow blur={6} halo={0.9} opacity={beacon}>
            <path
              d={polyPath([flaggedXY, C2])}
              stroke={NEON.infected}
              strokeWidth={2.2}
              strokeDasharray="6 8"
              strokeDashoffset={-phase(frame, 15) * 14}
              fill="none"
            />
            <circle
              cx={C2[0]}
              cy={C2[1]}
              r={38}
              fill={NEON.bg}
              stroke={NEON.infected}
              strokeWidth={2.5}
            />
            <ServerIcon x={C2[0]} y={C2[1] - 2} size={36} color={NEON.infectedCore} />
          </Glow>
          <text
            x={C2[0]}
            y={C2[1] + 68}
            textAnchor="middle"
            fontFamily={FONT_MONO}
            fontSize={24}
            fill={NEON.infectedCore}
            opacity={beacon}
          >
            C2
          </text>
          {T.beacons.map((b) => {
            const t = (frame - b) / 24
            if (t < 0 || t > 1) return null
            return (
              <Glow key={b} blur={6} halo={1}>
                <Packet
                  route={[flaggedXY, C2]}
                  t={t}
                  color={NEON.infected}
                  core={NEON.infectedCore}
                />
              </Glow>
            )
          })}
        </g>
      ) : null}

      {/* Su flujo llega a Horus por el camino normal (los flujos de .47 son los que delatan) */}
      {flowOn ? (
        <Glow blur={6} halo={1}>
          <Packet
            route={routeFrom(FLAGGED)}
            t={flowT}
            color={NEON.infected}
            core={NEON.infectedCore}
            size={1.15}
          />
        </Glow>
      ) : null}

      {/* Horus señala la IP: haz breve de Horus a .47 */}
      {beam > 0.001 ? (
        <Glow blur={6} halo={1} opacity={beam}>
          <path
            d={polyPath([
              [HORUS[0], HORUS[1] - 52],
              along([[HORUS[0], HORUS[1] - 52], flaggedXY], ramp(frame, T.detect, T.flag)),
            ])}
            stroke={NEON.cyan}
            strokeWidth={2}
            fill="none"
            strokeLinecap="round"
          />
        </Glow>
      ) : null}

      {/* 10.20.1.47: punto (activo ↔ Infectado), anillos de pulso y etiqueta */}
      {T.rings.map((r) => {
        const k = ramp(frame, r, r + 34, (x) => 1 - Math.pow(1 - x, 3))
        if (frame < r || frame > r + 34 || flagged < 0.05) return null
        return (
          <circle
            key={r}
            cx={flaggedXY[0]}
            cy={flaggedXY[1]}
            r={10 + 46 * k}
            fill="none"
            stroke={NEON.infected}
            strokeWidth={3 * (1 - k) + 0.5}
            opacity={(1 - k) * flagged}
          />
        )
      })}
      <Glow blur={6} halo={flagged} wide={flagged > 0.05}>
        <circle
          cx={flaggedXY[0]}
          cy={flaggedXY[1]}
          r={15}
          fill="none"
          stroke={NEON.infected}
          strokeWidth={3}
          opacity={flagged}
        />
        <circle
          cx={flaggedXY[0]}
          cy={flaggedXY[1]}
          r={5.4 + 2.6 * flagged}
          fill={mix(mix(NEON.dotStrong, NEON.lapis, 0.4), NEON.infectedCore, flagged)}
        />
      </Glow>
      {label > 0.001 ? (
        <g opacity={label} transform={`translate(${-12 * (1 - label)} 0)`}>
          <rect
            x={flaggedXY[0] - 226}
            y={flaggedXY[1] + 24}
            width={196}
            height={48}
            rx={12}
            fill={NEON.bg}
            stroke={NEON.infected}
            strokeWidth={2}
          />
          <text
            x={flaggedXY[0] - 128}
            y={flaggedXY[1] + 57}
            textAnchor="middle"
            fontFamily={FONT_MONO}
            fontSize={26}
            fontWeight={700}
            fill={NEON.text}
          >
            10.20.1.47
          </text>
        </g>
      ) : null}
    </Stage>
  )
}
