import type {
  ClientPrefix,
  CustomerDetail,
  FlowExporter,
  KindChange,
  Peer,
  PrefixImportPreview,
  PrefixProposal,
  Router,
  Site,
} from '~~/types/api'
import {
  baseAlias,
  CATEGORIES,
  customerAddress,
  customerSite,
  INACTIVE_INDICES,
  isBaseCommercial,
  isV6Customer,
  openFindings,
  tenantScale,
  v6CustomerPrefix,
  V6_CLIENT_LEN,
} from './base'
import { ALL_TENANTS, type MockTenant } from './data'
import { contains, parseCidr, parseIp } from './net'
import { hash, iso, mockUuid, rng } from './random'
import { mockState, type OnboardingRun } from './state'

/**
 * Inventario simulado (nodos, routers, túneles, exportadores, prefijos y clientes) derivado
 * de la misma base que los widgets (`mocks/base.ts`). Fibra Norte tiene además un nodo en
 * modo descubrimiento (Nodo Costa, sin prefijos) y un router recién dado de alta (Nodo Lago,
 * pendiente de configurar); Valle Conecta, un router silencioso (Nodo Ribera).
 */

const DAY = 86_400_000

export function tenantIndex(tenant: MockTenant) {
  return ALL_TENANTS.findIndex((t) => t.tenant_id === tenant.tenant_id) + 1
}

export type SiteRole = 'active' | 'discovery' | 'pending'
export interface MockSite {
  id: string
  name: string
  role: SiteRole
  index: number
}

export function sitesOf(tenant: MockTenant): MockSite[] {
  const sites: MockSite[] = tenant.sites.map((s, index) => ({ ...s, role: 'active', index }))
  if (tenant.tenant_slug === 'fibra-norte') {
    sites.push(
      { id: mockUuid('0192e111', 105), name: 'Nodo Costa', role: 'discovery', index: 4 },
      { id: mockUuid('0192e111', 106), name: 'Nodo Lago', role: 'pending', index: 5 },
    )
  }
  return sites
}

export function findSite(tenant: MockTenant, id: string) {
  return sitesOf(tenant).find((s) => s.id === id)
}

function stamp(tenant: MockTenant) {
  return { tenant_id: tenant.tenant_id, created_at: '2026-09-02T14:00:00.000Z' }
}

const privateRealm = (tenant: MockTenant, site: MockSite) =>
  mockUuid('0192e444', tenantIndex(tenant) * 100 + site.index + 1)
const publicRealm = (tenant: MockTenant) => mockUuid('0192e555', tenantIndex(tenant))

// --- Prefijos de clientes --------------------------------------------------------------------

/** Prefijos del nodo: los iniciales y los que el usuario añadió, importó o aceptó. */
export function prefixesOf(tenant: MockTenant, site: MockSite): ClientPrefix[] {
  let list = mockState.prefixes.get(site.id)
  if (!list) {
    list = site.role === 'active' ? initialPrefixes(tenant, site) : []
    mockState.prefixes.set(site.id, list)
  }
  return list
}

function initialPrefixes(tenant: MockTenant, site: MockSite): ClientPrefix[] {
  const n = tenantIndex(tenant) * 1000 + site.index * 10
  const common = {
    ...stamp(tenant),
    updated_at: '2026-09-02T14:00:00.000Z',
    version: 1,
    site_id: site.id,
    confirmed: true,
  }
  return [
    {
      ...common,
      id: mockUuid('0192e666', n + 1),
      prefix: `${tenant.prefix}.0.0/20`,
      role: 'customers',
      assignment_mode: 'dynamic',
      default_kind: 'residential',
      source: 'routeros_api',
      note: 'pool-pppoe',
      realm_id: privateRealm(tenant, site),
      realm_kind: 'node_private',
      ipv6_client_len: null,
    },
    {
      ...common,
      id: mockUuid('0192e666', n + 2),
      prefix: v6CustomerPrefix(tenant),
      role: 'customers',
      assignment_mode: 'dynamic',
      default_kind: 'residential',
      source: 'routeros_api',
      note: 'pd-clientes (DHCPv6-PD)',
      realm_id: publicRealm(tenant),
      realm_kind: 'public',
      ipv6_client_len: V6_CLIENT_LEN,
    },
    {
      ...common,
      id: mockUuid('0192e666', n + 3),
      prefix: `${tenant.prefix}.255.0/24`,
      role: 'infrastructure',
      assignment_mode: 'static',
      default_kind: 'residential',
      source: 'manual',
      note: 'Gestión y enlaces',
      realm_id: privateRealm(tenant, site),
      realm_kind: 'node_private',
      ipv6_client_len: null,
    },
  ]
}

