#!/usr/bin/env python3
"""ipfix-probe.py — receptor mínimo de IPFIX/NetFlow v9 para el selftest del laboratorio (I0-11).

No sustituye al colector de Horus ni a tools/flowsim/cmd/sim-verify: solo comprueba, sin
dependencias, lo que pide vendors/mikrotik.md §8.3.3 y §8.3.4:
  - llegan datagramas IPFIX (v10) o v9 desde el exportador esperado;
  - llega al menos una plantilla y registros de datos decodificables con ella;
  - (con --expect-src) alguna dirección sourceIPv4Address/destinationIPv4Address de los
    registros pertenece a la red privada de los clientes: el NAT del mismo router no la oculta.

Uso: ipfix-probe.py --listen 10.255.0.1:4739 --seconds 90 [--exporter 10.255.3.17]
                    [--expect-src 10.20.0.0/24] [--min-records 1]
Sale con 0 si se cumplen las condiciones e imprime un resumen JSON (sin listar IPs completas
más allá de una muestra acotada: son datos de laboratorio, no de clientes reales).
"""

import argparse
import ipaddress
import json
import socket
import struct
import sys
import time

IE_SRC4, IE_DST4, IE_SRC6, IE_DST6 = 8, 12, 27, 28


def parse_templates(body, v9, templates, key):
    off = 0
    while off + 4 <= len(body):
        tid, count = struct.unpack_from("!HH", body, off)
        off += 4
        if tid == 0 and count == 0:
            break  # relleno
        fields = []
        for _ in range(count):
            if off + 4 > len(body):
                return
            ie, ln = struct.unpack_from("!HH", body, off)
            off += 4
            if not v9 and ie & 0x8000:  # enterprise
                ie &= 0x7FFF
                off += 4
                ie = -ie  # no estándar: se ignora al decodificar
            fields.append((ie, ln))
        templates[(key, tid)] = fields


def parse_data(body, fields, out):
    off = 0
    minlen = sum(4 if ln == 65535 else ln for _, ln in fields)
    while off + minlen <= len(body) and minlen > 0:
        rec = {}
        for ie, ln in fields:
            if ln == 65535:  # longitud variable (IPFIX)
                ln = body[off]
                off += 1
                if ln == 255:
                    ln = struct.unpack_from("!H", body, off)[0]
                    off += 2
            val = body[off:off + ln]
            off += ln
            if ie in (IE_SRC4, IE_DST4) and ln == 4:
                rec[ie] = ipaddress.IPv4Address(val)
            elif ie in (IE_SRC6, IE_DST6) and ln == 16:
                rec[ie] = ipaddress.IPv6Address(val)
        out.append(rec)


def handle(pkt, exporter, templates, stats, records):
    if len(pkt) < 20:
        stats["malformed"] += 1
        return
    version = struct.unpack_from("!H", pkt, 0)[0]
    if version == 10:
        length, _, _, domain = struct.unpack_from("!HIII", pkt, 2)
        off, end, v9 = 16, min(length, len(pkt)), False
    elif version == 9:
        domain = struct.unpack_from("!I", pkt, 16)[0]
        off, end, v9 = 20, len(pkt), True
    else:
        stats["other_versions"] += 1
        return
    stats["versions"][str(version)] = stats["versions"].get(str(version), 0) + 1
    key = (exporter, domain)
    while off + 4 <= end:
        sid, slen = struct.unpack_from("!HH", pkt, off)
        if slen < 4:
            break
        body = pkt[off + 4:off + slen]
        if (v9 and sid == 0) or (not v9 and sid == 2):
            parse_templates(body, v9, templates, key)
            stats["template_sets"] += 1
        elif sid >= 256:
            fields = templates.get((key, sid))
            if fields is None:
                stats["data_without_template"] += 1
            else:
                parse_data(body, fields, records)
        off += slen


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--listen", default="0.0.0.0:4739")
    p.add_argument("--seconds", type=float, default=90)
    p.add_argument("--exporter", help="IP origen esperada de los datagramas")
    p.add_argument("--expect-src", help="red privada de clientes que debe aparecer (p. ej. 10.20.0.0/24)")
    p.add_argument("--min-records", type=int, default=1)
    a = p.parse_args()

    host, port = a.listen.rsplit(":", 1)
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind((host, int(port)))
    s.settimeout(0.5)
    want_net = ipaddress.ip_network(a.expect_src) if a.expect_src else None

    stats = {"packets": 0, "versions": {}, "template_sets": 0, "data_without_template": 0,
             "malformed": 0, "other_versions": 0, "foreign_exporter": 0}
    templates, records, exporters = {}, [], set()
    deadline = time.time() + a.seconds
    while time.time() < deadline:
        try:
            pkt, (src, _) = s.recvfrom(65535)
        except socket.timeout:
            continue
        if a.exporter and src != a.exporter:
            stats["foreign_exporter"] += 1
            continue
        stats["packets"] += 1
        exporters.add(src)
        handle(pkt, src, templates, stats, records)
        if want_net is None and len(records) >= a.min_records and stats["template_sets"]:
            break
        if want_net is not None and any(
                isinstance(r.get(ie), ipaddress.IPv4Address) and r[ie] in want_net
                for r in records[-50:] for ie in (IE_SRC4, IE_DST4)):
            break

    addrs = {str(r[ie]) for r in records for ie in (IE_SRC4, IE_DST4, IE_SRC6, IE_DST6) if ie in r}
    private_seen = sorted(x for x in addrs if want_net and ipaddress.ip_address(x).version == 4
                          and ipaddress.ip_address(x) in want_net)
    summary = dict(stats, exporters=sorted(exporters), templates=len(templates), records=len(records),
                   ipv6_records=sum(1 for r in records if IE_SRC6 in r or IE_DST6 in r),
                   client_addresses_seen=private_seen[:10])
    checks = {
        "received": stats["packets"] > 0,
        "template": stats["template_sets"] > 0,
        "records": len(records) >= a.min_records,
    }
    if want_net:
        checks["pre_nat_client_ip"] = bool(private_seen)
    summary["checks"] = checks
    print(json.dumps(summary))
    sys.exit(0 if all(checks.values()) else 1)


if __name__ == "__main__":
    main()
