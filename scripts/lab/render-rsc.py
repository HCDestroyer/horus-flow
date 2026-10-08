#!/usr/bin/env python3
"""render-rsc.py — genera los scripts RouterOS del laboratorio CHR (historia I0-11).

  render-rsc.py onboarding <salida>   script de onboarding de vendors/mikrotik.md §7 (se extrae
                                      del documento: es la ÚNICA fuente; el laboratorio valida
                                      exactamente lo que está documentado, §8.3.1)
  render-rsc.py base <salida>         infrastructure/lab/chr/base.rsc.tmpl

Los placeholders <NOMBRE> se sustituyen con variables de entorno del mismo nombre. Falla si
queda algún placeholder sin valor fuera de los comentarios. La salida puede contener secretos
del laboratorio: se escribe con permisos 0600 y nunca se imprime.
"""

import os
import re
import sys

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
DOC = os.path.join(REPO, "docs", "vendors", "mikrotik.md")
BASE = os.path.join(REPO, "infrastructure", "lab", "chr", "base.rsc.tmpl")
PLACEHOLDER = re.compile(r"<([A-Z][A-Z0-9_]*)>")


def onboarding_from_doc():
    text = open(DOC, encoding="utf-8").read()
    sec = text.find("\n## 7.")
    if sec < 0:
        sys.exit("render-rsc: no encuentro la sección '## 7.' en docs/vendors/mikrotik.md")
    m = re.search(r"```routeros\n(.*?)```", text[sec:], re.S)
    if not m:
        sys.exit("render-rsc: no encuentro el bloque ```routeros de la §7")
    return m.group(1)


def render(src):
    missing = set()

    def sub(m):
        name = m.group(1)
        val = os.environ.get(name)
        if val is None or val == "":
            missing.add(name)
            return m.group(0)
        if any(c in val for c in '"\\\n$'):
            sys.exit(f"render-rsc: el valor de {name} tiene caracteres no permitidos")
        return val

    out = []
    for line in src.splitlines():
        if line.lstrip().startswith("#"):
            out.append(line)  # los comentarios conservan sus placeholders (Opción B, etc.)
            continue
        out.append(PLACEHOLDER.sub(sub, line))
    if missing:
        sys.exit("render-rsc: faltan valores para: " + ", ".join(sorted(missing)))
    return "\n".join(out) + "\n"


def main():
    if len(sys.argv) != 3 or sys.argv[1] not in ("onboarding", "base"):
        sys.exit(__doc__)
    kind, dst = sys.argv[1], sys.argv[2]
    src = onboarding_from_doc() if kind == "onboarding" else open(BASE, encoding="utf-8").read()
    data = render(src)
    fd = os.open(dst, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        f.write(data)


if __name__ == "__main__":
    main()
