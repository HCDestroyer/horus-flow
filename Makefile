# Horus Flow — punto de entrada único para desarrollo, pruebas y laboratorio.
# Los objetivos se autodocumentan: cada uno lleva un comentario `## descripción`.
# Ejecuta `make help` (o solo `make`) para ver la lista.
#
# Los objetivos que aún no tienen implementación imprimen la historia que los
# entregará ("pendiente: I0-XX") y salen con código 0.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

GO            ?= go
GOLANGCI_LINT ?= golangci-lint
GO_PACKAGES   := $(shell $(GO) list ./... 2>/dev/null)
VERSION       ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS       := -X main.version=$(VERSION)

# Compose de desarrollo (I0-02). Variables en deployments/compose/.env; perfiles opcionales
# con PROFILE o COMPOSE_PROFILES (p. ej. `make up PROFILE=app,observability`).
# compose.observability.yaml (I0-18) se fusiona siempre, pero sus servicios solo arrancan con el
# perfil `observability`.
COMPOSE_DIR          := deployments/compose
HORUS_COMPOSE_FILE   := $(COMPOSE_DIR)/compose.dev.yaml
HORUS_OBS_FILE       := $(COMPOSE_DIR)/compose.observability.yaml
COMPOSE              ?= docker compose --project-directory $(COMPOSE_DIR) -f $(HORUS_COMPOSE_FILE) -f $(HORUS_OBS_FILE)
ifneq ($(PROFILE),)
export COMPOSE_PROFILES := $(PROFILE)
endif
COMPOSE_WAIT_TIMEOUT ?= 120
# down/reset funcionan aunque aún no exista .env (toman los valores de .env.example).
COMPOSE_DOWN         := $(COMPOSE) $(if $(wildcard $(COMPOSE_DIR)/.env),,--env-file $(COMPOSE_DIR)/.env.example)
export COMPOSE

define pending
	@echo "pendiente: $(1) — '$@' aún no está implementado."
endef

##@ Ayuda

.PHONY: help
help: ## Muestra esta ayuda
	@awk 'BEGIN {FS = ":.*## "; printf "Uso: make \033[36m<objetivo>\033[0m\n"} \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } \
		/^[a-zA-Z0-9_.-]+:.*## / { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

##@ Entorno de desarrollo

.PHONY: up
up: ## Levanta el compose de desarrollo (perfil mínimo) y espera a que esté healthy
	@bash scripts/compose-preflight.sh $(HORUS_COMPOSE_FILE) $(HORUS_OBS_FILE)
	$(COMPOSE) up -d --wait --wait-timeout $(COMPOSE_WAIT_TIMEOUT)
	@$(COMPOSE) ps --format 'table {{.Service}}\t{{.Status}}\t{{.Ports}}'

.PHONY: down
down: ## Detiene el compose de desarrollo (conserva los datos)
	$(COMPOSE_DOWN) --profile "*" down --remove-orphans

.PHONY: reset
reset: ## Detiene el compose y borra sus volúmenes (datos de PostgreSQL, ClickHouse...)
	$(COMPOSE_DOWN) --profile "*" down --volumes --remove-orphans

.PHONY: migrate-ch
migrate-ch: ## Aplica el esquema ClickHouse (I0-13) al ClickHouse del compose (idempotente)
	@test -f $(COMPOSE_DIR)/.env || { echo "migrate-ch: falta $(COMPOSE_DIR)/.env (ejecuta 'make up')"; exit 1; }
	@set -a; . ./$(COMPOSE_DIR)/.env; set +a; \
	HORUS_CLICKHOUSE_DSN="$${HORUS_CLICKHOUSE_DSN:-clickhouse://$${HORUS_CH_USER}@127.0.0.1:$${HORUS_CH_NATIVE_PORT:-9000}/$${HORUS_CH_DB}}" \
	HORUS_CLICKHOUSE_PASSWORD_FILE="$${HORUS_CLICKHOUSE_PASSWORD_FILE:-$(COMPOSE_DIR)/secrets/clickhouse_password.txt}" \
	$(GO) run ./services/ingester/cmd/ch-migrate

