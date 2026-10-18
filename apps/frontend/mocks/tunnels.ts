import type { Peer, Router, TunnelConfig } from '~~/types/api'
import type { OnboardingProgress, MockRouter } from './inventory'
import type { TransportOverride } from './state'
import { iso } from './random'

/**
 * Transportes IPsec IKEv2 y GRE en la API simulada (docs/tunnels.md): `PUT /routers/{id}/transport`
 * y `POST /routers/{id}/tunnel-config`. El estado del túnel avanza con el mismo reloj del asistente
 * (`horus.mock.onboardingStepMs`): configuración generada → túnel arriba → primer flujo.
 */

/** Aplica el transporte elegido al router y a su túnel. */
export function applyTransport(
  router: Router,
  peer: Peer,
  progress: OnboardingProgress,
  transport: TransportOverride | undefined,
  now: number,
) {
  if (!transport) return
  router.connection_mode = transport.mode
  router.protection_level = transport.level
  router.tunnel_remote_address = transport.remoteAddress
  router.tunnel_remote_id = transport.remoteId
  if (transport.mode === 'direct') {
    router.tunnel_address = null
    router.wireguard_peer_id = null
    return
  }
  if (transport.mode === 'wireguard') {
    peer.transport = 'wireguard'
    peer.tunnel = null
    return
  }
  // IPsec/GRE: sin paso de clave; pending_configuration hasta la primera SA o paquete.
  if (router.onboarding_state === 'key_received') router.onboarding_state = 'pending_configuration'
  const up = progress.stage === 'tunnel_up' || progress.stage === 'exporting'
  const configured = !!progress.run && progress.run.scriptAt >= transport.at
  peer.transport = transport.mode
  peer.public_key = null
  peer.endpoint =
    up && transport.mode === 'ipsec' ? `${transport.remoteAddress ?? '198.51.100.7'}:4500` : null
  peer.status = up ? 'active' : configured ? 'pending_handshake' : 'awaiting_enrollment'
  peer.handshake_state = up ? 'ok' : 'never'
  peer.last_handshake_at = up ? iso(now - 5_000) : null
  peer.tunnel = {
    state:
      transport.mode === 'ipsec'
        ? up
          ? 'established'
          : configured
            ? 'connecting'
            : null
        : configured
          ? 'up'
          : null,
    last_rekey_at: transport.mode === 'ipsec' && up ? iso(progress.tunnelAt ?? now) : null,
    last_packet_at: transport.mode === 'gre' && up ? iso(now - 3_000) : null,
    error: null,
    remote_address: transport.remoteAddress,
    remote_id: transport.remoteId,
    gre_key: transport.mode === 'gre' && router.vendor === 'huawei' ? 7 : null,
  }
}

function secret(seed: string, n: number) {
  const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'
  let h = 2166136261
  let out = ''
  for (let i = 0; i < n; i++) {
    h = Math.imul(h ^ (seed.charCodeAt(i % seed.length) + i), 16777619) >>> 0
    out += chars[h % chars.length]
  }
  return out
}

