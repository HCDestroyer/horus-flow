#!/usr/bin/env python3
"""scope_guard.py — check `scope-guard` (docs/conventions.md §6.3, ADR-0028).

Un agente solo toca las rutas que posee. El agente se deduce de la rama del PR
(`<agente>/<ID>-slug`, docs/backlog/team.md §5.1, o `agent/<agente>/...`, conventions §6.3)
y la propiedad de cada archivo, de .github/CODEOWNERS: el dueño de un archivo es el agente
declarado (`# Agente: <ROL>`) en el bloque de la ÚLTIMA regla que coincide y declara un agente
válido (misma semántica de "última regla gana" que GitHub).

Pasa sin comprobar cuando:
  * el PR lleva la etiqueta `scope-extended` (la pone la persona o el coordinador);
  * la rama no tiene prefijo de agente (p. ej. `human/...`): no es un PR de agente;
  * el evento no es un PR (push, merge_group): el alcance ya se comprobó en el PR.

Rutas compartidas que cualquier agente puede tocar: go.mod, go.sum (añadir dependencias; el
job `dod` exige ADR para dependencias directas nuevas) y docs/open-questions/ (team.md §6.4).

Uso: scripts/ci/scope_guard.py --branch <rama> [--labels l1,l2] [--base REF] [--head REF]
     [--repo DIR] [--codeowners ARCHIVO] [--files archivo...]
     (si se pasan archivos, no se consulta Git). En CI se ejecuta la copia del script y del
     CODEOWNERS de la rama BASE: un PR no puede relajar su propio control.
Sale con 0 si todo está en alcance, 1 si hay archivos fuera, 2 ante errores de uso.
Historia I0-03 (docs/backlog/increment-0.md).
"""

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
AGENTS = ("PLAT", "CORE", "FLOW", "SEC", "UI", "INT")
SHARED = ("go.mod", "go.sum", "docs/open-questions/")
OVERRIDE_LABEL = "scope-extended"
BRANCH_RE = re.compile(r"^(?:agent/)?(plat|core|flow|sec|ui|int)(?:/|-|$)", re.IGNORECASE)


def pattern_to_regex(pattern: str) -> re.Pattern[str]:
    """Traduce un patrón de CODEOWNERS (sintaxis gitignore reducida) a regex."""
    anchored = pattern.startswith("/") or "/" in pattern.rstrip("/")
    p = pattern.lstrip("/")
    if p.endswith("/"):
        p += "**"
    out, i = "", 0
    while i < len(p):
        if p.startswith("**/", i):
            out += "(?:.*/)?"
            i += 3
        elif p.startswith("**", i):
            out += ".*"
            i += 2
        elif p[i] == "*":
            out += "[^/]*"
            i += 1
        elif p[i] == "?":
            out += "[^/]"
            i += 1
        else:
            out += re.escape(p[i])
            i += 1
    prefix = "^" if anchored else "^(?:.*/)?"
    return re.compile(prefix + out + "(?:/.*)?$")


def load_rules(codeowners: Path) -> list[tuple[re.Pattern[str], str, str]]:
    rules, agent = [], ""
    for raw in codeowners.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line:
            continue
        if line.startswith("#"):
            m = re.search(r"Agente:\s*([A-Z]+)", line)
            if m:
                agent = m.group(1) if m.group(1) in AGENTS else ""
            elif "Agente:" in line:
                agent = ""
            continue
        pattern = line.split()[0]
        rules.append((pattern_to_regex(pattern), agent, pattern))
    return rules


def owner_of(path: str, rules) -> tuple[str, str]:
    for regex, agent, pattern in reversed(rules):
        if agent and regex.match(path):
            return agent, pattern
    return "", ""


def git_changed(repo: str, base: str, head: str) -> list[str]:
    out = subprocess.run(
        ["git", "diff", "--name-only", f"{base}...{head}"],
        cwd=repo, check=True, capture_output=True, text=True,
    ).stdout
    return [f for f in out.splitlines() if f]


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--branch", default=os.environ.get("GITHUB_HEAD_REF", ""))
    ap.add_argument("--labels", default=os.environ.get("PR_LABELS", ""))
    ap.add_argument("--base", default=os.environ.get("BASE_SHA", ""))
    ap.add_argument("--head", default=os.environ.get("HEAD_SHA", "HEAD"))
    ap.add_argument("--repo", default=str(ROOT), help="checkout con el diff (por defecto, este repo)")
    ap.add_argument("--codeowners", default=str(ROOT / ".github/CODEOWNERS"))
    ap.add_argument("--files", nargs="*")
    args = ap.parse_args()

    labels = {l.strip() for l in args.labels.split(",") if l.strip()}
    if OVERRIDE_LABEL in labels:
        print(f"scope-guard: OK — etiqueta '{OVERRIDE_LABEL}' presente; alcance ampliado")
        return 0

    m = BRANCH_RE.match(args.branch or "")
    if not m:
        print(f"scope-guard: OK — la rama '{args.branch}' no tiene prefijo de agente "
              f"({'|'.join(a.lower() for a in AGENTS)}/...); no aplica")
        return 0
    agent = m.group(1).upper()

    if args.files is not None:
        files = args.files
    else:
        if not args.base:
            print("scope-guard: falta --base (o BASE_SHA)", file=sys.stderr)
            return 2
        files = git_changed(args.repo, args.base, args.head)

    rules = load_rules(Path(args.codeowners))
    outside = []
    for f in files:
        if any(f == s or f.startswith(s) for s in SHARED):
            continue
        owner, pattern = owner_of(f, rules)
        if owner != agent:
            outside.append((f, owner or "?", pattern or "(sin regla con agente)"))

    if outside:
        print(f"scope-guard: KO — la rama es del agente {agent} y el diff toca rutas de otros:")
        for f, owner, pattern in outside:
            print(f"  {f}  → dueño {owner} (regla {pattern})")
        print(f"Saca esos cambios a un PR del agente dueño, abre un PR de contrato, o pide a la "
              f"persona o al coordinador la etiqueta '{OVERRIDE_LABEL}'.")
        return 1
    print(f"scope-guard: OK — {len(files)} archivos dentro del alcance del agente {agent}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
