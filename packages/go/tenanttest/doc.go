// Package tenanttest es la batería obligatoria "A no ve B" (docs/security.md
// §3.9, docs/conventions.md §11, historia I0-08):
//
//   - [AssertRLS]: test de arquitectura — toda tabla con tenant_id tiene RLS
//     activado y forzado con la política p_tenant.
//   - [LoadOperations] y [Suite]: lee el bundle OpenAPI del contrato y, para
//     cada operación con `x-scope: tenant`, exige un caso de aislamiento
//     declarado; un endpoint nuevo sin caso hace fallar la suite.
package tenanttest