export function newPrefix(
  tenant: MockTenant,
  site: MockSite,
  input: Pick<ClientPrefix, 'prefix' | 'role'> & Partial<ClientPrefix>,
  now: Date,
): ClientPrefix {
  const v6 = parseCidr(input.prefix)?.family === 6
  return {
    ...stamp(tenant),
    created_at: now.toISOString(),
    updated_at: now.toISOString(),
    version: 1,
    id: mockUuid('0192e777', hash(`${site.id}${input.prefix}${now.getTime()}`)),
    site_id: site.id,
    confirmed: true,
    assignment_mode: input.assignment_mode ?? 'unknown',
    default_kind: input.default_kind ?? 'residential',
    source: input.source ?? 'manual',
    note: input.note ?? null,
    prefix: input.prefix,
    role: input.role,
    ipv6_client_len: v6 ? (input.ipv6_client_len ?? 64) : null,
    realm_id: v6 ? publicRealm(tenant) : privateRealm(tenant, site),
    realm_kind: v6 ? 'public' : 'node_private',
  }
}

/** Prefijo existente del nodo que solapa con `prefix` (misma familia, contiene o contenido). */
export function overlapping(tenant: MockTenant, site: MockSite, prefix: string) {
  const p = parseCidr(prefix)
  if (!p) return undefined
  return prefixesOf(tenant, site).find((x) => {
    const q = parseCidr(x.prefix)
    return !!q && (contains(p, q) || contains(q, p))
  })
}

// --- Routers, túneles y exportadores ---------------------------------------------------------

export function routerName(site: { name: string }) {
  return `rt-${site.name
    .replace('Nodo ', '')
    .toLowerCase()
    .normalize('NFD')
    .replace(/[̀-ͯ]/g, '')
    .replace(/\s+/g, '-')}`
}

export interface OnboardingProgress {
  stage: Router['onboarding_state']
  run?: OnboardingRun
  tokenState: Peer['enrollment']['token_state']
  keyAt?: number
  tunnelAt?: number
  flowAt?: number
}

/** Pasos del asistente para un router pendiente, según cuándo se generó el script. */
export function onboardingProgress(
  routerId: string,
  now: number,
  stepMs: number,
): OnboardingProgress {
  const run = mockState.onboarding.get(routerId)
  if (!run) return { stage: 'pending_configuration', tokenState: 'none' }
  const keyAt = run.scriptAt + stepMs
  const tokenDead = (run.revokedAt && run.revokedAt < keyAt) || run.expiresAt < keyAt
  if (tokenDead || now < keyAt) {
    const tokenState = run.revokedAt
      ? 'revoked'
      : run.expiresAt <= Math.min(now, keyAt)
        ? 'expired'
        : 'pending'
    return { stage: 'pending_configuration', run, tokenState }
  }
  const tunnelAt = keyAt + stepMs
  const flowAt = tunnelAt + stepMs
  const stage = now >= flowAt ? 'exporting' : now >= tunnelAt ? 'tunnel_up' : 'key_received'
  return { stage, run, tokenState: 'used', keyAt, tunnelAt, flowAt }
}

export interface MockRouter {
  site: MockSite
  router: Router
  exporter: FlowExporter
  peer: Peer
  progress: OnboardingProgress
}

