// Package tenancy es la suite obligatoria de aislamiento entre ISP (historia
// I0-08, docs/security.md §3.9). Los tests llevan la etiqueta `integration`
// (PostgreSQL real con testcontainers o HORUS_TEST_POSTGRES_DSN):
//
//	go test -tags=integration ./tests/tenancy/...
package tenancy
