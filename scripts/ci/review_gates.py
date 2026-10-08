#!/usr/bin/env python3
"""review_gates.py — checks `agent-review` y `persona-gate` (conventions.md §6, team.md §5.2).

  agent-review   Pasa si al menos un agente revisor DISTINTO del autor aprobó el PR y ningún
                 revisor agente tiene "cambios pedidos" vigentes. Revisores válidos: la lista
                 AGENT_REVIEWER_LOGINS (variable del repositorio, separada por comas, p. ej.
                 `horus-reviewer[bot]`). Si está vacía (transitorio, hasta crear la GitHub App
                 del revisor) vale cualquier revisor que no sea el autor.
  persona-gate   Si el PR lleva `contract:*`, `area:security` o `area:sensitive`, falla hasta
                 que una persona de PERSONA_LOGINS (por defecto, el dueño del repositorio)
                 lo apruebe; mientras tanto pone la etiqueta `needs:persona` y la quita al
                 aprobarse (la persona trabaja desde `label:needs:persona is:open`).

Fuera de un PR (merge_group, push) ambos pasan: ya se evaluaron antes de entrar en la cola.
Entorno: GITHUB_TOKEN, GITHUB_REPOSITORY, GITHUB_EVENT_NAME, GITHUB_EVENT_PATH,
GITHUB_API_URL, AGENT_REVIEWER_LOGINS, PERSONA_LOGINS.
Uso: scripts/ci/review_gates.py agent-review|persona-gate
Historia I0-03 (docs/backlog/increment-0.md).
"""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

SENSITIVE_PREFIXES = ("contract:",)
SENSITIVE_LABELS = {"area:security", "area:sensitive"}
NEEDS_PERSONA = "needs:persona"


def api(method: str, path: str, body: dict | None = None):
    url = os.environ.get("GITHUB_API_URL", "https://api.github.com") + path
    req = urllib.request.Request(url, method=method, data=json.dumps(body).encode() if body else None)
    req.add_header("Authorization", f"Bearer {os.environ['GITHUB_TOKEN']}")
    req.add_header("Accept", "application/vnd.github+json")
    req.add_header("X-GitHub-Api-Version", "2022-11-28")
    with urllib.request.urlopen(req, timeout=30) as resp:
        data = resp.read()
        return json.loads(data) if data else None


def paged(path: str) -> list:
    items, page = [], 1
    while True:
        sep = "&" if "?" in path else "?"
        chunk = api("GET", f"{path}{sep}per_page=100&page={page}")
        items += chunk
        if len(chunk) < 100:
            return items
        page += 1


def logins(var: str, default: str = "") -> set[str]:
    raw = os.environ.get(var, "") or default
    return {x.strip().lower() for x in raw.split(",") if x.strip()}


def latest_states(reviews: list) -> dict[str, str]:
    """Último estado decisivo por revisor (APPROVED / CHANGES_REQUESTED / DISMISSED)."""
    state: dict[str, str] = {}
    for r in sorted(reviews, key=lambda r: r.get("submitted_at") or ""):
        user = (r.get("user") or {}).get("login", "").lower()
        if r.get("state") in ("APPROVED", "CHANGES_REQUESTED", "DISMISSED"):
            state[user] = r["state"]
    return state


def main() -> int:
    if len(sys.argv) != 2 or sys.argv[1] not in ("agent-review", "persona-gate"):
        print(__doc__)
        return 2
    gate = sys.argv[1]
    event = json.load(open(os.environ["GITHUB_EVENT_PATH"], encoding="utf-8"))
    pr = event.get("pull_request")
    if not pr:
        print(f"{gate}: OK — evento {os.environ.get('GITHUB_EVENT_NAME')} sin PR; se evaluó antes de la cola")
        return 0

    repo = os.environ["GITHUB_REPOSITORY"]
    number = pr["number"]
    author = pr["user"]["login"].lower()
    states = latest_states(paged(f"/repos/{repo}/pulls/{number}/reviews"))

    if gate == "agent-review":
        allowed = logins("AGENT_REVIEWER_LOGINS")
        def is_agent(u: str) -> bool:
            return u != author and (u in allowed if allowed else True)
        approved = sorted(u for u, s in states.items() if s == "APPROVED" and is_agent(u))
        blocking = sorted(u for u, s in states.items() if s == "CHANGES_REQUESTED" and is_agent(u))
        if blocking:
            print(f"agent-review: KO — cambios pedidos por {', '.join(blocking)}")
            return 1
        if not approved:
            who = ", ".join(sorted(allowed)) if allowed else "cualquier revisor distinto del autor"
            print(f"agent-review: KO — falta la aprobación de un agente revisor ({who}); autor: {author}")
            return 1
        print(f"agent-review: OK — aprobado por {', '.join(approved)}")
        return 0

    # persona-gate: etiquetas frescas (el etiquetador pudo añadir alguna en este mismo run).
    labels = {l["name"] for l in api("GET", f"/repos/{repo}/issues/{number}/labels")}
    sensitive = sorted(l for l in labels if l in SENSITIVE_LABELS or l.startswith(SENSITIVE_PREFIXES))
    personas = logins("PERSONA_LOGINS", repo.split("/")[0])

    def set_needs_persona(on: bool) -> None:
        try:
            if on and NEEDS_PERSONA not in labels:
                api("POST", f"/repos/{repo}/issues/{number}/labels", {"labels": [NEEDS_PERSONA]})
            elif not on and NEEDS_PERSONA in labels:
                api("DELETE", f"/repos/{repo}/issues/{number}/labels/{urllib.parse.quote(NEEDS_PERSONA, safe='')}")
        except urllib.error.HTTPError as exc:  # PR desde fork: token de solo lectura
            print(f"persona-gate: aviso — no se pudo actualizar '{NEEDS_PERSONA}' ({exc.code})")

    if not sensitive:
        set_needs_persona(False)
        print("persona-gate: OK — sin etiquetas contract:* / area:security / area:sensitive")
        return 0
    approved_by = sorted(u for u, s in states.items() if s == "APPROVED" and u in personas)
    if approved_by:
        set_needs_persona(False)
        print(f"persona-gate: OK — {', '.join(sensitive)} aprobado por la persona ({', '.join(approved_by)})")
        return 0
    set_needs_persona(True)
    print(f"persona-gate: KO — {', '.join(sensitive)} exige la aprobación de la persona "
          f"({', '.join(sorted(personas))}); etiquetado '{NEEDS_PERSONA}'")
    return 1


if __name__ == "__main__":
    sys.exit(main())
