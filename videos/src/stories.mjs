// Guiones de los videos de Horus Flow y ArgosISP.
// Coordenadas normalizadas (0..1) sobre capturas 16:9 (3200×1800):
//   path: encuadres [x, y, w] por los que pasa la cámara (en 16:9 la altura es igual a w).
//   v:    encuadres para 9:16 (opcional; si falta, se centra en el mismo punto).
//   hi:   resaltes { r: [x, y, w, h], at: fracción de la escena, label }.
// `say` es la narración; los subtítulos salen del mismo texto.

const FULL = [0, 0, 1]

export const stories = {
  // ───────────────────────────── 1 · Demo para la landing ─────────────────────────────
  demo: {
    title: 'Horus Flow · demo',
    scenes: [
      {
        kind: 'title',
        eyebrow: 'Para ISP',
        title: 'Ve lo que pasa dentro de tu red',
        sub: 'Tráfico por cliente, seguridad y capacidad en una sola consola',
        say: 'Esto es Horus Flow: convierte los flujos de tus routers en respuestas para tu ISP.',
      },
      {
        kind: 'shot', img: 'horus/dashboard-noc', label: 'NOC del ISP',
        path: [FULL, [0.16, 0.17, 0.56]],
        v: [[0.16, 0.17, 0.3], [0.44, 0.17, 0.3]],
        hi: [{ r: [0.175, 0.2, 0.265, 0.19], at: 0.45, label: 'Tráfico en vivo' }],
        say: 'Lee NetFlow, IPFIX y sFlow de tus routers MikroTik y Huawei, y te muestra el tráfico en vivo, nodo por nodo.',
      },
      {
        kind: 'shot', img: 'horus/clientes', label: 'Clientes',
        path: [[0.16, 0.08, 0.84], [0.16, 0.25, 0.6]],
        v: [[0.17, 0.26, 0.28], [0.6, 0.26, 0.28]],
        say: 'Cada IP se vuelve un cliente: sabes quién consume, qué servicios usa y si su equipo se comporta raro.',
      },
      {
        kind: 'shot', img: 'horus/cliente-infectado', label: 'Ficha del cliente',
        path: [[0.17, 0.1, 0.66], [0.17, 0.38, 0.6]],
        v: [[0.17, 0.4, 0.36], [0.36, 0.4, 0.36]],
        hi: [{ r: [0.185, 0.43, 0.8, 0.18], at: 0.35, label: 'Por qué tiene este estado' }],
        say: 'Y si algo cambia, te explica por qué: con evidencia, no con suposiciones.',
      },
      {
        kind: 'shot', img: 'horus/hallazgo-botnet', label: 'Hallazgo · escaneo saliente',
        path: [[0.16, 0.1, 0.5], [0.16, 0.32, 0.84]],
        v: [[0.17, 0.1, 0.32], [0.17, 0.34, 0.32], [0.66, 0.34, 0.33]],
        hi: [{ r: [0.665, 0.4, 0.33, 0.6], at: 0.55, label: 'Qué hacer' }],
        say: 'Cuando un equipo infectado escanea internet o habla con un servidor de botnet, Horus lo detecta, te muestra la evidencia y te dice qué hacer.',
      },
      {
        kind: 'shot', img: 'horus/dashboard-seguridad', label: 'Seguridad',
        path: [[0.16, 0.18, 0.84], [0.44, 0.19, 0.56]],
        v: [[0.17, 0.2, 0.28], [0.44, 0.2, 0.28]],
        say: 'Escaneos, botnets y DDoS, con su severidad y su nivel de confianza, en un solo lugar.',
      },
      {
        kind: 'shot', img: 'horus/capacidad', label: 'Capacidad',
        path: [[0.16, 0.1, 0.84], [0.16, 0.4, 0.84]],
        v: [[0.17, 0.18, 0.3], [0.17, 0.44, 0.3], [0.5, 0.44, 0.3]],
        hi: [{ r: [0.585, 0.53, 0.05, 0.1], at: 0.45, label: '82 % en hora pico' }],
        say: 'La planificación de capacidad te dice cuánto falta para llenar cada enlace, y cuándo ampliarlo, antes de que tus clientes lo noten.',
      },
      {
        kind: 'shot', img: 'horus/mural-noc', label: 'Pantalla NOC', frame: 'tv',
        path: [FULL, [0.0, 0.05, 0.6]],
        v: [[0, 0.06, 0.4], [0.33, 0.06, 0.4]],
        say: 'En la sala de operación, una pantalla mural que se actualiza sola.',
      },
      {
        kind: 'shot', img: 'horus/notificaciones', label: 'Alertas y reportes',
        path: [[0.16, 0.08, 0.84], [0.16, 0.1, 0.7]],
        v: [[0.17, 0.12, 0.32], [0.5, 0.12, 0.32]],
        say: 'Alertas por correo o Telegram, y un reporte de seguridad cada semana.',
      },
      {
        kind: 'pair', a: 'horus/integraciones', b: 'argos/int-4-bloque-horus',
        aPath: [0.16, 0.1, 0.84], bPath: [0.15, 0.62, 0.85],
        labels: ['Horus Flow', 'ArgosISP'],
        say: 'Y conectado a ArgosISP, cada hallazgo llega a la ficha del cliente.',
      },
      {
        kind: 'outro',
        title: 'Tu red, a la vista',
        sub: 'Agenda una demo con datos de tu propia red',
        say: 'Horus Flow. Tu red, a la vista.',
      },
    ],
  },

  // ───────────────────────────── 2 · Promo corta para redes ─────────────────────────────
  promo: {
    title: 'Horus Flow · promo',
    music: { bpm: 112, energy: 1 },
    scenes: [
      { kind: 'title', eyebrow: 'Horus Flow', title: '¿Sabes qué pasa dentro de tu red?', say: '¿Sabes qué pasa dentro de tu red?', min: 2.6 },
      {
        kind: 'shot', img: 'horus/dashboard-noc', label: 'Tráfico en vivo',
        path: [[0.16, 0.17, 0.56], [0.17, 0.2, 0.3]],
        v: [[0.17, 0.18, 0.28], [0.17, 0.2, 0.18]],
        say: 'Horus Flow lee los flujos de tus routers MikroTik y Huawei,',
      },
      {
        kind: 'shot', img: 'horus/hallazgos', label: 'Hallazgos',
        path: [[0.16, 0.08, 0.84], [0.16, 0.38, 0.6]],
        v: [[0.17, 0.4, 0.26], [0.17, 0.6, 0.26]],
        say: 'detecta botnets, escaneos y DDoS,',
      },
      {
        kind: 'shot', img: 'horus/capacidad', label: 'Capacidad',
        path: [[0.16, 0.4, 0.84], [0.5, 0.45, 0.5]],
        v: [[0.17, 0.44, 0.3], [0.54, 0.44, 0.3]],
        say: 'y te avisa antes de que se llene un enlace.',
      },
      {
        kind: 'shot', img: 'horus/mural-seguridad', label: 'Pantalla NOC', frame: 'tv',
        path: [FULL, [0, 0.04, 0.72]],
        v: [[0, 0.08, 0.45], [0.3, 0.08, 0.45]],
        say: 'Todo en una pantalla.',
        min: 2.2,
      },
      { kind: 'outro', title: 'Tu red, a la vista', sub: 'Para ISP que quieren ver su red', cta: 'Pide tu demo', say: 'Horus Flow. Tu red, a la vista.' },
    ],
  },

  // ───────────────────────────── 3 · Integración Horus ↔ Argos ─────────────────────────────
  integracion: {
    title: 'Horus Flow + ArgosISP',
    scenes: [
      {
        kind: 'title', eyebrow: 'Horus Flow + ArgosISP', title: 'La red y el negocio, conectados',
        sub: 'Seguridad de la red dentro de la gestión de clientes',
        say: 'Horus Flow ve la red. ArgosISP administra clientes, PPPoE y facturación. Juntos trabajan como uno solo.',
      },
      {
        kind: 'shot', img: 'horus/integraciones', label: 'Horus Flow · Integraciones',
        path: [[0.16, 0.08, 0.84], [0.17, 0.14, 0.45]],
        v: [[0.17, 0.14, 0.3], [0.17, 0.2, 0.25]],
        hi: [{ r: [0.185, 0.315, 0.08, 0.04], at: 0.4, label: 'Un enlace, un paso' }],
        say: 'En Horus generas un enlace de conexión.',
      },
      {
        kind: 'shot', img: 'argos/int-1-enlace', label: 'ArgosISP · Integración con Horus',
        path: [[0.15, 0.06, 0.85], [0.29, 0.3, 0.44]],
        v: [[0.29, 0.3, 0.27], [0.55, 0.36, 0.27]],
        hi: [{ r: [0.57, 0.435, 0.2, 0.075], at: 0.35, label: 'Pegar y conectar' }],
        say: 'Lo pegas en ArgosISP, y los dos sistemas intercambian sus credenciales en un solo paso, sin copiar secretos a mano.',
      },
      {
        kind: 'shot', img: 'argos/int-2-activa', label: 'ArgosISP · Integración con Horus',
        path: [[0.29, 0.06, 0.44], [0.29, 0.06, 0.66]],
        v: [[0.29, 0.08, 0.25], [0.48, 0.08, 0.25]],
        hi: [{ r: [0.885, 0.125, 0.08, 0.05], at: 0.3, label: 'Activa' }],
        say: 'Activas la integración, y listo.',
      },
      {
        kind: 'shot', img: 'horus/hallazgo-botnet', label: 'Horus Flow · Hallazgo',
        path: [[0.16, 0.1, 0.5], [0.16, 0.3, 0.5]],
        v: [[0.17, 0.1, 0.3], [0.17, 0.34, 0.3]],
        say: 'Cuando Horus detecta que el equipo de un cliente está escaneando internet,',
      },
      {
        kind: 'shot', img: 'argos/int-4-bloque-horus', label: 'ArgosISP · Ficha del servicio',
        path: [[0.15, 0.62, 0.85], [0.17, 0.66, 0.55]],
        v: [[0.17, 0.68, 0.28], [0.4, 0.68, 0.28]],
        hi: [{ r: [0.585, 0.71, 0.4, 0.105], at: 0.35, label: 'Hallazgo de Horus' }],
        say: 'ArgosISP lo muestra en la ficha del servicio: estado de seguridad, hallazgo y severidad.',
      },
      {
        kind: 'shot', img: 'argos/int-3-servicio', label: 'ArgosISP · Ficha del servicio',
        path: [[0.15, 0, 0.85], [0.15, 0, 0.62]],
        v: [[0.16, 0.06, 0.42], [0.42, 0, 0.42]],
        hi: [{ r: [0.163, 0.18, 0.4, 0.04], at: 0.2, label: 'Limitar, suspender…' }, { r: [0.733, 0.017, 0.093, 0.035], at: 0.6, label: 'Ver en Horus' }],
        say: 'Desde ahí, el operador decide: limitar, suspender o avisar al cliente. Y con Ver en Horus entra directo a la evidencia, sin volver a iniciar sesión.',
      },
      {
        kind: 'shot', img: 'argos/cobro-3-reactivado', label: 'ArgosISP · Cobro',
        path: [[0.15, 0.06, 0.85], [0.38, 0.3, 0.62]],
        v: [[0.15, 0.3, 0.45], [0.55, 0.3, 0.45]],
        hi: [{ r: [0.6, 0.505, 0.095, 0.04], at: 0.4, label: 'Servicio reactivado' }],
        say: 'Y cuando el cliente paga, ArgosISP reactiva su servicio al instante.',
      },
      {
        kind: 'outro', title: 'Horus Flow + ArgosISP', sub: 'La red y el negocio, en el mismo lugar',
        say: 'Horus Flow y ArgosISP. La red y el negocio, conectados.',
      },
    ],
  },

  // ───────────────────────────── 4 · Tutorial: alta de router y NOC ─────────────────────────────
  tutorial: {
    title: 'Recorrido: alta de router y NOC',
    music: { energy: 0.6 },
    scenes: [
      {
        kind: 'title', eyebrow: 'Recorrido', title: 'Alta de router y pantalla NOC',
        sub: 'De un MikroTik nuevo a la sala de operación',
        say: 'En este recorrido conectamos un router MikroTik a Horus Flow y armamos la pantalla de la sala de operación.',
      },
      { kind: 'chapter', n: '1', title: 'Alta de router', say: 'Primero, el alta del router.' },
      {
        kind: 'shot', img: 'horus/alta-0-nodos', label: 'Nodos y routers',
        path: [[0.16, 0.06, 0.84], [0.44, 0.17, 0.56]],
        v: [[0.36, 0.17, 0.3], [0.7, 0.17, 0.3]],
        hi: [{ r: [0.725, 0.535, 0.1, 0.04], at: 0.55, label: 'Conectar router' }],
        say: 'En Nodos y routers, cada nodo agrupa los equipos de un sitio. El Nodo Lago tiene un router pendiente: pulsamos Conectar router.',
      },
      {
        kind: 'shot', img: 'horus/alta-1-pasos', label: 'rt-lago · Conexión',
        path: [[0.16, 0.06, 0.84], [0.17, 0.4, 0.6]],
        v: [[0.17, 0.42, 0.32], [0.17, 0.6, 0.32]],
        say: 'El alta tiene tres pasos: generar el script, pegarlo en el router y verificar.',
      },
      {
        kind: 'shot', img: 'horus/alta-2b-script-detalle', label: 'Paso 1 · Generar script',
        path: [[0.17, 0.08, 0.66], [0.17, 0.45, 0.66]],
        v: [[0.17, 0.3, 0.3], [0.17, 0.72, 0.3]],
        hi: [{ r: [0.215, 0.515, 0.765, 0.105], at: 0.15, label: 'Solo se muestra una vez' }],
        say: 'Horus genera un script de RouterOS que crea un túnel WireGuard, un usuario de solo lectura, un certificado y la exportación de flujos. Se muestra una sola vez, y caduca pronto.',
      },
      {
        kind: 'shot', img: 'horus/alta-2-script', label: 'Paso 2 · Pegar en el router',
        path: [[0.17, 0.25, 0.66], [0.34, 0.4, 0.66]],
        v: [[0.4, 0.55, 0.42], [0.58, 0.6, 0.42]],
        hi: [{ r: [0.84, 0.83, 0.125, 0.04], at: 0.35, label: 'Copiar o descargar .rsc' }],
        say: 'Lo copias, o lo descargas como archivo punto r s c, y lo pegas en la terminal del MikroTik, por Winbox o SSH.',
      },
      {
        kind: 'shot', img: 'horus/alta-3-clave', label: 'Paso 3 · Verificación',
        path: [[0.17, 0.18, 0.62], [0.17, 0.32, 0.45]],
        v: [[0.17, 0.36, 0.26]],
        say: 'Sin recargar la página, Horus marca cada avance: primero recibe la clave del router,',
      },
      {
        kind: 'shot', img: 'horus/alta-4-tunel', label: 'Paso 3 · Verificación',
        path: [[0.17, 0.32, 0.45]],
        v: [[0.17, 0.36, 0.26]],
        hi: [{ r: [0.205, 0.455, 0.2, 0.03], at: 0.1, label: 'Túnel arriba' }],
        say: 'luego confirma que el túnel está arriba,',
      },
      {
        kind: 'shot', img: 'horus/alta-5-flujo', label: 'Paso 3 · Verificación',
        path: [[0.17, 0.32, 0.45], [0.17, 0.3, 0.62]],
        v: [[0.17, 0.36, 0.26], [0.2, 0.4, 0.3]],
        hi: [{ r: [0.205, 0.487, 0.2, 0.03], at: 0.1, label: 'Primer flujo' }],
        say: 'y el primer flujo recibido. Si el nodo aún no tiene prefijos de clientes, te lo indica.',
      },
      {
        kind: 'shot', img: 'horus/alta-6-exportando', label: 'rt-lago',
        path: [[0.16, 0, 0.84], [0.17, 0, 0.6]],
        v: [[0.17, 0, 0.32], [0.45, 0, 0.32]],
        hi: [{ r: [0.185, 0.06, 0.13, 0.045], at: 0.2, label: 'Exportando' }],
        say: 'El router queda en línea y exportando, con su salud en vivo.',
      },
      {
        kind: 'shot', img: 'horus/importar-prefijos', label: 'Importar del MikroTik',
        path: [[0.16, 0, 0.84], [0.18, 0.05, 0.63]],
        v: [[0.18, 0.08, 0.5], [0.18, 0.42, 0.5]],
        hi: [{ r: [0.2, 0.275, 0.6, 0.27], at: 0.4, label: 'Revisas cada prefijo' }],
        say: 'Después importas los prefijos de clientes directo del MikroTik, IPv4 e IPv6, y revisas cada uno antes de confirmar.',
      },
      { kind: 'chapter', n: '2', title: 'La pantalla NOC', say: 'Ahora, la sala de operación.' },
      {
        kind: 'shot', img: 'horus/dashboard-noc', label: 'NOC del ISP',
        path: [FULL, [0.16, 0.17, 0.84]],
        v: [[0.17, 0.18, 0.28], [0.6, 0.18, 0.28]],
        hi: [{ r: [0.72, 0.41, 0.27, 0.29], at: 0.6, label: 'Exportadores' }],
        say: 'Este dashboard reúne el tráfico en vivo, los clientes activos, los hallazgos abiertos y el estado de cada exportador.',
      },
      {
        kind: 'shot', img: 'horus/dashboard-noc', label: 'NOC del ISP',
        path: [[0.16, 0.6, 0.84], [0.44, 0.62, 0.56]],
        v: [[0.17, 0.66, 0.28], [0.72, 0.66, 0.28]],
        say: 'Abajo, las categorías de tráfico, los clientes que más consumen y los hallazgos recientes.',
      },
      {
        kind: 'shot', img: 'horus/routers', label: 'Salud de routers',
        path: [[0.16, 0.06, 0.84], [0.17, 0.2, 0.42]],
        v: [[0.17, 0.22, 0.22], [0.38, 0.22, 0.22]],
        say: 'En Salud de routers ves CPU, memoria, temperatura y el enlace de subida de cada equipo.',
      },
      {
        kind: 'shot', img: 'horus/kioscos', label: 'Pantallas NOC',
        path: [[0.16, 0.06, 0.84], [0.17, 0.08, 0.6]],
        v: [[0.17, 0.1, 0.3], [0.58, 0.1, 0.3]],
        hi: [{ r: [0.59, 0.445, 0.07, 0.04], at: 0.35, label: 'Código de un solo uso' }],
        say: 'Para la TV de la sala, registras una pantalla mural con un código de un solo uso, y eliges qué dashboards rota y cada cuánto.',
      },
      {
        kind: 'shot', img: 'horus/mural-noc', label: 'Vista mural', frame: 'tv',
        path: [FULL, [0.5, 0.3, 0.5]],
        v: [[0, 0.06, 0.4], [0.6, 0.35, 0.4]],
        say: 'La vista mural está pensada para verse a distancia: cifras grandes, alto contraste y actualización sola.',
      },
      {
        kind: 'shot', img: 'horus/mural-seguridad', label: 'Mural de seguridad', frame: 'tv',
        path: [FULL, [0, 0.05, 0.6]],
        v: [[0, 0.06, 0.4], [0.33, 0.06, 0.4]],
        say: 'Y el mural de seguridad muestra los hallazgos abiertos, las señales de botnet y los puertos vigilados.',
      },
      {
        kind: 'outro', title: 'Listo para operar', sub: 'Router conectado y NOC en pantalla',
        say: 'Router conectado y sala de operación en pantalla. Eso es Horus Flow.',
      },
    ],
  },
}