/** Configuración generada (simulada; la real la genera services/wireguard/internal/tunnelcfg). */
export function tunnelConfig(
  item: MockRouter,
  transport: TransportOverride,
  at: number,
): TunnelConfig {
  const tun = item.peer.address.replace('/32', '')
  const hub = '203.0.113.10'
  const ipsec = transport.mode === 'ipsec'
  const psk = ipsec ? secret(item.router.id + at, 48) : null
  const snmp = `horus-${item.router.id.slice(-8)}`
  const huawei = item.router.vendor === 'huawei'
  const vendorConfig = huawei
    ? [
        'system-view',
        'interface LoopBack10',
        ` ip address ${tun} 255.255.255.255`,
        ' quit',
        ...(ipsec
          ? [
              'ike peer horus',
              ' undo version 1',
              ' version 2',
              ` pre-shared-key cipher ${psk}`,
              ` remote-address ${hub}`,
              ' quit',
              'interface Tunnel10',
              ' ip address unnumbered interface LoopBack10',
              ' tunnel-protocol ipsec',
              ` destination ${hub}`,
              ' ipsec profile horus',
              ' quit',
            ]
          : [
              'interface Tunnel10',
              ' ip address unnumbered interface LoopBack10',
              ' tunnel-protocol gre',
              ` source ${transport.remoteAddress}`,
              ` destination ${hub}`,
              ' gre key 7',
              ' quit',
            ]),
        `ip netstream export source ${tun}`,
        'ip netstream export host 10.255.0.1 2055',
        'return',
      ]
    : ipsec
      ? [
          `/ip address add address=${tun}/32 interface=horus-lo comment="horus"`,
          `/ip ipsec peer add name=horus address=${hub}/32 exchange-mode=ike2 profile=horus comment="horus"`,
          `/ip ipsec identity add peer=horus auth-method=pre-shared-key secret="${psk}" comment="horus"`,
          `/ip ipsec policy add peer=horus tunnel=yes src-address=${tun}/32 dst-address=10.255.0.0/24 comment="horus"`,
          `/ip traffic-flow target add dst-address=10.255.0.1 port=4739 version=ipfix src-address=${tun}`,
        ]
      : [
          `/interface gre add name=horus-gre local-address=${transport.remoteAddress} remote-address=${hub} mtu=1400 !keepalive comment="horus"`,
          `/ip address add address=${tun}/32 network=10.255.0.1 interface=horus-gre comment="horus"`,
          `/ip traffic-flow target add dst-address=10.255.0.1 port=4739 version=ipfix src-address=${tun}`,
        ]
  const linux = ipsec
    ? ['connections {', '  horus {', '    version = 2', `    remote_addrs = ${hub}`, '  }', '}']
    : [`ip tunnel add horus0 mode gre local ${transport.remoteAddress} remote ${hub} ttl 64`]
  const notes = [
    `IP de túnel del router: ${tun}/32 (identidad del exportador).`,
    ipsec
      ? 'La clave precompartida (PSK) aparece UNA SOLA VEZ.'
      : 'GRE NO CIFRA: SNMP y flujos viajan en claro.',
  ]
  return {
    router_id: item.router.id,
    transport: ipsec ? 'ipsec' : 'gre',
    protection_level: transport.level,
    tunnel_address: item.peer.address,
    hub_address: hub,
    hub_id: ipsec ? hub : null,
    remote_address: transport.remoteAddress,
    remote_id: ipsec ? transport.remoteId : null,
    psk,
    gre_key: !ipsec && huawei ? 7 : null,
    snmp_user: snmp,
    configs: [
      {
        platform: huawei ? 'vrp' : 'routeros',
        language: huawei ? 'vrp' : 'routeros',
        title: huawei
          ? `Huawei VRP · ${ipsec ? 'IPsec IKEv2' : 'GRE (sin cifrado)'}`
          : `MikroTik RouterOS 7 · ${ipsec ? 'IPsec IKEv2' : 'GRE (sin cifrado)'}`,
        config: vendorConfig.join('\n') + '\n',
        notes: huawei ? [...notes, 'PENDIENTE DE VALIDACIÓN EN EQUIPO REAL.'] : notes,
        placeholders: huawei
          ? [{ key: '{INTERFAZ_NETSTREAM}', description: 'Interfaz donde activar NetStream.' }]
          : [],
        warnings: [],
      },
      {
        platform: 'strongswan',
        language: 'shell',
        title: `Linux / strongSwan (ejemplo genérico) · ${ipsec ? 'IPsec IKEv2' : 'GRE'}`,
        config: linux.join('\n') + '\n',
        notes,
        placeholders: [],
        warnings: [],
      },
    ],
    warnings:
      transport.level === 'unencrypted_internet'
        ? ['Transporte SIN CIFRAR por Internet (confirmado por un administrador).']
        : [],
  }
}
