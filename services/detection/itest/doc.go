// Package itest son las pruebas de integración del motor de detección con
// datos reales del pipeline de flujos: cada escenario del simulador
// (tools/flowsim/fixtures/sim) y la captura real anonimizada pasan por el
// collector y el ingester reales a ClickHouse (testcontainers o
// HORUS_CH_TEST_DSN), el motor los evalúa como el usuario horus_detection
// (row policies con SQL_horus_tenant) y los hallazgos quedan en PostgreSQL:
//
//	HORUS_CH_NOFILE=16384 go test -tags=integration ./services/detection/itest/...
package itest