export function routersOf(
  tenant: MockTenant,
  now: Date,
  options: { degraded?: boolean; stepMs?: number } = {},
): MockRouter[] {
  const t = now.getTime()
  const ti = tenantIndex(tenant)
  const r = rng(hash(tenant.tenant_id + 'exporters'))
  const active = sitesOf(tenant).filter((s) => s.role === 'active')
  return sitesOf(tenant).map((site) => {
    const id = mockUuid('0192e333', ti * 100 + site.index + 1)
    const pending = site.role === 'pending'
    const progress = pending
      ? onboardingProgress(id, t, options.stepMs ?? 6000)
      : ({ stage: 'exporting', tokenState: 'used' } as OnboardingProgress)
    const exporting = progress.stage === 'exporting'
    const silentAlways = tenant.tenant_slug === 'valle-conecta' && site.index === 1
    const silentDegraded = !!options.degraded && site.role === 'active' && site === active.at(-1)
    const silent = !pending && (silentAlways || silentDegraded)
    const lossy = !silent && site.role === 'active' && site.index === 1
    const fps = Math.round(250 + r() * 600)
    const loss = Math.round(r() * 20) / 10_000
    const tunnel = `10.255.${ti}.${site.index + 2}/32`
    const silentSince = t - (silentAlways ? 42 : 7) * 60_000
    const hasTunnel = !pending || progress.stage !== 'pending_configuration'

    const state: FlowExporter['state'] = pending
      ? exporting
        ? 'exporting'
        : 'pending_configuration'
      : silent
        ? 'silent'
        : lossy
          ? 'lossy'
          : 'exporting'
    const stateSince = pending
      ? exporting
        ? progress.flowAt!
        : t - 25 * 60_000
      : silent
        ? silentSince
        : t - (2 + site.index) * 3_600_000

    const router: Router = {
      ...stamp(tenant),
      updated_at: iso(t - 3_600_000),
      version: 3,
      id,
      name: routerName(site),
      display_name: `Router principal ${site.name.replace('Nodo ', '')}`,
      site_id: site.id,
      is_primary: true,
      vendor: 'mikrotik',
      model: site.index % 2 ? 'CCR2004-1G-12S+2XS' : 'CCR2116-12G-4S+',
      routeros_version: '7.16.2',
      routeros_version_detected: hasTunnel ? '7.16.2' : null,
      routeros_version_supported: true,
      tags: [],
      admin_state: 'active',
      onboarding_state: progress.stage,
      tunnel_address: hasTunnel ? tunnel : null,
      wireguard_peer_id: mockUuid('0192e888', ti * 100 + site.index + 1),
      warnings: lossy ? ['hardware_offload_suspected'] : [],
      credentials: [
        {
          kind: 'routeros_api',
          configured: hasTunnel,
          last_result: hasTunnel ? 'ok' : null,
          last_used_at: hasTunnel ? iso(t - 40 * 60_000) : null,
        },
      ],
    }
    if (pending) router.created_at = iso(t - 25 * 60_000)

    const exporter: FlowExporter = {
      tenant_id: tenant.tenant_id,
      router_id: id,
      site_id: site.id,
      version: 1,
      state,
      state_since: iso(stateSince),
      exporter_ip: hasTunnel ? tunnel.replace('/32', '') : null,
      flow_source: state === 'pending_configuration' ? null : 'ipfix',
      flows_per_second: state === 'exporting' || state === 'lossy' ? fps : null,
      last_flow_at:
        state === 'pending_configuration'
          ? null
          : iso(silent ? silentSince : t - Math.round(1 + r() * 4) * 1000),
      loss_ratio_5m: state === 'exporting' ? loss : lossy ? 0.031 : null,
      sampling_rate: state === 'pending_configuration' ? null : 1,
      clock_skew_seconds: state === 'pending_configuration' ? null : 0.2,
      coverage_ratio: null,
      hints: silent
        ? ['check_tunnel', 'check_firewall', 'check_traffic_flow_target']
        : lossy
          ? ['hardware_offload_suspected']
          : [],
    }

    const handshake: Peer['handshake_state'] = !hasTunnel
      ? 'never'
      : pending && progress.stage === 'key_received'
        ? 'never'
        : silentAlways
          ? 'stale'
          : 'ok'
    const peer: Peer = {
      ...stamp(tenant),
      updated_at: iso(t - 60_000),
      version: 2,
      id: router.wireguard_peer_id!,
      router_id: id,
      server_id: mockUuid('0192e999', 1),
      address: tunnel,
      public_key: hasTunnel ? `${btoaId(id)}=` : null,
      endpoint: hasTunnel ? `190.85.${ti}.${20 + site.index}:13231` : null,
      persistent_keepalive_seconds: 25,
      handshake_state: handshake,
      last_handshake_at:
        handshake === 'never'
          ? null
          : iso(silentAlways ? silentSince : pending ? progress.tunnelAt! : t - 12_000),
      status: !hasTunnel
        ? 'awaiting_enrollment'
        : handshake === 'never'
          ? 'pending_handshake'
          : 'active',
      enrollment: {
        token_state: progress.tokenState,
        token_id: progress.run?.tokenId ?? null,
        token_expires_at: progress.run ? iso(progress.run.expiresAt) : null,
        enrolled_at: progress.keyAt
          ? iso(progress.keyAt)
          : hasTunnel
            ? '2026-09-02T15:10:00.000Z'
            : null,
      },
    }
    return { site, router, exporter, peer, progress }
  })
}

