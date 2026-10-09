/**
 * Prefijos IPv4/IPv6 para la API simulada (solapes de `client_prefix`, búsqueda por IP).
 * Sin dependencias: direcciones como BigInt.
 */

export interface ParsedCidr {
  family: 4 | 6
  /** Primera dirección (red). */
  network: bigint
  length: number
}

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

export function parseIp(ip: string): { family: 4 | 6; value: bigint } | null {
  const v4 = parseV4(ip.trim())
  if (v4 !== null) return { family: 4, value: v4 }
  const v6 = parseV6(ip.trim().toLowerCase())
  return v6 === null ? null : { family: 6, value: v6 }
}

export function parseCidr(cidr: string): ParsedCidr | null {
  const [ip, len, extra] = cidr.trim().split('/')
  if (extra !== undefined) return null
  const parsed = parseIp(ip ?? '')
  if (!parsed) return null
  const bits = parsed.family === 4 ? 32 : 128
  const length = len === undefined ? bits : Number(len)
  if (!Number.isInteger(length) || length < 0 || length > bits) return null
  const mask = length === 0 ? 0n : ((1n << BigInt(length)) - 1n) << BigInt(bits - length)
  return { family: parsed.family, network: parsed.value & mask, length }
}

/** ¿`inner` está dentro de `outer` (o son iguales)? */
export function contains(outer: ParsedCidr, inner: ParsedCidr) {
  if (outer.family !== inner.family || outer.length > inner.length) return false
  const bits = outer.family === 4 ? 32 : 128
  const shift = BigInt(bits - outer.length)
  return outer.network >> shift === inner.network >> shift
}

export function overlaps(a: string, b: string) {
  const pa = parseCidr(a)
  const pb = parseCidr(b)
  return !!pa && !!pb && (contains(pa, pb) || contains(pb, pa))
}
