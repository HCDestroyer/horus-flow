# Horus Flow

Horus Flow es una plataforma multi-ISP de visibilidad de tráfico y **detección de botnets** para
proveedores de Internet. Cada ISP (tenant) conecta el router principal de sus nodos —MikroTik
RouterOS 7 primero— por un túnel WireGuard; Horus recibe sus flujos IPFIX/NetFlow, descubre
automáticamente a los clientes por su IP, enriquece el tráfico (ASN, organización, servicio,
reputación) y lo guarda en ClickHouse. Sobre esos datos detecta comportamientos de botnet
(contacto con C2, escaneos, DDoS, spam…) y genera hallazgos explicables con acciones
recomendadas, que el operador ve en dashboards modulares y pantallas de NOC/kiosco.

El backend es un único binario Go (`horus`) con roles seleccionables (`HORUS_ROLES`); el frontend
es Nuxt 4 + Nuxt UI. Lo construye un equipo de agentes de IA coordinado con una persona.

## Estructura

```
apps/frontend/         UI Nuxt 4 (agente UI)
services/cmd/horus/    main único: compone módulos según HORUS_ROLES (PLAT)
services/<módulo>/     módulos del binario: gateway, auth, devices, wireguard, wgagent, snmp,
                       collector, ingester, traffic, analytics, detection, alerts, jobs
packages/              contratos (protobuf, events, schemas) y librerías Go comunes (go/)
infrastructure/        configuración de PostgreSQL, ClickHouse, NATS, Valkey, Traefik…
deployments/           compose y despliegue
scripts/               utilidades de desarrollo y CI
tools/flowsim/         simulador de flujos IPFIX/NetFlow v9
tests/acceptance/      aceptación por incremento (make accept-iN)
docs/                  diseño, ADRs, backlog
```

La propiedad de cada carpeta por agente está en [`.github/CODEOWNERS`](.github/CODEOWNERS)
(ver [`docs/backlog/team.md`](docs/backlog/team.md) §2).

## Cómo empezar

Requisitos: Go 1.24, GNU Make y, para `make lint`, golangci-lint v2.

```bash
make help        # lista todos los objetivos
make build       # compila ./bin/horus
make test        # tests de Go
make lint        # golangci-lint + verificación de CODEOWNERS
```

`make accept-i0` ejecuta la batería de aceptación del incremento 0 (tests Go con `-race`, lint,
contratos, simulador, compose con el perfil `app`, migraciones, e2e de humo contra el backend real
y Playwright del frontend) y termina con un resumen OK/FAIL/SKIP por paso; ver
[`tests/acceptance/README.md`](tests/acceptance/README.md).

## Documentación

Todo el diseño está en [`docs/README.md`](docs/README.md): arquitectura, módulos, convenciones,
decisiones del PO, ADRs y backlog por incrementos.