/** Clave pública WireGuard sintética (44 caracteres base64). */
function btoaId(id: string) {
  const hex = id.replace(/-/g, '')
  const bytes = hex.match(/../g)!.map((b) => String.fromCharCode(parseInt(b, 16)))
  const raw = (bytes.join('') + bytes.join('')).slice(0, 32)
  return btoa(raw).slice(0, 43)
}

/** Script de alta para RouterOS 7 (text/plain). Horus no lo ejecuta: lo pega el técnico. */
export function provisioningScript(
  tenant: MockTenant,
  item: MockRouter,
  token: string,
  endpoint: string,
) {
  const tunnel = `10.255.${tenantIndex(tenant)}.${item.site.index + 2}`
  return [
    `# Horus Flow · alta de ${item.router.name} (${item.site.name}) · ${tenant.tenant_name}`,
    '# Requisitos: RouterOS v7 >= 7.12. Pegar en una terminal del router (Winbox o SSH).',
    '# Contiene secretos de un solo uso: no lo guardes en tickets ni chats.',
    ':local horusToken "' + token + '"',
    ':local horusPass "' + token.slice(-12).split('').reverse().join('') + '"',
    '/interface wireguard add name=wg-horus listen-port=13231 comment="horus"',
    `/ip address add address=${tunnel}/32 interface=wg-horus comment="horus"`,
    `/interface wireguard peers add interface=wg-horus endpoint-address=${endpoint} endpoint-port=51820 public-key="HoRuSHubPublicKeyExampleBase64xxxxxxxxxxxxx=" allowed-address=10.255.0.1/32 persistent-keepalive=25s comment="horus"`,
    '/ip route add dst-address=10.255.0.1/32 gateway=wg-horus comment="horus"',
    `/ip traffic-flow set enabled=yes interfaces=all cache-entries=128k active-flow-timeout=1m inactive-flow-timeout=15s`,
    '/ip traffic-flow target add dst-address=10.255.0.1 port=4739 version=ipfix comment="horus"',
    '/user group add name=horus-ro policy=read,api,rest-api,!write,!policy comment="horus"',
    '/user add name=horus-ro group=horus-ro password=$horusPass comment="horus"',
    ':local pub [/interface wireguard get [find name=wg-horus] public-key]',
    `/tool fetch url="https://${endpoint}/api/v1/enroll/wireguard" http-method=post http-header-field="Content-Type: application/json" http-data=("{\\"token\\":\\"" . $horusToken . "\\",\\"public_key\\":\\"" . $pub . "\\"}") output=none`,
    ':put "Horus: clave enviada. Vuelve a la ficha del router en Horus para ver el progreso."',
    '',
  ].join('\n')
}

export function deprovisioningScript(item: MockRouter) {
  return [
    `# Horus Flow · desinstalación de ${item.router.name}`,
    '/ip traffic-flow target remove [find comment="horus"]',
    '/ip route remove [find comment="horus"]',
    '/interface wireguard peers remove [find comment="horus"]',
    '/ip address remove [find comment="horus"]',
    '/interface wireguard remove [find comment="horus"]',
    '/user remove [find comment="horus"]',
    '/user group remove [find comment="horus"]',
    '',
  ].join('\n')
}

