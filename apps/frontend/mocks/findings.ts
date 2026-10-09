import type { EvidenceFlow, Finding, RecommendedAction, SecuritySummary } from '~~/types/api'
import findingExample from '~~/types/api/contract/examples/finding-outbound-scanning.api.json'
import {
  customerAddress,
  customerSite,
  findingId,
  FINDING_TEMPLATES,
  maskIp,
  openFindings,
  SIGNALS_BY_KIND,
  type MockFinding,
} from './base'
import type { MockTenant } from './data'
import { buildCustomer, routersOf } from './inventory'
import { hash, iso, rng } from './random'
import { mockState } from './state'

/**
 * Hallazgos completos (C8) de la API simulada a partir de la base (`openFindings`): razones con
 * dato, evidencia agregada según el tipo y acciones recomendadas con comandos RouterOS y su
 * deshacer (D11; Horus nunca los ejecuta). Más unos cerrados (resueltos o falsos positivos)
 * para que la lista tenga todos los estados.
 */

type Kind = Finding['kind']

const ACTOR = '01926b3e-1111-7000-8000-000000000001'

function render(commands: string[], values: Record<string, string>) {
  return commands.map((c) => c.replace(/\{\{(\w+)\}\}/g, (_, k: string) => values[k] ?? `{{${k}}}`))
}

function routerAction(
  base: Omit<RecommendedAction, 'routeros' | 'execution'>,
  commands: string[],
  undo: string[],
  placeholders: NonNullable<RecommendedAction['routeros']>['placeholders'],
  values: Record<string, string> | null,
  notes: string | null = null,
): RecommendedAction {
  return {
    ...base,
    execution: 'manual',
    routeros: {
      min_version: '7.12',
      commands,
      undo_commands: undo,
      placeholders,
      rendered_commands: values ? render(commands, values) : null,
      rendered_undo_commands: values ? render(undo, values) : null,
      notes,
    },
  }
}

const contact = (message: string, explanation: string): RecommendedAction => ({
  code: 'contact_customer',
  title: 'Avisar al cliente',
  explanation,
  priority: 1,
  risk: 'low',
  audience: 'customer_support',
  execution: 'manual',
  customer_message: message,
  routeros: null,
})

const quarantine = (values: Record<string, string> | null, priority = 3) =>
  routerAction(
    {
      code: 'quarantine_address_list',
      title: 'Aislar la IP en la address-list de cuarentena',
      explanation:
        'Si el cliente no responde y la actividad continúa, añadir la IP a una address-list que su política de cuarentena ya trate (walled garden). Afecta a todo el servicio del cliente.',
      priority,
      risk: 'high',
      audience: 'network_engineer',
      customer_message: null,
    },
    [
      '/ip firewall address-list add list=horus-quarantine address={{customer_address}} timeout=1d comment="horus finding {{finding_id}}"',
    ],
    [
      '/ip firewall address-list remove [find list=horus-quarantine comment="horus finding {{finding_id}}"]',
    ],
    ['customer_address', 'finding_id'],
    values,
    'La address-list no hace nada por sí sola: requiere una regla de cuarentena existente en su router.',
  )

interface KindDetail {
  reasons: Finding['reasons']
  evidence: Finding['evidence']
  target: Finding['primary_target']
  actions: (values: Record<string, string> | null) => RecommendedAction[]
  rule: string
}

