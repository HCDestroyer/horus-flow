#!/usr/bin/env bash
# Prueba de extremo a extremo IKEv2: hub charon (imagen horus-ipsec, configurado solo por el adaptador vici
# de tunnel-agent) ↔ "router" charon con swanctl, en una red de Docker (docs/tunnels.md §8).
#
#   bash tests/tunnels/ipsec-e2e.sh            # construye horus-ipsec:local si no existe
#   HORUS_IPSEC_IMAGE=horus-ipsec:local bash tests/tunnels/ipsec-e2e.sh
#
# Requiere Docker con soporte XFRM en el kernel (SA/políticas). No necesita interfaces XFRM ni ip_gre: la ruta
# de datos route-based (hxfrm*) y GRE no se ejercitan aquí (sí en un host con esos módulos).
set -euo pipefail

repo="$(cd "$(dirname "$0")/../.." && pwd)"
image="${HORUS_IPSEC_IMAGE:-horus-ipsec:local}"
net=hft-ipsec-e2e
subnet=172.28.250.0/24 hub_ip=172.28.250.2 router_ip=172.28.250.3
work="$(mktemp -d)"
psk="$(printf 'Prueba%.0s' 1 2 3 4 5 6 7 8)" # PSK de prueba de baja entropía (no es un secreto)

cleanup() {
  docker rm -f hft-hub hft-router >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT
[ -z "${KEEP:-}" ] || trap - EXIT

docker image inspect "$image" >/dev/null 2>&1 || docker build -t "$image" "$repo/deployments/images/strongswan"
docker network create --subnet "$subnet" "$net" >/dev/null

ike_props=aes256-sha256-modp2048 esp_props=aes256-sha256-modp2048
if [ "${HORUS_TEST_ROUTER_SUITE:-fallback}" = preferred ]; then
  ike_props=aes256gcm16-prfsha384-ecp384,aes256-sha256-ecp384,aes256-sha256-modp2048
  esp_props=aes256gcm16-ecp384,aes256-sha256-ecp384,aes256-sha256-modp2048
fi
mkdir -p "$work/hub" "$work/router"
docker run -d --name hft-hub --network "$net" --ip "$hub_ip" --cap-drop ALL --cap-add NET_ADMIN --cap-add NET_BIND_SERVICE --cap-add SETUID --cap-add SETGID --cap-add CHOWN --cap-add SETPCAP \
  -v "$work/hub:/run/horus-ipsec" "$image" >/dev/null

# Router: charon con swanctl, identidades como la configuración generada por Horus y selectores de su /32
# hacia la red de servicios (como RouterOS). Propone SOLO la suite de respaldo para VRP antiguos
# (AES-256-CBC + SHA-256 + MODP-2048, PFS grupo 14): comprueba que el hub la acepta. Con
# HORUS_TEST_ROUTER_SUITE=preferred propone las tres del hub (el kernel necesita rfc4106(gcm(aes)) para
# instalar la SA con AES-GCM).
cat >"$work/router/horus.conf" <<CONF
connections {
  horus {
    version = 2
    remote_addrs = $hub_ip
    proposals = $ike_props
    dpd_delay = 30s
    local { auth = psk
            id = fqdn:rt-centro.isp.example }
    remote { auth = psk
             id = $hub_ip }
    children {
      horus {
        local_ts = 10.255.0.17/32
        remote_ts = 10.255.0.0/24
        esp_proposals = $esp_props
        start_action = none
      }
    }
  }
}
secrets {
  ike-horus {
    id-a = fqdn:rt-centro.isp.example
    id-b = $hub_ip
    secret = "$psk"
  }
}
CONF
docker run -d --name hft-router --network "$net" --ip "$router_ip" --cap-drop ALL --cap-add NET_ADMIN --cap-add NET_BIND_SERVICE --cap-add SETUID --cap-add SETGID --cap-add CHOWN --cap-add SETPCAP \
  -v "$work/router:/run/horus-ipsec" "$image" >/dev/null
for c in hft-hub hft-router; do
  for _ in $(seq 1 50); do [ -S "$work/${c#hft-}/charon.vici" ] && break; sleep 0.2; done
done
docker cp "$work/router/horus.conf" hft-router:/etc/swanctl/conf.d/horus.conf
docker exec -u 0:1430 hft-router swanctl --load-all --uri unix:///run/horus-ipsec/charon.vici >/dev/null

# Sin el módulo esp4 el kernel no puede instalar SA (charon: "Requested type not found"): se comprueba IKE y la
# negociación ESP, no la instalación de la CHILD SA.
expect_child=1
if ! docker exec hft-hub ip xfrm state add src 192.0.2.1 dst 192.0.2.2 proto esp spi 0x100 mode tunnel \
    auth 'hmac(sha256)' 0x3030303030303030303030303030303030303030303030303030303030303030 \
    enc 'cbc(aes)' 0x3030303030303030303030303030303030303030303030303030303030303030 2>/dev/null; then
  expect_child=0
  echo "AVISO: el kernel no admite SA ESP (esp4): solo se valida IKEv2 y la selección de la propuesta ESP" >&2
else
  docker exec hft-hub ip xfrm state flush
fi

cd "$repo"
HORUS_TEST_VICI_SOCKET="$work/hub/charon.vici" HORUS_TEST_ROUTER_CONTAINER=hft-router HORUS_TEST_HUB_CONTAINER=hft-hub \
  HORUS_TEST_ROUTER_IP="$router_ip" HORUS_TEST_HUB_ID="$hub_ip" HORUS_TEST_PSK="$psk" HORUS_TEST_EXPECT_CHILD="$expect_child" \
  go test -count=1 -v -tags=ipsec -run TestIKEv2AgainstStrongSwan ./services/tunnelagent/internal/adapters/vici/
