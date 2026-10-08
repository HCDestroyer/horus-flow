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
# con COMPOSE_PROFILES (p. ej. `make up COMPOSE_PROFILES=app`).
COMPOSE_DIR          := deployments/compose
HORUS_COMPOSE_FILE   := $(COMPOSE_DIR)/compose.dev.yaml
COMPOSE              ?= docker compose --project-directory $(COMPOSE_DIR) -f $(HORUS_COMPOSE_FILE)
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
	@bash scripts/compose-preflight.sh $(HORUS_COMPOSE_FILE)
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
#   SIM_TARGET (127.0.0.1:4739 o :2055) SIM_OUT (fichero .hfsim/.pcap[.gz] en vez de UDP)
#   SIM_EXPECTED (bin/sim/<escenario>-<proto>.expected.json) SIM_ARGS (flags extra)
#   sim-verify: sin variables = prueba sin router de todos los escenarios + fixtures;
#   SIM_IN=fichero o SIM_LISTEN=host:puerto verifican contra SIM_EXPECTED.
SCENARIO     ?= normal
SEED         ?= 1
PROTO        ?= ipfix
SPEED        ?= 1
SIM_DIR      ?= bin/sim
SIM_EXPECTED ?= $(SIM_DIR)/$(SCENARIO)-$(PROTO).expected.json
SIM_FIXTURES ?= tools/flowsim/fixtures/sim

.PHONY: sim
sim: ## Genera flujos IPFIX/NetFlow v9 simulados (make sim SCENARIO=scan PROTO=v9 RATE=2000)
	@mkdir -p $(SIM_DIR) $(dir $(SIM_EXPECTED))
	$(GO) run ./tools/flowsim/cmd/flowsim -scenario $(SCENARIO) -seed $(SEED) -proto $(PROTO) \
		$(if $(RATE),-rate $(RATE)) $(if $(DURATION),-duration $(DURATION)) \
		$(if $(NAT),-nat=$(NAT)) $(if $(IPV6),-ipv6=$(IPV6)) \
		$(if $(SIM_OUT),-out $(SIM_OUT),$(if $(SIM_TARGET),-target $(SIM_TARGET)) -speed $(SPEED)) \
		-expected $(SIM_EXPECTED) $(SIM_ARGS)

.PHONY: sim-verify
sim-verify: ## Decodifica y verifica flujos simulados (sin variables: prueba sin router para CI)
ifneq ($(SIM_LISTEN),)
	$(GO) run ./tools/flowsim/cmd/sim-verify -listen $(SIM_LISTEN) -expected $(SIM_EXPECTED) $(SIM_ARGS)
else ifneq ($(SIM_IN),)
	$(GO) run ./tools/flowsim/cmd/sim-verify -in $(SIM_IN) -expected $(SIM_EXPECTED) $(SIM_ARGS)
else
	$(GO) run ./tools/flowsim/cmd/sim-verify -selftest -fixtures $(SIM_FIXTURES) $(SIM_ARGS)
endif

.PHONY: lab-up
lab-up: ## Levanta el laboratorio MikroTik CHR
	$(call pending,I0-11)

.PHONY: lab-down
lab-down: ## Detiene el laboratorio MikroTik CHR
	$(call pending,I0-11)

##@ Aceptación

.PHONY: accept-i0
accept-i0: ## Ejecuta la batería de aceptación del incremento 0
	$(call pending,I0-19)

.PHONY: clean
clean: ## Borra artefactos de build locales
	rm -rf bin dist