/** Vista previa de importación desde el MikroTik (solo lectura; I1-28, E-IPv6-1). */
export function prefixImportPreview(
  tenant: MockTenant,
  item: MockRouter,
  now: Date,
): PrefixImportPreview {
  const site = item.site
  const existing = prefixesOf(tenant, site)
  const v6 = v6CustomerPrefix(tenant)
  const raw: Omit<PrefixImportPreview['items'][number], 'diff' | 'existing_client_prefix_id'>[] = [
    {
      prefix: `${tenant.prefix}.0.0/20`,
      origin: 'ip_pool',
      origin_name: 'pool-pppoe',
      suggested_role: 'customers',
      suggested_assignment_mode: 'dynamic',
    },
    {
      prefix: `${tenant.prefix}.32.0/24`,
      origin: 'ip_pool',
      origin_name: 'pool-hotspot',
      suggested_role: 'customers',
      suggested_assignment_mode: 'dynamic',
    },
    {
      prefix: '100.64.40.0/22',
      origin: 'ip_pool',
      origin_name: 'pool-cgnat',
      suggested_role: 'customers',
      suggested_assignment_mode: 'dynamic',
    },
    {
      prefix: `${tenant.prefix}.0.0/16`,
      origin: 'interface_address',
      origin_name: 'bridge-clientes',
      suggested_role: 'customers',
      suggested_assignment_mode: 'unknown',
    },
    {
      prefix: `${tenant.prefix}.250.0/29`,
      origin: 'interface_address',
      origin_name: 'ether1-uplink',
      suggested_role: 'infrastructure',
      suggested_assignment_mode: 'static',
    },
    {
      prefix: v6,
      origin: 'ipv6_pool',
      origin_name: 'pd-clientes',
      suggested_role: 'customers',
      suggested_assignment_mode: 'dynamic',
      delegated_prefix_length: V6_CLIENT_LEN,
      ipv6_pool_usage: 'dhcpv6_pd',
      suggested_ipv6_client_len: V6_CLIENT_LEN,
    },
    {
      prefix: v6.replace('00::/40', 'f0::/44'),
      origin: 'ipv6_pool',
      origin_name: 'pd-empresas',
      suggested_role: 'customers',
      suggested_assignment_mode: 'static',
      delegated_prefix_length: 48,
      ipv6_pool_usage: 'dhcpv6_pd',
      suggested_ipv6_client_len: 48,
    },
    {
      prefix: v6.replace('00::/40', 'ff:ff00::/56'),
      origin: 'ipv6_pool',
      origin_name: 'ppp-enlaces',
      suggested_role: 'infrastructure',
      suggested_assignment_mode: 'dynamic',
      delegated_prefix_length: 64,
      ipv6_pool_usage: 'ppp_link_shared',
      suggested_ipv6_client_len: null,
    },
    {
      prefix: v6.replace('00::/40', 'fe:4000::/50'),
      origin: 'ipv6_pool',
      origin_name: 'pd-pruebas',
      suggested_role: 'excluded',
      suggested_assignment_mode: 'unknown',
      delegated_prefix_length: 62,
      ipv6_pool_usage: 'unused',
      suggested_ipv6_client_len: null,
    },
  ]
  return {
    router_id: item.router.id,
    routeros_version: '7.16.2',
    read_at: now.toISOString(),
    tls_fingerprint_sha256:
      'SHA256:7f3a9c1e2b4d5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e',
    items: raw.map((item) => {
      const same = existing.find((p) => p.prefix === item.prefix)
      const over = same ? undefined : overlapping(tenant, site, item.prefix)
      return {
        ...item,
        diff: same ? 'exists' : over ? 'overlaps' : 'new',
        existing_client_prefix_id: (same ?? over)?.id ?? null,
      }
    }),
  }
}

/** Propuestas del modo descubrimiento (I1-29): agregados de flujos fuera de prefijos. */
export function prefixProposals(tenant: MockTenant, site: MockSite): PrefixProposal[] {
  if (site.role === 'active' || prefixesOf(tenant, site).length) return []
  if (site.role === 'pending') return []
  const declared = (p: string) => !!overlapping(tenant, site, p)
  return (
    [
      {
        prefix: '100.64.12.0/22',
        distinct_ips: 212,
        bytes: '41200000000',
        reason: 'private_or_cgnat',
        suggested_role: 'customers',
      },
      {
        prefix: '10.30.0.0/23',
        distinct_ips: 96,
        bytes: '12800000000',
        reason: 'private_or_cgnat',
        suggested_role: 'customers',
      },
      {
        prefix: `2001:db8:${tenant.tenant_slug === 'fibra-norte' ? '4a8' : '5b8'}0::/44`,
        distinct_ips: 31,
        bytes: '3900000000',
        reason: 'isp_public_asn',
        suggested_role: 'customers',
      },
      {
        prefix: '10.30.255.0/28',
        distinct_ips: 6,
        bytes: '210000000',
        reason: 'private_or_cgnat',
        suggested_role: 'infrastructure',
      },
    ] satisfies PrefixProposal[]
  ).filter((p) => !declared(p.prefix))
}

