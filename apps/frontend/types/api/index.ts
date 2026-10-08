/**
 * Alias legibles de los tipos generados del contrato (I0-15). La fuente es
 * `schema.d.ts`, generado por `pnpm api:generate` desde
 * `packages/schemas/openapi/dist/horus-api.v0.yaml` (C5, que incluye los esquemas de C9).
 * Aquí solo se nombran; no se escribe ningún campo a mano.
 */
import type { components, operations, paths } from './schema'

export type { components, operations, paths }

type Schemas = components['schemas']

export type ProblemDetails = Schemas['Problem']
export type ErrorCode = Schemas['ErrorCode']
export type AccessTokenResponse = Schemas['AccessTokenResponse']
export type MfaChallenge = Schemas['MfaChallenge']
export type LoginResponse = AccessTokenResponse | MfaChallenge
export type LoginRequest = operations['authLogin']['requestBody']['content']['application/json']
export type MfaVerifyRequest =
  operations['authMfaVerify']['requestBody']['content']['application/json']
export type TokenRequest =
  operations['authIssueToken']['requestBody']['content']['application/json']

export type Me = Schemas['Me']
export type Membership = Schemas['Membership']
export type RoleAssignment = Schemas['RoleAssignment']
export type PermissionsWithScope = Membership['permissions_with_scope']

export type SystemStatus = Schemas['SystemStatus']
export type CapabilityState = Schemas['CapabilityState']

export type Severity = Schemas['Severity']
export type SecurityState = Schemas['SecurityState']
export type FindingKind = Schemas['FindingKind']
export type FindingState = Schemas['FindingState']
export type FlowExporterState = Schemas['FlowExporterState']
export type SecuritySummary = Schemas['SecuritySummary']
export type CustomerStats = Schemas['CustomerStats']
export type Finding = Schemas['finding.schema']

// Dashboards y widgets (C9).
export type Dashboard = Schemas['dashboard.schema']
export type DashboardSummary = Schemas['DashboardSummary']
export type DashboardWidget = Schemas['Widget']
export type WidgetPosition = Schemas['Position']
export type WidgetType = Schemas['widget-type.schema']
export type WidgetData = Schemas['widget-data.schema']
export type WidgetDataMeta = Schemas['Meta']
export type StateData = Schemas['StateData']
export type SeriesData = Schemas['SeriesData']
export type TableData = Schemas['TableData']
export type RelativeRange = Schemas['RelativeRange']
