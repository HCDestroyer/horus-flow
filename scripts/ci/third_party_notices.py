#!/usr/bin/env python3
"""Genera y verifica THIRD_PARTY_NOTICES.md (licencia propietaria de Horus Flow).

Inventaria los componentes de terceros que se distribuyen con Horus Flow:
  - módulos Go enlazados en el binario `horus` (go list -deps),
  - dependencias de producción del frontend (pnpm licenses list --prod),
  - imágenes de contenedor de terceros fijadas en el compose de producción,
  - y, aparte, las herramientas que solo producen material de la landing (Remotion),
y clasifica su licencia. Falla si aparece una licencia incompatible con distribuir Horus Flow
como software propietario (GPL, AGPL, LGPL, SSPL, licencia desconocida no revisada).

Uso:
  python3 scripts/ci/third_party_notices.py            # regenera THIRD_PARTY_NOTICES.md
  python3 scripts/ci/third_party_notices.py --check    # falla si está desactualizado o hay licencias vetadas
"""
import json
import os
import re
import subprocess
import sys

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
OUT = os.path.join(ROOT, "THIRD_PARTY_NOTICES.md")
COMPOSE = os.path.join(ROOT, "deployments", "compose", "compose.prod.yaml")

ALLOWED = {
    "MIT", "ISC", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "BSD", "0BSD", "Zlib",
    "BlueOak-1.0.0", "CC0-1.0", "Python-2.0", "Unlicense", "CC-BY-4.0", "MPL-2.0",
}
# Licencias dobles: se elige la opción permisiva (la primera que esté en ALLOWED).
# Revisadas a mano: paquetes sin campo de licencia o sin archivo LICENSE en el paquete publicado.
REVIEWED = {
    "tosource": "Zlib",   # LICENSE del paquete: texto zlib ("provided 'as-is'...")
    "vaul-vue": "MIT",    # sin LICENSE en el paquete; el repositorio unovue/vaul-vue declara MIT
}
# MPL-2.0 solo se admite si el componente se usa al compilar y no se distribuye en el producto.
BUILD_ONLY = {"lightningcss", "lightningcss-linux-x64-gnu", "lightningcss-linux-x64-musl"}

# Imágenes de terceros: se distribuyen sin modificar (y en el paquete offline mediante docker save).
IMAGE_INFO = {
    "clickhouse/clickhouse-server": ("ClickHouse", "Apache-2.0", "https://github.com/ClickHouse/ClickHouse"),
    "nats": ("NATS Server", "Apache-2.0", "https://github.com/nats-io/nats-server"),
    "valkey/valkey": ("Valkey", "BSD-3-Clause", "https://github.com/valkey-io/valkey"),
    "traefik": ("Traefik Proxy", "MIT", "https://github.com/traefik/traefik"),
    "busybox": ("BusyBox", "GPL-2.0-only", "https://busybox.net/downloads/"),
}
BASE_IMAGES = [
    ("PostgreSQL (base de horus-postgres)", "PostgreSQL License", "https://www.postgresql.org/about/licence/"),
    ("pgBackRest (incluido en horus-postgres)", "MIT", "https://github.com/pgbackrest/pgbackrest"),
    ("Distroless (base de horus y horus-web)", "Apache-2.0 (imagen); paquetes Debian con sus licencias",
     "https://github.com/GoogleContainerTools/distroless"),
    ("Node.js (runtime de horus-web)", "MIT y otras", "https://github.com/nodejs/node/blob/main/LICENSE"),
]


# Herramientas que solo se usan para PRODUCIR material de la landing (no se distribuyen con
# Horus Flow ni con la landing: lo que llega al visitante son los vídeos renderizados).
MOTION_PKG = os.path.join(ROOT, "apps", "landing-motion", "package.json")
PRODUCTION_TOOLS = [
    # (nombre, paquete npm cuya versión se lee de apps/landing-motion/package.json, licencia, enlace)
    ("Remotion", "remotion", "Remotion License (no es de código abierto; ver nota)",
     "https://github.com/remotion-dev/remotion/blob/main/LICENSE.md"),
]