export function toSite(tenant: MockTenant, site: MockSite): Site {
  return {
    ...stamp(tenant),
    updated_at: '2026-09-02T14:00:00.000Z',
    version: 1,
    id: site.id,
    name: site.name,
    code: site.name.replace('Nodo ', '').slice(0, 3).toUpperCase(),
    kind: 'node',
    timezone: 'America/Bogota',
    tags: [],
    address: null,
    discovery_mode: prefixesOf(tenant, site).filter((p) => p.role === 'customers').length === 0,
    primary_router_id: mockUuid('0192e333', tenantIndex(tenant) * 100 + site.index + 1),
    private_realm_id: privateRealm(tenant, site),
  }
}

// --- Clientes --------------------------------------------------------------------------------

/** Clientes del ISP (CustomerStats.total) y nuevos de hoy, como `customers_active`. */
export function customerCounts(tenant: MockTenant) {
  const r = rng(hash(tenant.tenant_id + 'customers'))
  const total = Math.round(800 * tenantScale(tenant) + r() * 200)
  return { total, newToday: Math.round(3 + r() * 9) }
}

/** Clientes con más consumo (índices en orden): comercios y algún residencial intenso. */
const TOP_INDICES = [5, 2, 17, 0, 23, 8, 31, 12, 44, 3, 27, 36]

export function isInactive(i: number) {
  return INACTIVE_INDICES.includes(i) || (i >= 50 && i % 37 === 0)
}

export function customerId(tenant: MockTenant, i: number) {
  return mockUuid('0193c000', tenantIndex(tenant) * 100_000 + i)
}

export function customerIndexFromId(tenant: MockTenant, id: string) {
  const prefix = mockUuid('0193c000', 0).slice(0, 24)
  if (!id.startsWith(prefix)) return -1
  const n = Number(id.slice(24))
  const i = n - tenantIndex(tenant) * 100_000
  return i >= 0 && i < customerCounts(tenant).total ? i : -1
}

/** Bajada 24 h (bytes) del cliente `i`; `null` si está inactivo. */
export function customerDown(tenant: MockTenant, i: number) {
  if (isInactive(i)) return null
  const rank = TOP_INDICES.indexOf(i)
  if (rank >= 0) return Math.round(9.5e11 / (rank + 1.4))
  return Math.round(1.5e9 + rng(hash(tenant.tenant_id + 'down' + i))() * 3.2e10)
}

export function customerUp(tenant: MockTenant, i: number) {
  const down = customerDown(tenant, i)
  if (down === null) return null
  const r = rng(hash(tenant.tenant_id + 'up' + i))()
  return Math.round(down * (isBaseCommercial(i) ? 0.55 : 0.06 + r * 0.1))
}

export function customerPrefixLen(i: number) {
  return isV6Customer(i) ? V6_CLIENT_LEN : null
}

/**
 * Cliente `i` con su estado actual: base determinista + cambios del usuario (tipo, alias,
 * notas, reinicio) + estado de seguridad derivado de los hallazgos activos.
 */
