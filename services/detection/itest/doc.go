// Package itest son las pruebas de integración de la capa de hallazgos de
// detection con PostgreSQL real (ciclo de vida, eventos por outbox, estado de
// seguridad y RLS). Las de los escenarios del simulador por el pipeline real
// están en tests/detection (importan collector e ingester, prohibido dentro
// de un módulo por la regla module-api-only):
//
//	go test -tags=integration ./services/detection/itest/...
package itest
