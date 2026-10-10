// Resilience — lo que mide tests/load/REPORT.md › "Resultados de las pruebas de fallo":
// con ClickHouse (o NATS, o horus-app) parado 30 s durante la ingesta, los flujos se acumulan en
// el búfer (TLM_FLOWS) y se vacían al volver: 0 flujos perdidos, backlog drenado en 12–20 s.
//
// La animación comprime el tiempo (8 s de loop) y no lleva cifras ni texto: la afirmación exacta
// va en el HTML de la sección de fiabilidad, junto a su condición de prueba.
//
//   collector → búfer → ClickHouse;  ClickHouse se apaga (gris, nunca rojo: el rojo solo
//   significa Infectado), el búfer se llena; ClickHouse vuelve, el búfer se vacía.
import { useCurrentFrame, useVideoConfig } from 'remotion'
import { DatabaseIcon, ServerIcon } from '../lib/icons'
import {
  along,
  envelope,
  interpolateClamp,
  mix,
  phase,
  polyPath,
  ramp,
  springAt,
  type Pt,
} from '../lib/motion'
import { Glow, Stage } from '../lib/Neon'
import { NEON } from '../lib/theme'

export const RES_FPS = 30
export const RES_FRAMES = 240 // 8 s
const W = 1920
const H = 640
const Y = 320

const SRC: Pt = [300, Y]
const BUF = { x: 960 - 120, y: Y - 160, w: 240, h: 320 }
const DST: Pt = [1620, Y]
const IN: Pt[] = [
  [SRC[0] + 80, Y],
  [BUF.x, Y],
]
const OUT: Pt[] = [
  [BUF.x + BUF.w, Y],
  [DST[0] - 80, Y],
]

const DOWN = 40 // ClickHouse se apaga
const UP = 128 // vuelve
const DRAINED = 196 // búfer vacío otra vez
const ROWS = 14

export function Resilience() {
  const frame = useCurrentFrame()
  const { fps } = useVideoConfig()

  const off = envelope(frame, DOWN, DOWN + 10, UP - 4, UP + 6) // 1 = apagado
  const back = springAt(frame, fps, UP, { damping: 14 }) * (1 - ramp(frame, UP + 30, UP + 60))
  const level = interpolateClamp(frame, [DOWN, UP, UP + 6, DRAINED], [0, 1, 1, 0])
  const rows = Math.round(level * ROWS)

  // Entrada constante: un paquete cada 10 frames (10 | 240), siempre llega al búfer.
  const inPackets = Array.from({ length: 4 }, (_, k) => phase(frame - k * 10, 40))
  // Salida: normal (cada 10 frames) salvo con ClickHouse apagado; durante el drenaje, más rápida.
  const draining = frame > UP && frame < DRAINED
  const outPeriod = draining ? 14 : 40
  const outCount = draining ? 7 : 4
  const outPackets = Array.from({ length: outCount }, (_, k) =>
    phase(frame - k * (outPeriod / outCount), outPeriod),
  )
  const dstLit = 1 - off
  const dstColor = mix(NEON.dotStrong, NEON.lapis, dstLit)

  return (
    <Stage width={W} height={H}>
      <g fill="none" strokeWidth={2.5} strokeLinecap="round">
        <path d={polyPath(IN)} stroke={NEON.border} />
        <path
          d={polyPath(OUT)}
          stroke={NEON.border}
          strokeDasharray={off > 0.5 ? '6 10' : undefined}
        />
      </g>

      {/* Collector */}
      <Glow blur={6} halo={0.6}>
        <rect
          x={SRC[0] - 80}
          y={Y - 80}
          width={160}
          height={160}
          rx={34}
          fill={NEON.bgPanel}
          stroke={NEON.lapisDeep}
          strokeWidth={3}
        />
        <ServerIcon x={SRC[0]} y={Y} size={76} color={NEON.lapis} />
      </Glow>

      {/* Búfer: los registros se apilan desde abajo */}
      <Glow blur={6} halo={0.45 + 0.4 * level}>
        <rect
          x={BUF.x}
          y={BUF.y}
          width={BUF.w}
          height={BUF.h}
          rx={30}
          fill={NEON.bgPanel}
          stroke={mix(NEON.lapisDeep, NEON.cyan, level)}
          strokeWidth={3}
        />
      </Glow>
      <Glow blur={3} halo={0.9}>
        {Array.from({ length: rows }, (_, r) => (
          <rect
            key={r}
            x={BUF.x + 28}
            y={BUF.y + BUF.h - 30 - (r + 1) * 19}
            width={BUF.w - 56}
            height={10}
            rx={5}
            fill={mix(NEON.lapisDeep, NEON.cyan, r / ROWS)}
          />
        ))}
      </Glow>

      {/* ClickHouse */}
      <Glow blur={12} halo={0.15 + 0.55 * dstLit + 0.3 * back} wide={back > 0.05}>
        <rect
          x={DST[0] - 80}
          y={Y - 80}
          width={160}
          height={160}
          rx={34}
          fill={NEON.bgPanel}
          stroke={mix(NEON.borderStrong, NEON.lapisDeep, dstLit)}
          strokeWidth={3}
          strokeDasharray={off > 0.5 ? '10 10' : undefined}
        />
        <DatabaseIcon x={DST[0]} y={Y} size={76} color={mix(dstColor, NEON.cyanCore, back)} />
      </Glow>

      {/* Paquetes */}
      <Glow blur={6} halo={1}>
        {inPackets.map((t, k) => {
          const [x, y] = along(IN, t)
          return (
            <circle
              key={`i${k}`}
              cx={x}
              cy={y}
              r={6}
              fill={NEON.cyanCore}
              opacity={ramp(t, 0, 0.1) * (1 - ramp(t, 0.9, 1))}
            />
          )
        })}
        {off < 0.5
          ? outPackets.map((t, k) => {
              const [x, y] = along(OUT, t)
              return (
                <circle
                  key={`o${k}`}
                  cx={x}
                  cy={y}
                  r={6}
                  fill={NEON.cyanCore}
                  opacity={ramp(t, 0, 0.1) * (1 - ramp(t, 0.9, 1)) * (1 - off * 2)}
                />
              )
            })
          : null}
      </Glow>
    </Stage>
  )
}
