"""DDL v0 aplicable (C2, C3) sin servicios: PostgreSQL con el parser real (libpg_query vía pglast) y
ClickHouse ejecutado en un motor embebido vacío (chdb). Requiere: pip install pglast==7.2 chdb==3.1.2.
En CI con servicios, PLAT puede sustituirlo por psql contra postgres:16 y clickhouse-client contra 24.8.
Uso: python3 tools/ddl-check.py datastore/v0/postgres-contract-v0.sql datastore/v0/clickhouse-contract-v0.sql
"""
import sys

import pglast

pg, ch = sys.argv[1], sys.argv[2]
with open(pg, encoding="utf-8") as f:
    stmts = pglast.parse_sql(f.read())
print("postgres: parsed", len(stmts), "statements (libpg_query, PG 16 grammar)")

import chdb  # noqa: E402
from chdb import session  # noqa: E402

s = session.Session()
with open(ch, encoding="utf-8") as f:
    sql = f.read()
parts = [p.strip() for p in sql.split(";\n") if p.strip() and not all(l.strip().startswith("--") or not l.strip() for l in p.strip().splitlines())]
for p in parts:
    s.query(p)
print("clickhouse: applied", len(parts), "statements on empty chdb", chdb.__version__)
print(s.query("SELECT database, name, engine FROM system.tables WHERE database IN ('flows','dim') ORDER BY database, name", "TSV"))