.PHONY: observability-smoke
observability-smoke: ## Levanta el perfil observability y comprueba Prometheus, Loki y Grafana (I0-18)
	@bash scripts/ci/observability-smoke.sh

##@ Calidad

.PHONY: build
build: ## Compila el binario horus en ./bin/horus
	@mkdir -p bin
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/horus ./services/cmd/horus

.PHONY: test
test: ## Ejecuta los tests de Go (go test ./...)
	@if [ -z "$(GO_PACKAGES)" ]; then echo "test: no hay paquetes Go todavía"; exit 0; fi
	$(GO) test -race -shuffle=on ./...

.PHONY: lint
lint: check-codeowners ## Ejecuta golangci-lint y la verificación de CODEOWNERS
	@if [ -z "$(GO_PACKAGES)" ]; then echo "lint: no hay paquetes Go todavía"; exit 0; fi
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { \
		echo "lint: falta '$(GOLANGCI_LINT)' (https://golangci-lint.run/welcome/install/)"; exit 1; }
	$(GOLANGCI_LINT) run ./...

.PHONY: vet
vet: ## Ejecuta go vet ./...
	$(GO) vet ./...

.PHONY: contracts-check
contracts-check: ## Verifica los contratos v0 (OpenAPI, eventos, Protobuf, esquemas, DDL)
	@command -v $${BUF_BIN:-buf} >/dev/null 2>&1 || { \
		echo "contracts-check: falta 'buf' (go install github.com/bufbuild/buf/cmd/buf@v1.47.2)"; exit 1; }
	npm ci --prefix packages/schemas --no-audit --no-fund
	npm --prefix packages/schemas run check:db

.PHONY: check-codeowners
check-codeowners: ## Verifica que toda carpeta de primer nivel y cada módulo tiene dueño
	@bash scripts/check-codeowners.sh

##@ Datos de flujo simulados y laboratorio

# Simulador de flujos (tools/flowsim, I0-10). Variables:
#   SCENARIO (normal) SEED (1) PROTO (ipfix|v9) RATE DURATION NAT IPV6 SPEED (1)
#   SIM_PROFILE (routeros7: plantillas 258/259 reales y NAT real | legacy) NAT_IPS (a,b,c)
#   SIM_TARGET (127.0.0.1:4739 o :2055) SIM_OUT (fichero .hfsim/.pcap/.pcapng[.gz] en vez de UDP)
#   SIM_EXPECTED (bin/sim/<escenario>-<proto>.expected.json) SIM_ARGS (flags extra)
#   sim-verify: sin variables = prueba sin router de todos los escenarios + fixtures
#   + captura real anonimizada (SIM_REAL_FIXTURES, I0-12);
#   SIM_IN=fichero o SIM_LISTEN=host:puerto verifican contra SIM_EXPECTED.
SCENARIO     ?= normal
SEED         ?= 1
PROTO        ?= ipfix
SPEED        ?= 1
SIM_DIR      ?= bin/sim
SIM_EXPECTED ?= $(SIM_DIR)/$(SCENARIO)-$(PROTO).expected.json
SIM_FIXTURES ?= tools/flowsim/fixtures/sim
SIM_REAL_FIXTURES ?= tests/fixtures/mikrotik-real

.PHONY: sim
sim: ## Genera flujos IPFIX/NetFlow v9 simulados (make sim SCENARIO=scan PROTO=v9 RATE=2000)
	@mkdir -p $(SIM_DIR) $(dir $(SIM_EXPECTED))
	$(GO) run ./tools/flowsim/cmd/flowsim -scenario $(SCENARIO) -seed $(SEED) -proto $(PROTO) \
		$(if $(RATE),-rate $(RATE)) $(if $(DURATION),-duration $(DURATION)) \
		$(if $(NAT),-nat=$(NAT)) $(if $(IPV6),-ipv6=$(IPV6)) \
		$(if $(SIM_PROFILE),-profile $(SIM_PROFILE)) $(if $(NAT_IPS),-nat-ips $(NAT_IPS)) \
		$(if $(SIM_OUT),-out $(SIM_OUT),$(if $(SIM_TARGET),-target $(SIM_TARGET)) -speed $(SPEED)) \
		-expected $(SIM_EXPECTED) $(SIM_ARGS)