function detailFor(kind: Kind, summary: string, seq: number): KindDetail {
  const r = rng(hash(`${kind}${seq}`))
  const port = Number(summary.match(/puerto (\d+)/)?.[1] ?? 23)
  switch (kind) {
    case 'outbound_scanning': {
      const destinations =
        Number(summary.match(/a ([\d\s ]+) destinos/)?.[1]?.replace(/\D/g, '')) || 1240
      return {
        rule: 'outbound-scan@2',
        target: { type: 'remote_port', value: String(port) },
        reasons: [
          {
            code: 'syn_only_ratio_high',
            detail: `${Math.round(90 + r() * 8)} % de los flujos salientes fueron SYN sin respuesta`,
            weight: 0.45,
            data: { syn_ratio: 0.96 },
          },
          {
            code: 'watched_port_fanout',
            detail: `Puerto ${port} hacia ${destinations.toLocaleString('es-ES')} destinos en ${Math.round(destinations * 0.95).toLocaleString('es-ES')} redes /24`,
            weight: 0.41,
            data: { port, destinations },
          },
        ],
        evidence: {
          ...findingExample.evidence,
          distinct_destinations: destinations,
          distinct_nets24: Math.round(destinations * 0.95),
          destination_ports: port === 23 ? [23, 2323] : [port],
        },
        actions: (values) => [
          ...(findingExample.recommended_actions as RecommendedAction[]).map((a) => ({
            ...a,
            title:
              a.code === 'block_outbound_port'
                ? `Bloquear el puerto ${port} saliente de esta IP`
                : a.title,
            routeros: a.routeros
              ? {
                  ...a.routeros,
                  commands: a.routeros.commands.map((c) =>
                    c.replace('23,2323', String(port === 23 ? '23,2323' : port)),
                  ),
                  rendered_commands: values
                    ? render(
                        a.routeros.commands.map((c) =>
                          c.replace('23,2323', String(port === 23 ? '23,2323' : port)),
                        ),
                        values,
                      )
                    : null,
                  rendered_undo_commands: values ? render(a.routeros.undo_commands, values) : null,
                }
              : null,
          })),
        ],
      }
    }
    case 'botnet_c2_communication':
    case 'reputation_hit': {
      const c2 = kind === 'botnet_c2_communication'
      const remote = c2 ? '185.220.101.47' : '45.95.168.12'
      return {
        rule: c2 ? 'c2-contact@4' : 'reputation-hit@1',
        target: { type: 'remote_ip', value: remote },
        reasons: [
          {
            code: 'reputation_listed',
            detail: c2
              ? `${remote} figura en Feodo Tracker (abuse.ch) como servidor de control de botnet desde el 02/10`
              : `${remote} figura en Spamhaus DROP (red secuestrada o de uso malicioso)`,
            weight: 0.6,
            data: { indicator: remote, source: c2 ? 'abuse_ch_feodo' : 'spamhaus_drop' },
          },
          {
            code: c2 ? 'responded_connections' : 'connections',
            detail: c2
              ? `${Math.round(30 + r() * 40)} conexiones con respuesta (no solo SYN) en las últimas 6 h`
              : `${Math.round(3 + r() * 6)} conexiones en 24 h, sin patrón periódico`,
            weight: 0.34,
            data: { responded: c2 },
          },
        ],
        evidence: {
          flows: Math.round(40 + r() * 80),
          bytes_est: String(Math.round(180_000 + r() * 400_000)),
          destination_asns: [c2 ? 60729 : 204428],
          reputation_sources: [
            {
              source: c2 ? 'abuse_ch_feodo' : 'spamhaus_drop',
              indicator: remote,
              category: c2 ? 'botnet_cc' : 'blocklist',
              listed_at: '2026-10-02T08:00:00.000Z',
              responded: c2,
            },
          ],
        },
        actions: (values) => [
          contact(
            'Detectamos desde su conexión comunicaciones con un servidor de control de botnet. Le recomendamos revisar sus equipos (cámaras, DVR, router) y pasar un antivirus.',
            'Las comunicaciones con un C2 indican un equipo comprometido. El cliente debe revisar sus dispositivos; sin su ayuda el equipo seguirá infectado.',
          ),
          routerAction(
            {
              code: 'block_destination',
              title: `Bloquear el destino ${remote}`,
              explanation:
                'Corta la comunicación con el servidor de control para toda la red sin afectar al resto del servicio del cliente. Se deshace borrando la regla con el comentario del hallazgo.',
              priority: 2,
              risk: 'low',
              audience: 'network_engineer',
              customer_message: null,
            },
            [
              '/ip firewall filter add chain=forward dst-address={{remote_ip}} action=drop comment="horus finding {{finding_id}}" place-before=0',
            ],
            ['/ip firewall filter remove [find comment="horus finding {{finding_id}}"]'],
            ['remote_ip', 'finding_id'],
            values ? { ...values, remote_ip: remote } : null,
          ),
          ...(c2 ? [quarantine(values)] : []),
        ],
      }
    }
    case 'spam_smtp_outbound': {
      const servers = Number(summary.match(/a (\d+) servidores/)?.[1] ?? 86)
      return {
        rule: 'smtp-direct@1',
        target: { type: 'remote_port', value: '25' },
        reasons: [
          {
            code: 'smtp_servers_per_hour',
            detail: `SMTP directo (puerto 25) a ${servers} servidores de correo distintos en 1 h`,
            weight: 0.5,
            data: { smtp_servers_per_hour: servers },
          },
          {
            code: 'residential_no_mail_server',
            detail: 'Cliente residencial sin servidor de correo propio declarado',
            weight: 0.3,
            data: null,
          },
        ],
        evidence: {
          smtp_servers_per_hour: servers,
          flows: servers * 3,
          destination_ports: [25],
          destination_sample: ['203.0.113.25', '198.51.100.140', '192.0.2.77'],
        },
        actions: (values) => [
          contact(
            'Detectamos envío masivo de correo directo desde su conexión, típico de un equipo infectado. Le recomendamos revisar sus equipos.',
            'Un equipo que envía correo directo a decenas de servidores suele estar infectado con un bot de spam; además puede llevar a que la IP del ISP acabe en listas negras.',
          ),
          routerAction(
            {
              code: 'block_smtp_outbound',
              title: 'Bloquear SMTP saliente (puerto 25) de esta IP',
              explanation:
                'Los clientes residenciales envían correo por el puerto 587 de su proveedor; bloquear el 25 frena el spam sin afectar al correo legítimo.',
              priority: 2,
              risk: 'low',
              audience: 'network_engineer',
              customer_message: null,
            },
            [
              '/ip firewall filter add chain=forward src-address={{customer_address}} protocol=tcp dst-port=25 action=drop comment="horus finding {{finding_id}}" place-before=0',
            ],
            ['/ip firewall filter remove [find comment="horus finding {{finding_id}}"]'],
            ['customer_address', 'finding_id'],
            values,
          ),
        ],
      }
    }
    case 'beaconing': {
      const interval = Number(summary.match(/cada (\d+) s/)?.[1] ?? 60)
      return {
        rule: 'beaconing@1',
        target: { type: 'remote_ip', value: '203.0.113.88' },
        reasons: [
          {
            code: 'periodic_interval',
            detail: `Conexiones cada ${interval} s (coeficiente de variación 0,04) durante 9 h`,
            weight: 0.48,
            data: { interval_seconds: interval, interval_cv: 0.04 },
          },
          {
            code: 'small_constant_payload',
            detail: 'Cada conexión transfiere entre 300 y 420 bytes',
            weight: 0.28,
            data: null,
          },
        ],
        evidence: {
          interval_seconds: interval,
          interval_cv: 0.04,
          duration_seconds: 9 * 3600,
          flows: Math.round((9 * 3600) / interval),
        },
        actions: (values) => [
          {
            code: 'monitor',
            title: 'Vigilar sin actuar',
            explanation:
              'La periodicidad sola no prueba una infección (hay actualizadores y telemetría legítimos). Esperar a que aparezca otra señal o confirmarlo con el cliente.',
            priority: 1,
            risk: 'low',
            audience: 'noc',
            execution: 'manual',
            customer_message: null,
            routeros: null,
          },
          routerAction(
            {
              code: 'block_destination',
              title: 'Bloquear el destino periódico',
              explanation:
                'Solo si se confirma que el destino no es legítimo. Se deshace borrando la regla con el comentario del hallazgo.',
              priority: 2,
              risk: 'medium',
              audience: 'network_engineer',
              customer_message: null,
            },
            [
              '/ip firewall filter add chain=forward src-address={{customer_address}} dst-address={{remote_ip}} action=drop comment="horus finding {{finding_id}}" place-before=0',
            ],
            ['/ip firewall filter remove [find comment="horus finding {{finding_id}}"]'],
            ['customer_address', 'remote_ip', 'finding_id'],
            values ? { ...values, remote_ip: '203.0.113.88' } : null,
          ),
        ],
      }
    }
    default: {
      // ddos_participation y otros.
      return {
        rule: 'ddos-burst@1',
        target: { type: 'remote_prefix', value: '198.51.100.0/24' },
        reasons: [
          {
            code: 'burst_to_few_targets',
            detail: 'Ráfaga de 48 000 pps UDP/123 hacia 2 destinos durante 4 min',
            weight: 0.52,
            data: { pps_peak: 48_000, protocol: 'udp' },
          },
          {
            code: 'baseline_deviation',
            detail: 'Subida 37 veces por encima de la línea base del cliente',
            weight: 0.33,
            data: null,
          },
        ],
        evidence: {
          pps_peak: 48_000,
          bps_peak: 310_000_000,
          protocol: 'udp',
          duration_seconds: 240,
          target_prefix: '198.51.100.0/24',
          destination_ports: [123],
        },
        actions: (values) => [
          routerAction(
            {
              code: 'rate_limit_customer',
              title: 'Limitar la subida del cliente',
              explanation:
                'Reduce el impacto del ataque en la red sin cortar el servicio. Se deshace borrando la cola con el comentario del hallazgo.',
              priority: 1,
              risk: 'medium',
              audience: 'network_engineer',
              customer_message: null,
            },
            [
              '/queue simple add name="horus-{{finding_id}}" target={{customer_address}} max-limit=2M/100M comment="horus finding {{finding_id}}"',
            ],
            ['/queue simple remove [find comment="horus finding {{finding_id}}"]'],
            ['customer_address', 'finding_id', 'rate_limit'],
            values,
          ),
          quarantine(values, 2),
        ],
      }
    }
  }
}