export function buildCustomer(
  tenant: MockTenant,
  i: number,
  now: Date,
  security?: Map<number, { open: number; infected: boolean }>,
): CustomerDetail {
  const t = now.getTime()
  const { total, newToday } = customerCounts(tenant)
  const site = customerSite(tenant, i)
  const mSite = sitesOf(tenant).find((s) => s.id === site.id)!
  const id = customerId(tenant, i)
  const inactive = isInactive(i)
  const isNew = i >= total - newToday
  const firstSeen = isNew ? t - ((i % 7) + 1) * 3_600_000 : t - (30 + ((i * 13) % 300)) * DAY
  const lastSeen = inactive
    ? t - (35 + (i % 20)) * DAY
    : i < 14
      ? t - 60_000
      : t - (((i * 17) % 55) + 2) * 60_000
  const commercial = isBaseCommercial(i)
  const scoring = i === 11
  const sec = (security ?? securityByCustomer(tenant, now)).get(i)
  const down = customerDown(tenant, i)
  const up = customerUp(tenant, i)
  const v6 = isV6Customer(i)
  const prefixes = prefixesOf(tenant, mSite)
  const prefix = prefixes.find((p) => {
    const ip = parseIp(customerAddress(tenant, i))
    const c = parseCidr(p.prefix)
    return (
      p.role === 'customers' &&
      ip &&
      c &&
      contains(c, { ...c, network: ip.value, length: ip.family === 4 ? 32 : 128 })
    )
  })
  const pppAlias = !commercial && i >= 20 && i <= 30 && i % 2 === 0
  const base: CustomerDetail = {
    id,
    tenant_id: tenant.tenant_id,
    version: 1,
    address: customerAddress(tenant, i),
    alias: baseAlias(i) ?? (pppAlias ? `ppp-${1000 + i}` : null),
    alias_source: commercial ? 'manual' : pppAlias ? 'routeros_ppp' : null,
    client_prefix_id: prefix?.id ?? null,
    commercial_use_suspected: false,
    first_seen: iso(firstSeen),
    last_seen: iso(lastSeen),
    kind: commercial || scoring ? 'commercial' : 'residential',
    kind_source: commercial ? 'manual' : scoring ? 'scoring' : 'default',
    kind_locked: commercial,
    kind_confidence: scoring ? 0.85 : null,
    kind_changed_at: commercial || scoring ? iso(firstSeen + 3 * DAY) : null,
    status: inactive ? 'inactive' : 'active',
    inactive_reason: inactive ? 'no_traffic' : null,
    open_findings: sec?.open ?? 0,
    security_state: sec ? (sec.infected ? 'infected' : 'suspected') : 'clean',
    realm_id: v6 ? publicRealm(tenant) : privateRealm(tenant, mSite),
    site_id: site.id,
    reset_at: null,
    traffic_24h:
      down === null
        ? null
        : {
            down_bytes: String(down),
            up_bytes: String(up),
            top_category: CATEGORIES[(i * 3) % 5]!,
          },
    notes: commercial ? 'Contacto: administración del local.' : null,
    suggested_kind: null,
    suggested_confidence: null,
    suggested_reasons: [],
  }
  const o = mockState.customers.get(id)
  if (!o) return base
  const merged: CustomerDetail = { ...base, version: o.version }
  for (const key of [
    'kind',
    'kind_source',
    'kind_locked',
    'kind_changed_at',
    'kind_confidence',
    'alias',
    'notes',
    'reset_at',
  ] as const) {
    if (o[key] !== undefined) (merged as Record<string, unknown>)[key] = o[key]
  }
  if (o.alias !== undefined) merged.alias_source = o.alias ? 'manual' : null
  return merged
}

/** Hallazgos activos y estado de seguridad por índice de cliente. */
export function securityByCustomer(tenant: MockTenant, now: Date) {
  const map = new Map<number, { open: number; infected: boolean }>()
  for (const f of openFindings(tenant, now)) {
    const entry = map.get(f.customer) ?? { open: 0, infected: false }
    entry.open++
    if (f.security_state === 'infected') entry.infected = true
    map.set(f.customer, entry)
  }
  return map
}

export function customersOf(tenant: MockTenant, now: Date): CustomerDetail[] {
  const { total } = customerCounts(tenant)
  const security = securityByCustomer(tenant, now)
  return Array.from({ length: total }, (_, i) => buildCustomer(tenant, i, now, security))
}

/** Historial de tipo (más reciente primero): cambios del usuario + historia base. */
export function kindHistory(tenant: MockTenant, i: number, now: Date): KindChange[] {
  const c = buildCustomer(tenant, i, now)
  const id = customerId(tenant, i)
  const firstSeen = new Date(c.first_seen).getTime()
  const base: KindChange[] = []
  if (isBaseCommercial(i) || i === 11) {
    base.push({
      id: mockUuid('0193d000', tenantIndex(tenant) * 100_000 + i * 10 + 2),
      changed_at: iso(firstSeen + 3 * DAY),
      from_kind: 'residential',
      to_kind: 'commercial',
      source: i === 11 ? 'scoring' : 'manual',
      actor_id: i === 11 ? null : '01926b3e-1111-7000-8000-000000000001',
      confidence: i === 11 ? 0.85 : null,
      manual_reason: i === 11 ? null : 'Local con TPV y horario comercial',
      model_ref: i === 11 ? 'kind-scoring@1' : null,
      reasons: [],
    })
  }
  base.push({
    id: mockUuid('0193d000', tenantIndex(tenant) * 100_000 + i * 10 + 1),
    changed_at: iso(firstSeen),
    from_kind: null,
    to_kind: 'residential',
    source: 'default',
    actor_id: null,
    confidence: null,
    manual_reason: null,
    model_ref: null,
    reasons: [],
  })
  return [...(mockState.kindHistory.get(id) ?? []), ...base]
}
