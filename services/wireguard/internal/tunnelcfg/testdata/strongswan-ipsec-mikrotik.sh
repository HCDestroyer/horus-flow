#!/bin/sh
# Horus Flow · túnel IPSEC · ejemplo genérico Linux (router rt-centro)
set -eu
# 1) Interfaz XFRM (if_id 42 local) con la IP de túnel y ruta a la red de servicios de Horus
ip link add horus0 type xfrm dev {INTERFAZ_WAN} if_id 42
ip addr add 10.255.0.17/32 dev horus0
ip link set horus0 mtu 1400 up
ip route replace 10.255.0.0/24 dev horus0 src 10.255.0.17
# 2) strongSwan (swanctl): IKEv2 con PSK, iniciado por el router
cat > /etc/swanctl/conf.d/horus.conf <<'CONF'
connections {
  horus {
    version = 2
    remote_addrs = 203.0.113.10
    proposals = aes256gcm16-prfsha384-ecp384,aes256-sha256-ecp384,aes256-sha256-modp2048
    dpd_delay = 30s
    rekey_time = 4h
    local {
      auth = psk
      id = fqdn:rt-centro.isp.example
    }
    remote {
      auth = psk
      id = 203.0.113.10
    }
    children {
      horus {
        local_ts = 0.0.0.0/0
        remote_ts = 0.0.0.0/0
        if_id_in = 42
        if_id_out = 42
        esp_proposals = aes256gcm16-ecp384,aes256-sha256-ecp384,aes256-sha256-modp2048
        rekey_time = 1h
        dpd_action = restart
        start_action = start
        close_action = start
      }
    }
  }
}
secrets {
  ike-horus {
    id-router = fqdn:rt-centro.isp.example
    id-hub = 203.0.113.10
    secret = "PruebaPruebaPruebaPruebaPruebaPruebaPruebaPrueba"
  }
}
CONF
chmod 0600 /etc/swanctl/conf.d/horus.conf
swanctl --load-all
swanctl --initiate --child horus
# 3) Exporte NetFlow v9/IPFIX a 10.255.0.1:4739 (IPFIX) o :2055 (NetFlow v9) con origen 10.255.0.17 y SNMPv3
#    (usuario horus-00000001, SHA-256/AES-128) escuchando en 10.255.0.17 solo para 10.255.0.0/24.