const CONFIDENCE_LEVEL = (c: number): Finding['confidence_level'] =>
  c >= 0.8 ? 'high' : c >= 0.6 ? 'medium' : 'low'

/** Estado inicial de la base: algunos ya reconocidos por alguien del equipo. */
function baseState(seq: number): Finding['state'] {
  return seq % 6 === 4 ? 'acknowledged' : 'open'
}

export function toFinding(
  tenant: MockTenant,
  f: MockFinding,
  now: Date,
  options: { canSeePersonalData: boolean; closed?: Finding['resolution'] },
): Finding {
  const t = now.getTime()
  const customer = buildCustomer(tenant, f.customer, now)
  const detail = detailFor(f.kind, f.summary, f.seq)
  const values = options.canSeePersonalData
    ? { customer_address: customer.address, finding_id: f.id, rate_limit: '2M/100M' }
    : null
  const routers = routersOf(tenant, now)
  const site = customerSite(tenant, f.customer)
  const opened = new Date(f.opened_at).getTime()
  const last = new Date(f.last_seen_at).getTime()
  const initial = options.closed
    ? options.closed.verdict === 'false_positive'
      ? 'false_positive'
      : 'resolved'
    : baseState(f.seq)
  const o = mockState.findings.get(f.id)
  const acknowledged = initial === 'acknowledged'
  const finding: Finding = {
    id: f.id,
    tenant_id: tenant.tenant_id,
    version: 3,
    state: initial,
    kind: f.kind,
    category: 'security',
    severity: f.severity as Finding['severity'],
    confidence: f.confidence,
    confidence_level: CONFIDENCE_LEVEL(f.confidence),
    subject_type: 'customer',
    customer_id: customer.id,
    realm_id: customer.realm_id,
    site_id: site.id,
    router_id: routers.find((r) => r.site.id === site.id)!.router.id,
    customer: {
      address: options.canSeePersonalData ? customer.address : maskIp(customer.address),
      address_masked: !options.canSeePersonalData,
      alias: customer.alias,
      kind: customer.kind,
    },
    summary: { code: f.kind, text: f.summary },
    window_from: iso(last - 5 * 60_000),
    window_to: iso(last),
    first_seen_at: f.opened_at,
    last_seen_at: f.last_seen_at,
    opened_at: f.opened_at,
    updated_at: f.last_seen_at,
    occurrences: 1 + Math.round((last - opened) / 3_600_000 / 3),
    reasons: detail.reasons,
    evidence: detail.evidence,
    primary_target: detail.target,
    rule_version: detail.rule,
    reputation_snapshot_version: 233,
    min_sampling_rate: 1,
    sampling_reduced_confidence: false,
    previous_finding_id: null,
    acknowledged_by: acknowledged ? ACTOR : null,
    acknowledged_at: acknowledged ? iso(Math.min(t, opened + 40 * 60_000)) : null,
    resolution: options.closed ?? null,
    recommended_actions: detail.actions(values),
  }
  if (!o) return finding
  return {
    ...finding,
    version: o.version,
    state: o.state,
    updated_at: o.updated_at,
    acknowledged_at: o.acknowledged_at !== undefined ? o.acknowledged_at : finding.acknowledged_at,
    acknowledged_by: o.acknowledged_by !== undefined ? o.acknowledged_by : finding.acknowledged_by,
    resolution: o.resolution !== undefined ? o.resolution : finding.resolution,
  }
}

