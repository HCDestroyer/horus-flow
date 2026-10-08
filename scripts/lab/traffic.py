#!/usr/bin/env python3
"""traffic.py — genera tráfico de prueba desde un cliente del laboratorio (I0-11, criterio 2).

Corre dentro de la netns de un cliente (lab.sh traffic lo lanza con `ip netns exec`). Perfiles
(vendors/mikrotik.md §8.2 y escenarios de tools/flowsim):
  beacon  HTTP GET periódico a /beacon (C2): cada --interval s durante --duration s
  scan    escaneo TCP connect de los puertos 1..--ports del destino (abiertos y cerrados)
  smtp    sesiones SMTP repetidas al :25 (spam simulado)
  volume  sube --mb MB al sumidero :5201 (el CHR free limita la subida a 1 Mbit/s por interfaz)
  dns     consultas UDP :53 periódicas
Solo biblioteca estándar. Imprime un resumen JSON al terminar.
"""

import argparse
import json
import socket
import time
import urllib.request


def beacon(a):
    n = ok = 0
    end = time.time() + a.duration
    while time.time() < end:
        n += 1
        try:
            urllib.request.urlopen(f"http://{a.target}/beacon?id=lab", timeout=3).read()
            ok += 1
        except OSError:
            pass
        time.sleep(a.interval)
    return {"requests": n, "ok": ok}


def scan(a):
    opened = []
    for port in range(1, a.ports + 1):
        s = socket.socket()
        s.settimeout(0.3)
        try:
            if s.connect_ex((a.target, port)) == 0:
                opened.append(port)
        except OSError:
            pass
        finally:
            s.close()
    return {"probed": a.ports, "open": opened}


def smtp(a):
    sessions = 0
    end = time.time() + a.duration
    while time.time() < end:
        try:
            with socket.create_connection((a.target, 25), timeout=3) as s:
                f = s.makefile("rwb")
                f.readline()
                for cmd in (b"HELO lab\r\n", b"MAIL FROM:<a@lab>\r\n", b"RCPT TO:<b@example.net>\r\n",
                            b"DATA\r\n", b"Subject: lab\r\n\r\nhola\r\n.\r\n", b"QUIT\r\n"):
                    f.write(cmd)
                    f.flush()
                    f.readline()
            sessions += 1
        except OSError:
            pass
        time.sleep(a.interval)
    return {"sessions": sessions}


def volume(a):
    chunk = b"\0" * 65536
    sent = 0
    with socket.create_connection((a.target, 5201), timeout=10) as s:
        while sent < a.mb * 1024 * 1024:
            s.sendall(chunk)
            sent += len(chunk)
    return {"bytes": sent}


def dns(a):
    n = 0
    end = time.time() + a.duration
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
        s.settimeout(1)
        while time.time() < end:
            s.sendto(b"\x12\x34\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00\x03lab\x00\x00\x01\x00\x01", (a.target, 53))
            try:
                s.recvfrom(512)
                n += 1
            except OSError:
                pass
            time.sleep(a.interval)
    return {"answers": n}


PROFILES = {"beacon": beacon, "scan": scan, "smtp": smtp, "volume": volume, "dns": dns}


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--profile", required=True, choices=sorted(PROFILES))
    p.add_argument("--target", required=True)
    p.add_argument("--duration", type=float, default=60)
    p.add_argument("--interval", type=float, default=5)
    p.add_argument("--ports", type=int, default=1024)
    p.add_argument("--mb", type=int, default=2)
    a = p.parse_args()
    t0 = time.time()
    try:
        res = PROFILES[a.profile](a)
    except OSError as e:
        print(json.dumps({"profile": a.profile, "target": a.target, "error": str(e)}))
        raise SystemExit(1)
    res.update(profile=a.profile, target=a.target, seconds=round(time.time() - t0, 1))
    print(json.dumps(res))


if __name__ == "__main__":
    main()
