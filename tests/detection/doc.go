// Package detection son las pruebas de integración del motor de detección
// (I1-10, I1-11, I1-30) con datos del pipeline real: cada escenario del
// simulador (tools/flowsim/fixtures/sim) y la captura real anonimizada pasan
// por el collector y el ingester reales a ClickHouse (testcontainers o
// HORUS_CH_TEST_DSN); el módulo detection (Register + Start) los evalúa como
// el usuario horus_detection (row policies con SQL_horus_tenant) y los
// hallazgos quedan en PostgreSQL:
//
//	HORUS_CH_NOFILE=16384 go test -tags=integration ./tests/detection/...
package detection