/** Hallazgos ya cerrados (fuera de la base de abiertos): resueltos y falsos positivos. */
function closedFindings(
  tenant: MockTenant,
  now: Date,
): { f: MockFinding; res: NonNullable<Finding['resolution']> }[] {
  const t = now.getTime()
  return [0, 1, 2, 3, 4, 5].map((k) => {
    const template = FINDING_TEMPLATES[(k * 2 + 1) % FINDING_TEMPLATES.length]!
    const customer = 15 + k * 4
    const last = t - (3 + k * 2) * 86_400_000
    const fp = k % 3 === 2
    return {
      f: {
        id: findingId(70 + k),
        seq: 70 + k,
        ...template,
        customer,
        customer_ip: customerAddress(tenant, customer),
        alias: null,
        site: customerSite(tenant, customer).name,
        security_state: 'suspected',
        confidence: 0.62,
        opened_at: iso(last - 6 * 3_600_000),
        last_seen_at: iso(last),
      },
      res: {
        verdict: fp ? 'false_positive' : 'resolved',
        resolved_at: iso(last + 3_600_000),
        resolved_by: ACTOR,
        comment: fp
          ? 'Es el servidor de copias del cliente: tráfico esperado.'
          : 'El cliente cambió la contraseña de su cámara.',
        actions_taken: fp ? [] : ['contact_customer'],
        silence_until: fp ? iso(last + 31 * 86_400_000) : null,
      },
    }
  })
}

