// Package flows son las pruebas de extremo a extremo del dominio flows
// (I1-03…I1-07): una captura pasa por el collector (motor real, NATS
// JetStream en proceso) y el ingester (módulo completo, ClickHouse real con
// testcontainers o HORUS_CH_TEST_DSN) y se comprueba flows.flows_raw.
//
// Incluye el test dorado obligatorio con la captura real anonimizada de un
// MikroTik con NAT en el router principal
// (tests/fixtures/mikrotik-real/ipfix-nat-20s.pcapng):
//
//	HORUS_CH_NOFILE=16384 go test -tags=integration ./tests/flows/...
//	make test-flows-golden
package flows
