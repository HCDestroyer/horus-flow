#!/usr/bin/env python3
"""dod.py — job `dod`: Definición de Terminado verificada por máquina (conventions.md §11).

Versión MÍNIMA de I0-03. Agrega en un solo estado:
  * resultado de los jobs de CI de los que depende (NEEDS_JSON = toJSON(needs));
  * título del PR en Conventional Commits (§6.3); el ID de historia ([I0-02], HF-123) es aviso;
  * ningún TODO/FIXME/XXX añadido sin referencia a una tarea (HF-NNN o ID de historia I<n>-<nn>);
  * cada módulo services/<módulo>/ tocado tiene README.md;
  * migraciones nuevas con marca de tiempo UTC de 14 dígitos (goose, database.md M14);
  * aviso (no bloquea aún) si go.mod añade una dependencia directa sin ADR ni etiqueta
    `architecture` (§11 punto 13).
Los demás puntos de §11 (diff coverage, tenanttest, OpenAPI, squawk...) se añaden cuando
existan sus herramientas; cada uno es una función `check_*` más.

Publica la tabla de resultados en $GITHUB_STEP_SUMMARY. Sale con 1 si algún punto bloquea.
Uso (local): BASE_SHA=origin/main PR_TITLE='feat(x): y [I0-02]' scripts/ci/dod.py
Historia I0-03 (docs/backlog/increment-0.md).
"""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CC_TYPES = "feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert"
TITLE_RE = re.compile(rf"^({CC_TYPES})(\([a-z0-9._/-]+\))?!?: \S.*$")
STORY_RE = re.compile(r"\b(I\d+-\d+|HF-\d+)\b")
TODO_RE = re.compile(r"\b(TODO|FIXME|XXX)\b")
MIGRATION_RE = re.compile(r"^\d{14}_[a-z0-9_]+\.sql$")
# Archivos donde TODO es texto legítimo (documentación de la propia regla, plantillas).
TODO_EXEMPT = (".md", ".github/pull_request_template.md", "scripts/ci/dod.py")

results: list[tuple[str, str, str]] = []  # (punto, estado, detalle)


def git(*args: str) -> str:
    return subprocess.run(["git", *args], cwd=ROOT, check=True, capture_output=True, text=True).stdout


def record(point: str, ok: bool | None, detail: str) -> None:
    results.append((point, "ok" if ok else ("aviso" if ok is None else "KO"), detail))


def check_needs() -> None:
    raw = os.environ.get("NEEDS_JSON", "")
    if not raw:
        record("Jobs de CI", True, "sin dependencias declaradas (ejecución local)")
        return
    needs = json.loads(raw)
    bad = {k: v.get("result") for k, v in needs.items() if v.get("result") not in ("success", "skipped")}
    record("Jobs de CI", not bad, ", ".join(f"{k}={v}" for k, v in bad.items()) or
           f"{len(needs)} jobs en success/skipped")


def check_title() -> None:
    title = os.environ.get("PR_TITLE", "")
    if not title:
        record("Título Conventional Commits", True, "no es un PR (push/merge_group)")
        return
    record("Título Conventional Commits", bool(TITLE_RE.match(title)),
           "tipo(alcance): descripción" if TITLE_RE.match(title) else f"no cumple: {title!r}")
    record("ID de historia en el título", True if STORY_RE.search(title) else None,
           "presente" if STORY_RE.search(title) else "falta [I<n>-<nn>] o HF-NNN (team.md §5.1)")


def changed(base: str, head: str) -> list[str]:
    return [f for f in git("diff", "--name-only", "--diff-filter=ACMR", f"{base}...{head}").splitlines() if f]


def check_todos(base: str, head: str) -> None:
    diff = git("diff", "-U0", "--diff-filter=ACMR", f"{base}...{head}")
    current, offenders = "", []
    for line in diff.splitlines():
        if line.startswith("+++ "):
            current = line[6:] if line.startswith("+++ b/") else ""
        elif line.startswith("+") and not line.startswith("+++") and current:
            if current.endswith(TODO_EXEMPT) or current in TODO_EXEMPT:
                continue
            if TODO_RE.search(line) and not STORY_RE.search(line):
                offenders.append(current)
    uniq = sorted(set(offenders))
    record("TODO con tarea", not uniq, "ninguno sin referencia" if not uniq else
           "TODO/FIXME sin HF-NNN ni I<n>-<nn> en: " + ", ".join(uniq))


def check_module_readmes(files: list[str]) -> None:
    # services/<módulo>/README.md; para binarios, services/cmd/<binario>/README.md.
    modules = set()
    for f in files:
        parts = Path(f).parts
        if parts[0] != "services" or len(parts) < 3:
            continue
        depth = 3 if parts[1] == "cmd" and len(parts) > 3 else 2
        modules.add("/".join(parts[:depth]))
    missing = sorted(m for m in modules if not (ROOT / m / "README.md").exists())
    record("README de módulo", not missing, "presente" if not missing else "falta en: " + ", ".join(missing))


def check_migrations(files: list[str]) -> None:
    migs = [f for f in files if "/migrations/" in f and f.endswith(".sql")]
    bad = [f for f in migs if not MIGRATION_RE.match(Path(f).name)]
    record("Migraciones con marca de tiempo", not bad,
           f"{len(migs)} migraciones" if not bad else "nombre no válido (AAAAMMDDhhmmss_nombre.sql): " + ", ".join(bad))


def check_new_deps(base: str, head: str, files: list[str]) -> None:
    if "go.mod" not in files:
        return
    added = [l[1:].strip() for l in git("diff", "-U0", f"{base}...{head}", "--", "go.mod").splitlines()
             if l.startswith("+") and not l.startswith("+++") and "// indirect" not in l
             and re.match(r"^\+\s*(require\s+)?[a-z0-9.-]+\.[a-z]+/\S+\s+v\S+", l)]
    if not added:
        return
    labels = {x.strip() for x in os.environ.get("PR_LABELS", "").split(",")}
    has_adr = any(f.startswith("docs/adr/") for f in files) or "architecture" in labels
    record("ADR para dependencias nuevas", True if has_adr else None,
           ("con ADR/etiqueta: " if has_adr else "sin ADR ni etiqueta 'architecture': ") + ", ".join(added))


def main() -> int:
    check_needs()
    check_title()
    base, head = os.environ.get("BASE_SHA", ""), os.environ.get("HEAD_SHA", "HEAD")
    if base and set(base) != {"0"}:
        files = changed(base, head)
        check_todos(base, head)
        check_module_readmes(files)
        check_migrations(files)
        check_new_deps(base, head, files)
    else:
        record("Diff", None, "sin base: no se revisa el diff")

    table = ["| Punto DoD | Estado | Detalle |", "| --- | --- | --- |"]
    table += [f"| {p} | {s} | {d.replace('|', '/')} |" for p, s, d in results]
    print("\n".join(table))
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a", encoding="utf-8") as fh:
            fh.write("## Definición de Terminado (job `dod`)\n\n" + "\n".join(table) + "\n")
    failed = [p for p, s, _ in results if s == "KO"]
    if failed:
        print(f"dod: KO — {', '.join(failed)}", file=sys.stderr)
        return 1
    print("dod: OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