/** Todos los hallazgos del ISP (activos de la base + cerrados), con los cambios del usuario. */
export function findingsOf(tenant: MockTenant, now: Date, canSeePersonalData: boolean): Finding[] {
  const active = openFindings(tenant, now).map((f) =>
    toFinding(tenant, f, now, { canSeePersonalData }),
  )
  // Los que el usuario cerró salen de `openFindings`: se reconstruyen con su cambio.
  const closedByUser = [...mockState.findings.entries()]
    .filter(([, o]) => o.state === 'resolved' || o.state === 'false_positive')
    .map(([id]) => id)
  const all = openFindingsIncludingClosed(tenant, now).filter((f) => closedByUser.includes(f.id))
  const userClosed = all.map((f) => toFinding(tenant, f, now, { canSeePersonalData }))
  const closed = closedFindings(tenant, now).map(({ f, res }) =>
    toFinding(tenant, f, now, { canSeePersonalData, closed: res }),
  )
  return [...active, ...userClosed, ...closed]
}

/** La base sin filtrar los cerrados por el usuario (para reconstruirlos). */
function openFindingsIncludingClosed(tenant: MockTenant, now: Date) {
  const saved = new Map(mockState.findings)
  mockState.findings.clear()
  try {
    return openFindings(tenant, now)
  } finally {
    for (const [k, v] of saved) mockState.findings.set(k, v)
  }
}

