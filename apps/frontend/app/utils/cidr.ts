/**
 * Rangos de túnel del hub WireGuard (platform.yaml `POST /platform/wireguard/hubs/{id}/pools`):
 * la UI comprueba antes de enviar lo mismo que el backend (y la API simulada):
 * IPv4 entre /16 y /28 o IPv6 entre /64 y /120, escrito como dirección de red, fuera de
 * 100.64.0.0/10 (CGNAT, lo usan los ISP para sus clientes) y sin solaparse con los rangos que
 * ya tiene el hub ni con el de servicios. Sin dependencias: direcciones como BigInt.
 */

export interface Cidr {
  family: 4 | 6
  /** Dirección de red (bits de host a cero). */
  network: bigint
  /** Dirección tal y como se escribió (para saber si era la de red). */
  address: bigint
  length: number
}

export const POOL_PREFIX_LIMITS = { 4: { min: 16, max: 28 }, 6: { min: 64, max: 120 } } as const

/** Espacio compartido de CGNAT (RFC 6598): nunca puede ser un rango de túneles. */
export const CGNAT_CIDR = '100.64.0.0/10'

function parseV4(ip: string): bigint | null {
  const parts = ip.split('.')
  if (parts.length !== 4) return null
  let value = 0n
  for (const p of parts) {
    if (!/^\d{1,3}$/.test(p) || Number(p) > 255) return null
    value = (value << 8n) | BigInt(p)
  }
  return value
}

function parseV6(ip: string): bigint | null {
  if (!/^[0-9a-f:]+$/i.test(ip) || (ip.match(/::/g)?.length ?? 0) > 1) return null
  const compressed = ip.includes('::')
  const [head, tail] = compressed ? ip.split('::') : [ip, '']
  const h = head ? head.split(':') : []
  const t = tail ? tail.split(':') : []
  const missing = 8 - h.length - t.length
  if (compressed ? missing < 1 : h.length !== 8) return null
  const groups = [...h, ...Array<string>(compressed ? missing : 0).fill('0'), ...t]
  let value = 0n
  for (const g of groups) {
    if (!/^[0-9a-f]{1,4}$/i.test(g)) return null
    value = (value << 16n) | BigInt(parseInt(g, 16))
  }
  return value
}

const bitsOf = (family: 4 | 6) => (family === 4 ? 32 : 128)

function maskOf(family: 4 | 6, length: number) {
  const bits = bitsOf(family)
  return length === 0 ? 0n : ((1n << BigInt(length)) - 1n) << BigInt(bits - length)
}

/** `a.b.c.d/n` o `x:y::/n` (la longitud es obligatoria); `null` si no es un CIDR. */
export function parseCidr(raw: string): Cidr | null {
  const parts = raw.trim().split('/')
  if (parts.length !== 2 || !/^\d{1,3}$/.test(parts[1]!)) return null
  const ip = parts[0]!
  const v4 = parseV4(ip)
  const v6 = v4 === null ? parseV6(ip.toLowerCase()) : null
  const family: 4 | 6 | null = v4 !== null ? 4 : v6 !== null ? 6 : null
  if (family === null) return null
  const length = Number(parts[1])
  if (length > bitsOf(family)) return null
  const address = (v4 ?? v6)!
  return { family, address, network: address & maskOf(family, length), length }
}

/** Dirección como texto (IPv6 comprimida según RFC 5952). */
export function formatIp(family: 4 | 6, value: bigint): string {
  if (family === 4) {
    return [24n, 16n, 8n, 0n].map((s) => String((value >> s) & 255n)).join('.')
  }
  const groups = Array.from({ length: 8 }, (_, i) =>
    Number((value >> BigInt(112 - i * 16)) & 0xffffn),
  )
  // El tramo de ceros más largo (de al menos dos grupos) se comprime con `::`.
  let best = { start: -1, len: 0 }
  for (let i = 0; i < 8;) {
    if (groups[i] !== 0) {
      i++
      continue
    }
    let j = i
    while (j < 8 && groups[j] === 0) j++
    if (j - i > best.len && j - i >= 2) best = { start: i, len: j - i }
    i = j
  }
  const hex = groups.map((g) => g.toString(16))
  if (best.start < 0) return hex.join(':')
  const head = hex.slice(0, best.start).join(':')
  const tail = hex.slice(best.start + best.len).join(':')
  return `${head}::${tail}`
}