def production_tools():
    if not os.path.exists(MOTION_PKG):
        return []
    deps = json.load(open(MOTION_PKG)).get("dependencies", {})
    return [(n, deps[p], lic, url) for n, p, lic, url in PRODUCTION_TOOLS if p in deps]


def go_modules():
    out = subprocess.run(
        ["go", "list", "-deps", "-f",
         "{{if not .Standard}}{{with .Module}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}{{end}}",
         "./services/cmd/horus"],
        cwd=ROOT, check=True, capture_output=True, text=True).stdout
    mods = {}
    for line in out.splitlines():
        path, version, d = line.split("\t")
        if path.startswith("github.com/hcdestroyer/"):
            continue
        mods[path] = (version, d)
    res = []
    for path, (version, d) in sorted(mods.items()):
        res.append((path, version, classify_dir(d)))
    return res


def classify_dir(d):
    if not d or not os.path.isdir(d):
        return "UNKNOWN"
    for f in sorted(os.listdir(d)):
        if re.match(r"(LICEN[CS]E|COPYING)", f, re.I):
            t = open(os.path.join(d, f), errors="ignore").read()
            if "GNU AFFERO" in t:
                return "AGPL"
            if "GNU LESSER" in t:
                return "LGPL"
            if "GNU GENERAL PUBLIC" in t:
                return "GPL"
            if "Mozilla Public License" in t:
                return "MPL-2.0"
            if "Apache License" in t:
                return "Apache-2.0"
            if "Permission is hereby granted, free of charge" in t:
                return "MIT"
            if "Redistribution and use in source and binary forms" in t:
                return "BSD-3-Clause" if "Neither the name" in t or "neither the name" in t else "BSD-2-Clause"
            if "Permission to use, copy, modify, and/or distribute" in t or "Permission to use, copy, modify, and distribute" in t:
                return "ISC"
            return "UNKNOWN"
    return "UNKNOWN"


def npm_packages():
    out = subprocess.run(["pnpm", "licenses", "list", "--prod", "--json"],
                         cwd=os.path.join(ROOT, "apps", "frontend"), check=True,
                         capture_output=True, text=True).stdout
    data = json.loads(out)
    res = []
    for lic, pkgs in data.items():
        for p in pkgs:
            for v in p.get("versions", [p.get("version", "")]):
                res.append((p["name"], v, resolve_npm(p["name"], lic)))
    return sorted(set(res))


def resolve_npm(name, lic):
    if name in REVIEWED:
        return REVIEWED[name]
    opts = [o.strip() for o in re.sub(r"[()]", "", lic).split(" OR ")]
    for o in opts:
        if o in ALLOWED:
            return o
    return lic


def images():
    text = open(COMPOSE).read()
    res = []
    for ref in sorted(set(re.findall(r"\$\{HORUS_[A-Z_]+_IMAGE:-([^}]+@sha256:[0-9a-f]{64})\}", text))):
        repo = ref.split("@")[0].rsplit(":", 1)[0]
        tag = ref.split("@")[0].rsplit(":", 1)[1]
        name, lic, src = IMAGE_INFO.get(repo, (repo, "UNKNOWN", ""))
        res.append((name, repo, tag, lic, src))
    return res


def vetted(lic, name=""):
    if lic in ALLOWED:
        return lic != "MPL-2.0" or name in BUILD_ONLY
    return False


