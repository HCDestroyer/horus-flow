#!/usr/bin/env python3
"""Toda ruta de la tabla del gateway tiene un handler Go que la registra.

packages/schemas/openapi/v0/gateway-routes.yaml declara las rutas del contrato; si ningún módulo
registra "MÉTODO /api/v1/…" (httpx.ServiceMux.Handle/HandleFunc), el gateway responde 503
"El módulo X no atiende esta ruta en este proceso". Las e2e del frontend no lo detectan porque
usan el mock (apps/frontend/mocks/server.ts). Sin dependencias: lee el YAML línea a línea.
"""

from __future__ import annotations

import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
TABLE = ROOT / "packages/schemas/openapi/v0/gateway-routes.yaml"
METHODS = ("GET", "POST", "PUT", "PATCH", "DELETE")


def contract_routes() -> list[tuple[str, str, str]]:
    out: list[tuple[str, str, str]] = []
    module = prefix = ""
    in_methods = False
    for line in TABLE.read_text(encoding="utf-8").splitlines():
        if m := re.match(r"^  ([a-z][a-z0-9_-]*):\s*$", line):
            module, prefix, in_methods = m.group(1), "", False
        elif m := re.match(r"^\s*- prefix:\s*(\S+)", line):
            prefix, in_methods = m.group(1), False
        elif re.match(r"^\s+methods:\s*$", line):
            in_methods = True
        elif in_methods and (m := re.match(r"^\s+([A-Z]+):", line)) and m.group(1) in METHODS:
            out.append((module, m.group(1), prefix))
        elif in_methods and not re.match(r"^\s+[A-Z]+:", line):
            in_methods = False
    return out


def registered() -> set[str]:
    pat = re.compile(r'"((?:%s) /api/v1/[^"]*)"' % "|".join(METHODS))
    found: set[str] = set()
    for f in (ROOT / "services").rglob("*.go"):
        if f.name.endswith("_test.go"):
            continue
        found.update(pat.findall(f.read_text(encoding="utf-8")))
    return found


def main() -> int:
    routes = contract_routes()
    if not routes:
        print(f"routes-check: no se leyó ninguna ruta de {TABLE.relative_to(ROOT)}", file=sys.stderr)
        return 1
    have = registered()
    missing = [r for r in routes if f"{r[1]} {r[2]}" not in have]
    if missing:
        print(f"routes-check: {len(missing)} de {len(routes)} rutas del contrato sin handler Go:", file=sys.stderr)
        for module, method, prefix in missing:
            print(f"  [{module}] {method} {prefix}", file=sys.stderr)
        return 1
    print(f"routes-check: las {len(routes)} rutas del contrato tienen handler")
    return 0


if __name__ == "__main__":
    sys.exit(main())
