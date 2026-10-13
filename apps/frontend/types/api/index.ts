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

// Miembros, roles e invitaciones del ISP (I1).
export type Member = Schemas['Member']
export type Role = Schemas['Role']
export type RoleCreate = Schemas['RoleCreate']
export type RolePatch = Schemas['RolePatch']
export type PermissionCatalog = Schemas['PermissionCatalog']
export type CatalogPermission = PermissionCatalog['permissions'][number]
export type InvitationIssued = Schemas['InvitationIssued']
export type InvitationInspection =
  operations['authInspectInvitation']['responses'][200]['content']['application/json']
export type MemberPatch = Schemas['MemberPatch']
export type MePatch = Schemas['MePatch']
/** Enlace de restablecimiento de contraseña emitido por un administrador. */
export type PasswordResetIssued = Schemas['PasswordResetIssued']
export type PasswordResetInspection =
  operations['authInspectPasswordReset']['responses'][200]['content']['application/json']

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

// Inventario, clientes y prefijos (I1).
export type PageInfo = Schemas['PageInfo']
export type Site = Schemas['Site']
export type Router = Schemas['Router']
export type SiteInput = Schemas['SiteInput']
export type RouterInput = Schemas['RouterInput']
export type RouterPatch = Schemas['RouterPatch']
export type CredentialStatus = Schemas['CredentialStatus']
export type Peer = Schemas['Peer']
export type FlowExporter = Schemas['FlowExporter']
export type Customer = Schemas['Customer']
export type CustomerDetail = Schemas['CustomerDetail']
export type CustomerKind = Schemas['CustomerKind']
export type KindChange = Schemas['KindChange']
export type Reason = Schemas['Reason']
export type ClientPrefix = Schemas['ClientPrefix']
export type ClientPrefixInput = Schemas['ClientPrefixInput']
export type ClientPrefixRole = Schemas['ClientPrefixRole']
export type PrefixImportPreview = Schemas['PrefixImportPreview']
export type PrefixImportItem = PrefixImportPreview['items'][number]
export type PrefixProposal =
  operations['listPrefixProposals']['responses'][200]['content']['application/json']['data'][number]

// ASN propios del ISP (I1).
export type AsnRule = Schemas['AsnRule']
export type AsnRuleCatalog = Schemas['AsnRuleCatalog']
export type AsnInfo = Schemas['AsnInfo']

// Seguridad (I1, C8).
export type AllowlistEntry = Schemas['AllowlistEntry']
export type Evidence = Schemas['Evidence']
export type RecommendedAction = Schemas['RecommendedAction']
export type EvidenceFlow =
  operations['getFindingEvidence']['responses'][200]['content']['application/json']['data'][number]

// Analítica (I1).
export type AnalyticsMeta = Schemas['AnalyticsMeta']
export type SeriesResponse = Schemas['SeriesResponse']
export type TopResult = Schemas['TopResult']
export type TrafficAttribution =
  operations['getTrafficAttribution']['responses'][200]['content']['application/json']

// Kioscos y listas de reproducción (I1, C9).
export type Kiosk = Schemas['Kiosk']
export type KioskInput = Schemas['KioskInput']
export type KioskConfig =
  operations['getKioskConfig']['responses'][200]['content']['application/json']
export type KioskEnrollmentCode =
  operations['createKioskEnrollmentCode']['responses'][201]['content']['application/json']
export type Playlist = Schemas['playlist.schema']
export type PlaylistItem = Schemas['PlaylistItem']

// Canales de notificación (I1, D13/D17).
export type NotificationChannel = Schemas['NotificationChannel']
export type NotificationChannelInput = Schemas['NotificationChannelInput']
export type NotificationChannelKind = Schemas['NotificationChannelKind']
export type NotificationChannelCredentials = Schemas['NotificationChannelCredentials']
export type NotificationDelivery = Schemas['NotificationDelivery']
export type NotificationEventType = Schemas['NotificationEventType']
export type ConnectionTestResult = Schemas['ConnectionTestResult']
export type EmailChannelConfig = Schemas['EmailChannelConfig']
export type TelegramChannelConfig = Schemas['TelegramChannelConfig']
export type LibreNmsChannelConfig = Schemas['LibreNmsChannelConfig']

// Plataforma (I1, D19/D20).
export type InstallationAccess = Schemas['InstallationAccess']
export type WireguardHub = Schemas['WireguardHub']
export type WireguardPool = Schemas['WireguardPool']
export type WireguardHubUpdate =
  operations['platformUpdateWireguardHub']['requestBody']['content']['application/json']
export type TenantOverview = Schemas['TenantOverview']
export type Tenant = Schemas['Tenant']
export type TenantCreate = Schemas['TenantCreate']
/** `POST /platform/tenants` 201: el ISP y cómo quedó su primer administrador. */
export type TenantCreated =
  operations['platformCreateTenant']['responses'][201]['content']['application/json']
export type PlatformUser = Schemas['PlatformUser']
export type PlatformUserStatus = PlatformUser['status']
export type PlatformUserPatch = Schemas['PlatformUserPatch']
export type PlatformRoleKey =
  operations['platformSetUserRoles']['requestBody']['content']['application/json']['platform_roles'][number]
export type ReputationSource = Schemas['ReputationSource']
export type ReputationSourceCreate = Schemas['ReputationSourceCreate']
export type ReputationCategory = Schemas['ReputationCategory']
export type ReputationSourceFormat = Schemas['ReputationSourceFormat']
export type RemoteDestination = Schemas['RemoteDestination']
export type RemoteDestinationCreate = Schemas['RemoteDestinationCreate']
export type RemoteDestinationPatch = Schemas['RemoteDestinationPatch']
export type RemoteDestinationTestResult = Schemas['RemoteDestinationTestResult']

// Asistente de IA (docs/assist.md).
export type AssistStatus = Schemas['AssistStatus']
export type AssistSettings = Schemas['AssistSettings']
export type AssistSettingsUpdate = Schemas['AssistSettingsUpdate']
export type AssistProvider = Schemas['AssistProvider']
export type AssistEffort = Schemas['AssistEffort']
export type AssistFeature = Schemas['AssistFeature']
export type AssistTestResult = Schemas['AssistTestResult']
export type AssistAnalysis = Schemas['AssistAnalysis']
export type AssistAuditEntry = Schemas['AssistAuditEntry']
export type McpToken = Schemas['McpToken']
export type McpTokenIssued = Schemas['McpTokenIssued']