def render(gomods, npm, imgs, tools=()):
    lines = [
        "# Avisos de componentes de terceros",
        "",
        "Horus Flow es software propietario (ver [`LICENSE`](LICENSE)). Incluye o utiliza los",
        "componentes de terceros listados a continuación, que se rigen por sus propias licencias. Esta",
        "lista la genera `scripts/ci/third_party_notices.py`; no la edites a mano.",
        "",
        "## Imágenes de contenedor de terceros",
        "",
        "Se distribuyen **sin modificar**, por referencia (compose de producción) y, en el paquete",
        "offline, mediante `docker save`. El código fuente de cada una está disponible en su origen.",
        "",
        "| Componente | Imagen | Versión | Licencia | Código fuente |",
        "| --- | --- | --- | --- | --- |",
    ]
    for name, repo, tag, lic, src in imgs:
        lines.append(f"| {name} | `{repo}` | {tag} | {lic} | {src} |")
    for name, lic, src in BASE_IMAGES:
        lines.append(f"| {name} | — | — | {lic} | {src} |")
    lines += [
        "",
        "BusyBox (GPL-2.0) se usa como imagen auxiliar independiente, sin enlazarse con Horus Flow. Si",
        "se distribuye en el paquete offline, se ofrece su código fuente correspondiente a petición o en",
        "el enlace indicado, como exige la GPL.",
        "",
        f"## Módulos Go enlazados en el binario `horus` ({len(gomods)})",
        "",
        "| Módulo | Versión | Licencia |",
        "| --- | --- | --- |",
    ]
    for path, version, lic in gomods:
        lines.append(f"| `{path}` | {version} | {lic} |")
    lines += [
        "",
        f"## Dependencias de producción del frontend ({len(npm)})",
        "",
        "Paquetes npm incluidos en la interfaz web generada. Los marcados con † solo se usan al",
        "compilar y no se distribuyen en el producto.",
        "",
        "| Paquete | Versión | Licencia |",
        "| --- | --- | --- |",
    ]
    for name, version, lic in npm:
        mark = " †" if name in BUILD_ONLY else ""
        lines.append(f"| `{name}`{mark} | {version} | {lic} |")
    if tools:
        lines += [
            "",
            "## Herramientas de producción de la landing",
            "",
            "Se usan solo para producir material de la landing comercial (`apps/landing-motion`: las",
            "animaciones se renderizan a vídeo y se publican los archivos de vídeo). No se distribuyen",
            "con Horus Flow ni se cargan en la landing.",
            "",
            "| Herramienta | Versión | Licencia | Texto de la licencia |",
            "| --- | --- | --- | --- |",
        ]
        for name, version, lic, url in tools:
            lines.append(f"| {name} | {version} | {lic} | {url} |")
        lines += [
            "",
            "Remotion se rige por la \"Remotion License\": gratuita para particulares, organizaciones con",
            "ánimo de lucro de hasta 3 empleados y organizaciones sin ánimo de lucro; el resto necesita",
            "una \"Company License\". Resumen y cita del texto en",
            "[`apps/landing-motion/README.md`](apps/landing-motion/README.md#licencia-de-remotion).",
        ]
    lines.append("")
    return "\n".join(lines)


def main():
    check = "--check" in sys.argv[1:]
    gomods, npm, imgs = go_modules(), npm_packages(), images()
    bad = [f"go {p} {v}: {lic}" for p, v, lic in gomods if not vetted(lic, p)]
    bad += [f"npm {n} {v}: {lic}" for n, v, lic in npm if not vetted(lic, n)]
    bad += [f"imagen {r}: {lic}" for _, r, _, lic, _ in imgs if lic == "UNKNOWN"]
    if bad:
        print("third_party_notices: licencias no admitidas o sin revisar para software propietario:", file=sys.stderr)
        for b in bad:
            print("  " + b, file=sys.stderr)
        print("Revísalas (REVIEWED/BUILD_ONLY en este script) o sustituye la dependencia.", file=sys.stderr)
        sys.exit(1)
    text = render(gomods, npm, imgs, production_tools())
    if check:
        cur = open(OUT).read() if os.path.exists(OUT) else ""
        if cur != text:
            print("third_party_notices: THIRD_PARTY_NOTICES.md está desactualizado; ejecuta "
                  "`make third-party-notices` y commitéalo.", file=sys.stderr)
            sys.exit(1)
        print(f"third_party_notices: OK ({len(gomods)} módulos Go, {len(npm)} paquetes npm, {len(imgs)} imágenes)")
        return
    open(OUT, "w").write(text)
    print(f"third_party_notices: escrito {os.path.relpath(OUT, ROOT)}")


if __name__ == "__main__":
    main()
