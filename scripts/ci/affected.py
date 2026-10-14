#!/usr/bin/env python3
"""affected.py — detecta qué partes del monorepo afecta un cambio (docs/conventions.md §8.2).

Combina el diff de Git (`git diff --name-only <base>...<head>`) con el grafo de dependencias
Go (`go list -deps`) para decidir qué unidades Go probar y qué jobs condicionales ejecutar.

Unidad Go = carpeta que agrupa paquetes: services/<módulo>, packages/go/<librería>,
tools/<herramienta> o la carpeta de primer nivel. Las unidades de arquitectura y aislamiento de
tenant (packages/go/archtest, packages/go/tenanttest) se prueban SIEMPRE si existen.

Reglas fijas que afectan a TODO: go.mod, go.sum, packages/events/, packages/protobuf/,
.github/, infrastructure/, scripts/ci/, Dockerfile, o un base desconocido (primer push,
historial sin ancestro común).

Salida (líneas clave=valor; en CI se añaden a $GITHUB_OUTPUT):
  all=true|false            todo afectado por una regla fija
  go=true|false             hay unidades Go que probar
  go_units=[...]            JSON con las unidades Go afectadas (para strategy.matrix)
  frontend=true|false       apps/frontend/ cambió y existe package.json
  landing=true|false        apps/landing/ cambió y existe package.json
  landing_motion=true|false apps/landing-motion/ (Remotion) cambió y existe package.json
  compose=true|false        compose, infraestructura o scripts del compose cambiaron
  docker=true|false         hay que construir la imagen (Go, Dockerfile o .dockerignore)
  contracts=true|false      packages/schemas|events|protobuf o infrastructure/clickhouse cambió
  changed_files=N           número de archivos cambiados

Uso: scripts/ci/affected.py [--base REF] [--head REF]
     (BASE_SHA/HEAD_SHA en el entorno también valen; sin base → todo afectado)
Historia I0-03 (docs/backlog/increment-0.md).
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

GLOBAL_PREFIXES = (
    "go.mod",
    "go.sum",
    "packages/events/",
    "packages/protobuf/",
    ".github/",
    "infrastructure/",
    "scripts/ci/",
    "Dockerfile",
)
ALWAYS_UNITS = ("packages/go/archtest", "packages/go/tenanttest")
COMPOSE_PREFIXES = (
    "deployments/compose/",
    "infrastructure/",
    "scripts/compose-preflight.sh",
    "scripts/dev-secrets.sh",
    "scripts/wait-healthy.sh",
    "Makefile",
    ".github/workflows/ci.yml",
)
CONTRACT_PREFIXES = (
    "packages/schemas/",
    "packages/events/",
    "packages/protobuf/",
    "infrastructure/clickhouse/",
)
DOCKER_PREFIXES = ("Dockerfile", ".dockerignore", "go.mod", "go.sum", "services/", "packages/go/")


def run(*cmd: str) -> str:
    return subprocess.run(cmd, cwd=ROOT, check=True, capture_output=True, text=True).stdout


def changed_files(base: str | None, head: str) -> list[str] | None:
    """Archivos cambiados entre el ancestro común de base y head. None = desconocido."""
    if not base or set(base) == {"0"}:
        return None
    try:
        out = run("git", "diff", "--name-only", f"{base}...{head}")
    except subprocess.CalledProcessError:
        try:  # sin ancestro común (historial superficial): diff directo
            out = run("git", "diff", "--name-only", base, head)
        except subprocess.CalledProcessError:
            return None
    return [line for line in out.splitlines() if line]


def go_packages() -> list[dict]:
    """Paquetes Go del módulo con su carpeta relativa y sus dependencias internas."""
    if not (ROOT / "go.mod").exists():
        return []
    try:
        module = run("go", "list", "-m").strip()
        out = run(
            "go",
            "list",
            "-e",
            "-f",
            "{{.ImportPath}}\t{{.Dir}}\t{{join .Deps \" \"}} {{join .TestImports \" \"}} {{join .XTestImports \" \"}}",
            "./...",
        )
    except (subprocess.CalledProcessError, FileNotFoundError) as exc:
        print(f"affected: no se pudo listar paquetes Go ({exc}); se prueba todo", file=sys.stderr)
        return [{"path": "*", "dir": ".", "deps": set()}]
    pkgs = []
    for line in out.splitlines():
        if not line.strip():
            continue
        path, directory, deps = (line.split("\t") + ["", ""])[:3]
        rel = os.path.relpath(directory, ROOT)
        internal = {d for d in deps.split() if d == module or d.startswith(module + "/")}
        pkgs.append({"path": path, "dir": rel, "deps": internal, "module": module})
    return pkgs


def unit_of(rel_dir: str) -> str:
    parts = Path(rel_dir).parts
    if not parts or rel_dir == ".":
        return "."
    if parts[0] == "services" and len(parts) >= 2:
        return f"services/{parts[1]}"
    if parts[0] == "packages" and len(parts) >= 3 and parts[1] == "go":
        return f"packages/go/{parts[2]}"
    if parts[0] == "tools" and len(parts) >= 2:
        return f"tools/{parts[1]}"
    return parts[0]


def starts(path: str, prefixes: tuple[str, ...]) -> bool:
    return any(path == p or path.startswith(p) for p in prefixes)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--base", default=os.environ.get("BASE_SHA"))
    ap.add_argument("--head", default=os.environ.get("HEAD_SHA") or "HEAD")
    args = ap.parse_args()

    files = changed_files(args.base, args.head)
    everything = files is None or any(starts(f, GLOBAL_PREFIXES) for f in files)
    files = files or []

    pkgs = go_packages()
    units: set[str] = set()
    if pkgs:
        if everything or any(p["path"] == "*" for p in pkgs):
            units = {unit_of(p["dir"]) for p in pkgs if p["path"] != "*"} or {"."}
        else:
            module = pkgs[0]["module"]
            changed_dirs = {str(Path(f).parent) for f in files}
            # Un directorio borrado con código Go no aparece en `go list`: se prueba todo.
            deleted_go = any(
                f.endswith(".go") and not (ROOT / Path(f).parent).exists() for f in files
            )
            changed_pkgs = {
                module if d == "." else f"{module}/{d}" for d in changed_dirs
            }
            for p in pkgs:
                if deleted_go or p["dir"] in changed_dirs or p["deps"] & changed_pkgs:
                    units.add(unit_of(p["dir"]))
        for u in ALWAYS_UNITS:
            if (ROOT / u).is_dir() and any((ROOT / u).rglob("*.go")):
                units.add(u)

    frontend_exists = (ROOT / "apps/frontend/package.json").exists()
    landing_exists = (ROOT / "apps/landing/package.json").exists()
    motion_exists = (ROOT / "apps/landing-motion/package.json").exists()
    result = {
        "all": everything,
        "go": bool(units),
        "go_units": sorted(units),
        "frontend": frontend_exists and (everything or any(f.startswith("apps/frontend/") for f in files)),
        "landing": landing_exists and (everything or any(f.startswith("apps/landing/") for f in files)),
        "landing_motion": motion_exists
        and (everything or any(f.startswith("apps/landing-motion/") for f in files)),
        "compose": everything or any(starts(f, COMPOSE_PREFIXES) for f in files),
        "docker": everything or any(starts(f, DOCKER_PREFIXES) for f in files),
        "contracts": everything or any(starts(f, CONTRACT_PREFIXES) for f in files),
        "changed_files": len(files),
    }

    lines = []
    for key, value in result.items():
        if isinstance(value, bool):
            value = "true" if value else "false"
        elif isinstance(value, list):
            value = json.dumps(value, separators=(",", ":"))
        lines.append(f"{key}={value}")
    print("\n".join(lines))
    gh_out = os.environ.get("GITHUB_OUTPUT")
    if gh_out:
        with open(gh_out, "a", encoding="utf-8") as fh:
            fh.write("\n".join(lines) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