export function findFinding(
  tenant: MockTenant,
  id: string,
  now: Date,
  canSeePersonalData: boolean,
) {
  return findingsOf(tenant, now, canSeePersonalData).find((f) => f.id === id)
}

const SEVERITY_RANK: Record<string, number> = { critical: 4, high: 3, medium: 2, low: 1, info: 0 }

export function sortFindings(list: Finding[]) {
  return [...list].sort(
    (a, b) =>
      (SEVERITY_RANK[b.severity] ?? 0) - (SEVERITY_RANK[a.severity] ?? 0) ||
      b.last_seen_at.localeCompare(a.last_seen_at),
  )
}

/** `GET /security/summary`, de la misma base que los widgets. */
export function securitySummary(tenant: MockTenant, now: Date): SecuritySummary {
  const findings = openFindings(tenant, now)
  const customers = (list: MockFinding[]) => new Set(list.map((f) => f.customer)).size
  const bySignal: Record<string, Set<number>> = {}
  for (const f of findings) {
    for (const s of SIGNALS_BY_KIND[f.kind] ?? []) (bySignal[s] ??= new Set()).add(f.customer)
  }
  const dayAgo = now.getTime() - 86_400_000
  const byKind: Record<string, number> = {}
  for (const f of findings) byKind[f.kind] = (byKind[f.kind] ?? 0) + 1
  return {
    generated_at: now.toISOString(),
    affected_customers: customers(findings),
    new_last_24h: findings.filter((f) => new Date(f.opened_at).getTime() >= dayAgo).length,
    open_by_severity: Object.fromEntries(
      ['critical', 'high', 'medium', 'low'].map((s) => [
        s,
        findings.filter((f) => f.severity === s).length,
      ]),
    ),
    by_kind: byKind,
    by_security_state: {
      infected: customers(findings.filter((f) => f.security_state === 'infected')),
      suspected: customers(findings.filter((f) => f.security_state === 'suspected')),
    },
    by_signal: Object.fromEntries(Object.entries(bySignal).map(([k, v]) => [k, v.size])),
    by_site: tenant.sites.map((s) => {
      const here = findings.filter((f) => f.site === s.name)
      return { site_id: s.id, open_findings: here.length, customers_with_signals: customers(here) }
    }),
  }
}

/** Flujos de evidencia (`security.evidence.read`): sin payloads, solo metadatos. */
export function evidenceFlows(finding: Finding): EvidenceFlow[] {
  const r = rng(hash(finding.id))
  const last = new Date(finding.last_seen_at).getTime()
  const sample = finding.evidence.destination_sample ?? [
    '198.51.100.7',
    '203.0.113.19',
    '192.0.2.200',
  ]
  const ports = finding.evidence.destination_ports ?? [Number(finding.primary_target.value) || 443]
  const remoteFixed =
    finding.primary_target.type === 'remote_ip' ? finding.primary_target.value : null
  return Array.from({ length: 24 }, (_, i) => ({
    ts: iso(last - i * 11_000 - Math.round(r() * 5000)),
    remote_ip:
      remoteFixed ??
      `${sample[i % sample.length]!.split('.').slice(0, 3).join('.')}.${Math.floor(r() * 250) + 2}`,
    remote_port: ports[i % ports.length]!,
    protocol: finding.evidence.protocol === 'udp' ? 17 : 6,
    direction: 'upload',
    bytes: String(60 + Math.round(r() * 300)),
    packets: String(1 + Math.round(r() * 3)),
    tcp_flags: finding.evidence.protocol === 'udp' ? null : 2,
    remote_asn: 64500 + Math.floor(r() * 30),
    reputation_category: finding.evidence.reputation_sources?.[0]?.category ?? null,
  }))
}