export function formatCidr(c: Pick<Cidr, 'family' | 'network' | 'length'>) {
  return `${formatIp(c.family, c.network)}/${c.length}`
}

/** ¿Comparten alguna dirección? (uno contiene al otro o son iguales). */
export function cidrOverlaps(a: string | Cidr, b: string | Cidr): boolean {
  const pa = typeof a === 'string' ? parseCidr(a) : a
  const pb = typeof b === 'string' ? parseCidr(b) : b
  if (!pa || !pb || pa.family !== pb.family) return false
  const shorter = Math.min(pa.length, pb.length)
  const mask = maskOf(pa.family, shorter)
  return (pa.network & mask) === (pb.network & mask)
}

/**
 * Direcciones utilizables del rango (sin la de red ni la de broadcast). En IPv6 el número
 * puede superar el entero seguro de JavaScript: es una aproximación para mostrar.
 */
export function cidrSize(c: string | Cidr): number {
  const p = typeof c === 'string' ? parseCidr(c) : c
  if (!p) return 0
  return Number(2n ** BigInt(bitsOf(p.family) - p.length)) - 2
}

export type PoolCidrError =
  | 'required'
  | 'syntax'
  | 'ipv6'
  | 'prefix'
  | 'network'
  | 'cgnat'
  | 'overlapPool'
  | 'overlapServices'

export type PoolCidrCheck =
  | { ok: true; cidr: string; family: 4 | 6 }
  | {
      ok: false
      error: PoolCidrError
      /** Para `network`: la dirección de red correcta; para solapes: el rango en conflicto. */
      detail?: string
      family?: 4 | 6
    }

/** Comprueba un rango nuevo frente a los del hub y al de servicios. */
export function checkPoolCidr(
  raw: string,
  existing: { pools: readonly string[]; services?: string | null },
): PoolCidrCheck {
  const value = raw.trim()
  if (!value) return { ok: false, error: 'required' }
  const c = parseCidr(value)
  if (!c) return { ok: false, error: 'syntax' }
  // Las direcciones de túnel de los routers son IPv4 (script de RouterOS y origen IPFIX): el
  // servidor rechaza los rangos IPv6 aunque estén bien formados.
  if (c.family === 6) return { ok: false, error: 'ipv6', family: 6 }
  const limits = POOL_PREFIX_LIMITS[c.family]
  if (c.length < limits.min || c.length > limits.max) {
    return { ok: false, error: 'prefix', family: c.family }
  }
  if (c.address !== c.network) {
    return { ok: false, error: 'network', detail: formatCidr(c), family: c.family }
  }
  if (cidrOverlaps(c, CGNAT_CIDR)) return { ok: false, error: 'cgnat', family: c.family }
  const pool = existing.pools.find((p) => cidrOverlaps(c, p))
  if (pool) return { ok: false, error: 'overlapPool', detail: pool, family: c.family }
  if (existing.services && cidrOverlaps(c, existing.services)) {
    return { ok: false, error: 'overlapServices', detail: existing.services, family: c.family }
  }
  return { ok: true, cidr: formatCidr(c), family: c.family }
}

/**
 * Sugerencia de rango libre: el primer /16 privado dentro de 10.0.0.0/8, bajando desde
 * 10.255.0.0/16 (la parte alta de 10/8 es la que menos usan las redes internas de un ISP),
 * que no se solape con los rangos del hub ni con el de servicios. `null` si no queda ninguno.
 */
export function suggestPoolCidr(pools: readonly string[], services?: string | null): string | null {
  const taken = [...pools, ...(services ? [services] : [])]
  for (let b = 255; b >= 0; b--) {
    const candidate = `10.${b}.0.0/16`
    if (!taken.some((t) => cidrOverlaps(candidate, t))) return candidate
  }
  return null
}
