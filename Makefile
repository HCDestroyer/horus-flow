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
up: ## Levanta el compose de desarrollo (perfil mínimo)
	$(call pending,I0-02)

.PHONY: down
down: ## Detiene el compose de desarrollo (conserva los datos)
	$(call pending,I0-02)

.PHONY: reset
reset: ## Detiene el compose y borra sus volúmenes
	$(call pending,I0-02)

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

.PHONY: check-codeowners
check-codeowners: ## Verifica que toda carpeta de primer nivel y cada módulo tiene dueño
	@bash scripts/check-codeowners.sh

##@ Datos de flujo simulados y laboratorio

.PHONY: sim
sim: ## Genera flujos IPFIX/NetFlow v9 simulados con el simulador
	$(call pending,I0-10)

.PHONY: sim-verify
sim-verify: ## Decodifica y verifica los flujos generados por el simulador
	$(call pending,I0-10)

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
