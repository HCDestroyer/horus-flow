/**
 * Forma de `data` por tipo de widget (C9, `widget-data.schema.json`).
 *
 * El contrato fija el sobre (`state` / `series` / `table` + `meta`) pero no las claves de
 * `values` ni las columnas de cada tipo: esta es la lectura del frontend, alineada con los
 * esquemas de origen (SecuritySummary, CustomerStats, FlowExporter, mensaje WS
 * `traffic.summary`). Pendiente de fijarlas en C9 (ver README › Pendientes).
 */
import type { FlowExporterState, SecurityState, Severity } from '~~/types/api'

/** `traffic_now` (state): mismas claves que el mensaje WS `traffic.summary` + comparación. */
export interface TrafficNowValues {
  down_bps: number | null
  up_bps: number | null
  down_bps_yesterday: number | null
  up_bps_yesterday: number | null
  flows_per_second: number | null
  /** Última hora a paso fijo; `null` = hueco (nunca cero). */
  sparkline: { step_seconds: number; down_bps: (number | null)[]; up_bps: (number | null)[] }
}

/** `customers_active` (state), de CustomerStats. */
export interface CustomersActiveValues {
  active: number
  new_today: number
  total: number
}

/** `findings_summary` (state), de SecuritySummary. */
export interface FindingsSummaryValues {
  open_total: number
  open_by_severity: Partial<Record<Severity, number>>
  new_last_24h: number
  affected_customers: number
  /** Clientes por estado de seguridad (D18: `infected` se rotula "Infectado"). */
  by_security_state: Partial<Record<SecurityState, number>>
}

export const BOTNET_SIGNALS = [
  'c2_contact',
  'beaconing',
  'fan_out',
  'scanning',
  'watched_ports',
  'sustained_upload',
  'smtp',
  'ddos',
] as const
export type BotnetSignal = (typeof BOTNET_SIGNALS)[number]

/** `botnet_signals` (state): clientes afectados por señal (SecuritySummary.by_signal). */
export interface BotnetSignalsValues {
  by_signal: Partial<Record<BotnetSignal, number>>
  affected_customers: number
}

/** `exporters_status` (table), de FlowExporter. */
export interface ExporterRow {
  router: string
  site: string
  state: FlowExporterState
  state_since: string
  last_flow_at: string | null
  flows_per_second: number | null
  loss_ratio: number | null
}

/** `top_categories` / `top_services` / `top_organizations` (table). */
export interface TopRow {
  label: string
  down_bytes: number
  up_bytes: number
}

/** `top_customers` (table). `customer_ip` es dato personal (enmascarado si `masked_personal_data`). */
export interface TopCustomerRow {
  customer_ip: string
  alias: string | null
  kind: 'residential' | 'commercial'
  site: string
  down_bytes: number
  up_bytes: number
}

/** `findings_feed` (table). */
export interface FindingFeedRow {
  id: string
  severity: Severity
  kind: string
  summary: string
  customer_ip: string
  alias: string | null
  site: string
  security_state: SecurityState
  /** 0–1. */
  confidence: number
  last_seen_at: string
}

/** `security_by_node` (table). */
export interface SecurityByNodeRow {
  site: string
  customers_with_signals: number
  open_findings: number
}

/** `watched_ports` (table). */
export interface WatchedPortRow {
  port: number
  protocol: 'tcp' | 'udp'
  service: string
  customers: number
  flows: number
}