.PHONY: sim-verify
sim-verify: ## Decodifica y verifica flujos simulados (sin variables: prueba sin router para CI)
ifneq ($(SIM_LISTEN),)
	$(GO) run ./tools/flowsim/cmd/sim-verify -listen $(SIM_LISTEN) -expected $(SIM_EXPECTED) $(SIM_ARGS)
else ifneq ($(SIM_IN),)
	$(GO) run ./tools/flowsim/cmd/sim-verify -in $(SIM_IN) -expected $(SIM_EXPECTED) $(SIM_ARGS)
else
	$(GO) run ./tools/flowsim/cmd/sim-verify -selftest -fixtures $(SIM_FIXTURES) -real-fixtures $(SIM_REAL_FIXTURES) $(SIM_ARGS)
endif

# Laboratorio MikroTik CHR (I0-11, infrastructure/lab/chr/README.md). Necesita Linux, sudo y
# /dev/kvm. Variables: ROS (7.12), LAB_ACCEL (kvm|tcg), PROFILE/CLIENT/DURATION en lab-traffic;
# el resto en infrastructure/lab/chr/lab.env.
LAB_SH := bash scripts/lab/lab.sh

.PHONY: lab-up
lab-up: ## Levanta el laboratorio MikroTik CHR (make lab-up ROS=7.12)
	$(LAB_SH) up

.PHONY: lab-down
lab-down: ## Detiene el laboratorio MikroTik CHR y borra su red
	$(LAB_SH) down

.PHONY: lab-status
lab-status: ## Estado del laboratorio (VM, túnel WireGuard, netns)
	$(LAB_SH) status

.PHONY: lab-traffic
lab-traffic: ## Genera tráfico desde un cliente del laboratorio (PROFILE=beacon|scan|smtp|volume|dns)
	$(LAB_SH) traffic

.PHONY: lab-console
lab-console: ## Consola serie del CHR (salir con Ctrl-])
	$(LAB_SH) console

.PHONY: lab-selftest
lab-selftest: ## Validación §8.3 en un CHR limpio; deja la salida en .lab/selftest-<ROS>.log
	bash scripts/lab/selftest.sh

##@ Aceptación

# Batería de aceptación del incremento 0 (I0-19, tests/acceptance/README.md). Levanta su propio
# compose (proyecto horus-accept, puertos +20000, volúmenes nuevos) y lo destruye al terminar.
# Variables: ACCEPT_STEPS / ACCEPT_SKIP (lista de pasos), ACCEPT_KEEP=1, ACCEPT_IMAGE_MODE
# (auto|dockerfile|prebuilt), ACCEPT_PORT_OFFSET; herramientas: GOLANGCI_LINT, BUF_BIN,
# PLAYWRIGHT_BROWSERS_PATH.
.PHONY: accept-i0
accept-i0: ## Ejecuta la batería de aceptación del incremento 0 (resumen OK/FAIL/SKIP por paso)
	@GO='$(GO)' GOLANGCI_LINT='$(GOLANGCI_LINT)' bash scripts/accept/accept-i0.sh

.PHONY: accept-image
accept-image: ## Construye la imagen horus:accept (Dockerfile raíz o, si falla, desde el binario local)
	@GO='$(GO)' bash scripts/accept/image.sh

.PHONY: accept-e2e
accept-e2e: ## Solo el e2e de humo contra el backend real (compose de aceptación; ACCEPT_KEEP=1 lo conserva)
	@ACCEPT_STEPS=$${ACCEPT_STEPS:-image,compose-up,healthy,migrations,e2e-api} GO='$(GO)' bash scripts/accept/accept-i0.sh

.PHONY: clean
clean: ## Borra artefactos de build locales
	rm -rf bin dist
