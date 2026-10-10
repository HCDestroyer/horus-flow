// IP real del cliente para el rate limit. Solo se confía en X-Forwarded-For cuando la conexión
// viene de un proxy declarado en TRUSTED_PROXIES (p. ej. Nginx Proxy Manager); si no, cualquiera
// podría falsificar la cabecera y saltarse el límite.
import { BlockList, isIP } from 'node:net'

/** Quita el prefijo IPv4 mapeado en IPv6 (::ffff:1.2.3.4 → 1.2.3.4) y los corchetes. */
export function normalizeIp(raw: string | undefined | null): string {
  if (!raw) return ''
  let ip = raw.trim()
  if (ip.startsWith('[') && ip.includes(']')) ip = ip.slice(1, ip.indexOf(']'))
  const mapped = /^::ffff:(\d+\.\d+\.\d+\.\d+)$/i.exec(ip)
  if (mapped?.[1]) ip = mapped[1]
  return ip
}

function family(ip: string): 'ipv4' | 'ipv6' | null {
  const v = isIP(ip)
  return v === 4 ? 'ipv4' : v === 6 ? 'ipv6' : null
}

/** Construye la lista de proxies de confianza a partir de IPs o CIDR. Ignora entradas no válidas. */
export function buildTrustList(entries: string[]): BlockList | null {
  if (entries.length === 0) return null
  const list = new BlockList()
  let added = 0
  for (const entry of entries) {
    const [addrRaw, prefixRaw] = entry.split('/')
    const addr = normalizeIp(addrRaw)
    const fam = family(addr)
    if (!fam) continue
    if (prefixRaw === undefined) {
      list.addAddress(addr, fam)
    } else {
      const prefix = Number.parseInt(prefixRaw, 10)
      const max = fam === 'ipv4' ? 32 : 128
      if (!Number.isInteger(prefix) || prefix < 0 || prefix > max) continue
      list.addSubnet(addr, prefix, fam)
    }
    added++
  }
  return added > 0 ? list : null
}

function isTrusted(ip: string, trust: BlockList | null): boolean {
  if (!trust) return false
  const fam = family(ip)
  return fam !== null && trust.check(ip, fam)
}

/**
 * IP del cliente: la dirección del socket, salvo que venga de un proxy de confianza; entonces
 * se recorre X-Forwarded-For de derecha a izquierda y se toma la primera IP que no es un proxy
 * de confianza. Una entrada no válida corta el recorrido (no se sigue leyendo lo que pudo
 * escribir el cliente).
 */
export function resolveClientIp(
  remoteAddress: string | undefined,
  forwardedFor: string | string[] | undefined,
  trust: BlockList | null,
): string {
  const remote = normalizeIp(remoteAddress)
  if (!remote || !isTrusted(remote, trust)) return remote || 'unknown'
  const header = Array.isArray(forwardedFor) ? forwardedFor.join(',') : (forwardedFor ?? '')
  const hops = header
    .split(',')
    .map((s) => normalizeIp(s))
    .filter(Boolean)
  let candidate = remote
  for (let i = hops.length - 1; i >= 0; i--) {
    const hop = hops[i]!
    if (!family(hop)) break
    candidate = hop
    if (!isTrusted(hop, trust)) return hop
  }
  return candidate
}
