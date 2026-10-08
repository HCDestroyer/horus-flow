/**
 * Tipos de la API del gateway escritos a mano a partir de docs/api.md (§1.4, §2.1, §2.7).
 *
 * PROVISIONAL (I0-14): en I0-15 se sustituyen por los tipos generados desde el OpenAPI
 * (`openapi-typescript` → shared/api/schema.d.ts, conventions.md §3.5). Mantener los nombres
 * de campo en snake_case, como el contrato.
 */

/** Problem Details (RFC 9457) con las extensiones de docs/api.md §1.4. */
export interface ProblemDetails {
  type?: string
  title: string
  status: number
  detail?: string
  instance?: string
  /** Contrato estable (UPPER_SNAKE_CASE); el frontend traduce por `code`. */
  code: string
  trace_id?: string
  request_id?: string
  errors?: { field: string; code: string; message: string }[]
}

/** `POST /auth/login` sin 2FA o `POST /auth/mfa/verify`. */
export interface AccessTokenResponse {
  access_token: string
  expires_at: string
}

/** `POST /auth/login` cuando el usuario tiene 2FA. */
export interface MfaRequiredResponse {
  mfa_required: true
  mfa_token: string
}

export type LoginResponse = AccessTokenResponse | MfaRequiredResponse

export interface LoginRequest {
  email: string
  password: string
}

export interface MfaVerifyRequest {
  mfa_token: string
  code: string
}

/** Permisos efectivos con alcance: `{"devices.read": ["*"], "sites.read": ["site:018f…"]}`. */
export type PermissionsWithScope = Record<string, string[]>

export interface Membership {
  tenant_id: string
  tenant_slug: string
  tenant_name: string
  roles: string[]
  permissions_with_scope: PermissionsWithScope
}

/** `GET /me`. */
export interface Me {
  id: string
  email: string
  display_name: string
  locale: string
  time_zone: string
  mfa_enabled: boolean
  /** Roles de plataforma (`platform_admin`, `platform_operator`, `platform_auditor`). */
  platform_roles: string[]
  /** Permisos `platform.*` efectivos. */
  platform_permissions: string[]
  memberships: Membership[]
}

export type CapabilityState = 'ok' | 'degraded' | 'stale' | 'unavailable'

/** `GET /system/status` (vista resumida). */
export interface SystemStatus {
  status: 'ok' | 'degraded' | 'down'
  checked_at: string
  capabilities: Record<string, CapabilityState>
  components?: { name: string; status: string; since?: string; detail?: string }[]
}

export interface PageInfo {
  next_cursor: string | null
  prev_cursor: string | null
  has_more: boolean
  limit: number
}

export interface Paginated<T> {
  data: T[]
  page: PageInfo
}

/** Nodo (`site` con `kind = node`), docs/api.md §2.4. Solo los campos que usa I0. */
export interface Site {
  id: string
  tenant_id: string
  name: string
  kind: 'node' | 'site'
  created_at: string
}
