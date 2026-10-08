#!/usr/bin/env python3
"""internet-sim.py — servidores de prueba del laboratorio CHR (historia I0-11, §8.2).

Corre dentro de la netns internet-sim y escucha en todas sus direcciones:
  :80/tcp    HTTP (GET /, /beacon "C2" de prueba, /blob?kb=N para volumen de bajada)
  :25/tcp    SMTP mínimo (banner, responde 250 a todo, QUIT cierra) para el perfil smtp
  :4444/tcp  "C2" crudo: acepta, lee y cierra
  :5201/tcp  sumidero de volumen de subida (descarta todo lo que recibe)
  :53/udp    responde con el mismo datagrama (suficiente para generar flujos DNS)
Solo biblioteca estándar. Uso: internet-sim.py   (lab.sh lo arranca en segundo plano)
"""

import http.server
import socket
import socketserver
import threading


class HTTP(http.server.BaseHTTPRequestHandler):
    server_version = "horus-lab-internet-sim"

    def do_GET(self):  # noqa: N802
        if self.path.startswith("/blob"):
            kb = 64
            if "kb=" in self.path:
                try:
                    kb = max(1, min(102400, int(self.path.split("kb=")[1].split("&")[0])))
                except ValueError:
                    pass
            body = b"x" * 1024
            self.send_response(200)
            self.send_header("Content-Length", str(kb * 1024))
            self.end_headers()
            for _ in range(kb):
                self.wfile.write(body)
            return
        body = b"ok\n" if not self.path.startswith("/beacon") else b'{"task":"sleep"}\n'
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


class SMTP(socketserver.StreamRequestHandler):
    def handle(self):
        self.wfile.write(b"220 internet-sim ESMTP lab\r\n")
        for line in self.rfile:
            if line.upper().startswith(b"QUIT"):
                self.wfile.write(b"221 bye\r\n")
                return
            self.wfile.write(b"250 ok\r\n")


class Sink(socketserver.BaseRequestHandler):
    def handle(self):
        while self.request.recv(65536):
            pass


class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


def udp_echo(port):
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.bind(("0.0.0.0", port))
    while True:
        data, addr = s.recvfrom(2048)
        s.sendto(data, addr)


def main():
    servers = [
        http.server.ThreadingHTTPServer(("0.0.0.0", 80), HTTP),
        Server(("0.0.0.0", 25), SMTP),
        Server(("0.0.0.0", 4444), Sink),
        Server(("0.0.0.0", 5201), Sink),
    ]
    threads = [threading.Thread(target=s.serve_forever, daemon=True) for s in servers]
    threads.append(threading.Thread(target=udp_echo, args=(53,), daemon=True))
    for t in threads:
        t.start()
    print("internet-sim: escuchando en 80, 25, 4444, 5201/tcp y 53/udp", flush=True)
    threading.Event().wait()


if __name__ == "__main__":
    main()
