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
        aPath: [0.16, 0.1, 0.84], bPath: [0.15, 0.3, 0.85],
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
        path: [[0.15, 0.3, 0.85], [0.17, 0.36, 0.55]],
        v: [[0.17, 0.38, 0.42], [0.5, 0.38, 0.42]],
        hi: [{ r: [0.175, 0.505, 0.81, 0.115], at: 0.35, label: 'Hallazgo de Horus' }],
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
  // ───────────────────────────── 5 · Horus Flow: presentación general ─────────────────────────────
  presentacion: {
    title: 'Horus Flow · presentación',
    music: { energy: 0.7 },
    scenes: [
      {
        kind: 'title', eyebrow: 'Presentación', title: 'La visibilidad que tu ISP necesita',
        sub: 'Tráfico, seguridad y capacidad de tu red, en una sola consola',
        say: 'Horus Flow le da a tu ISP visibilidad completa de su red: tráfico, seguridad y capacidad, en una sola consola.',
      },
      {
        kind: 'shot', img: 'horus/resumen', label: 'Cómo funciona',
        path: [[0.16, 0.05, 0.84], [0.17, 0.15, 0.5]],
        v: [[0.17, 0.15, 0.42], [0.2, 0.3, 0.42]],
        hi: [{ r: [0.185, 0.285, 0.2, 0.04], at: 0.5, label: 'Todo funciona' }],
        say: 'Funciona con lo que ya tienes: tus routers MikroTik y Huawei envían sus flujos NetFlow, IPFIX o sFlow por un túnel cifrado, y Horus hace el resto. Nunca escribe en tus equipos.',
      },
      {
        kind: 'shot', img: 'horus/nodos', label: 'Nodos y routers',
        path: [[0.16, 0.06, 0.84], [0.44, 0.17, 0.56]],
        v: [[0.17, 0.17, 0.42], [0.58, 0.17, 0.42]],
        say: 'Organizas la red por nodos y routers, y das de alta cada router con un script, en minutos.',
      },
      {
        kind: 'shot', img: 'horus/dashboard-noc', label: 'NOC del ISP',
        path: [FULL, [0.16, 0.17, 0.56]],
        v: [[0.17, 0.18, 0.42], [0.58, 0.18, 0.42]],
        say: 'El NOC reúne el tráfico en vivo, los clientes activos, los hallazgos abiertos y el estado de cada exportador.',
      },
      {
        kind: 'shot', img: 'horus/trafico', label: 'Tráfico',
        path: [[0.16, 0.05, 0.84], [0.36, 0.6, 0.64]],
        v: [[0.17, 0.2, 0.42], [0.58, 0.62, 0.42]],
        hi: [{ r: [0.59, 0.635, 0.395, 0.33], at: 0.55, label: 'Por servicio' }],
        say: 'Ves el tráfico por cliente y por servicio: video, redes sociales, juegos o actualizaciones.',
      },
      {
        kind: 'shot', img: 'horus/clientes', label: 'Clientes',
        path: [[0.16, 0.08, 0.84], [0.16, 0.25, 0.6]],
        v: [[0.17, 0.26, 0.42], [0.58, 0.26, 0.42]],
        say: 'Cada IP se convierte en un cliente, con su consumo, sus servicios principales y su estado de seguridad.',
      },
      {
        kind: 'shot', img: 'horus/cliente-comercial', label: 'Ficha del cliente',
        path: [[0.17, 0.08, 0.66], [0.17, 0.38, 0.62]],
        v: [[0.17, 0.4, 0.42], [0.45, 0.62, 0.42]],
        hi: [{ r: [0.19, 0.745, 0.45, 0.045], at: 0.5, label: 'Puntuación comercial' }],
        say: 'Incluso detecta cuándo un cliente residencial se comporta como un comercio, y te dice con qué confianza.',
      },
      {
        kind: 'shot', img: 'horus/hallazgos', label: 'Hallazgos de seguridad',
        path: [[0.16, 0.08, 0.84], [0.16, 0.38, 0.6]],
        v: [[0.17, 0.4, 0.42], [0.5, 0.4, 0.42]],
        say: 'En seguridad detecta botnets, escaneos, contactos con servidores de control, envío masivo de correo y ataques DDoS.',
      },
      {
        kind: 'shot', img: 'horus/hallazgo-botnet', label: 'Hallazgo · escaneo saliente',
        path: [[0.16, 0.1, 0.5], [0.16, 0.32, 0.84], [0.5, 0.33, 0.5]],
        v: [[0.17, 0.1, 0.42], [0.17, 0.34, 0.42], [0.62, 0.34, 0.38]],
        hi: [{ r: [0.67, 0.465, 0.32, 0.095], at: 0.7, label: 'Análisis con IA' }],
        say: 'Cada hallazgo explica por qué se marcó, muestra la evidencia y propone qué hacer. Y si lo activas, un asistente de IA analiza la evidencia con datos seudonimizados.',
      },
      {
        kind: 'shot', img: 'horus/deteccion', label: 'Detección',
        path: [[0.16, 0.08, 0.84], [0.16, 0.18, 0.6]],
        v: [[0.17, 0.2, 0.42], [0.58, 0.2, 0.42]],
        say: 'Las reglas y los umbrales de detección se ajustan a la realidad de cada red.',
      },
      {
        kind: 'shot', img: 'horus/capacidad', label: 'Capacidad',
        path: [[0.16, 0.1, 0.84], [0.16, 0.4, 0.84]],
        v: [[0.17, 0.18, 0.42], [0.17, 0.44, 0.42], [0.55, 0.44, 0.42]],
        hi: [{ r: [0.585, 0.53, 0.05, 0.1], at: 0.45, label: '82 % en hora pico' }],
        say: 'La planificación de capacidad muestra la salud de cada enlace y pronostica cuándo tocará ampliarlo.',
      },
      {
        kind: 'shot', img: 'horus/routers', label: 'Salud de routers',
        path: [[0.16, 0.06, 0.84], [0.17, 0.2, 0.42]],
        v: [[0.17, 0.22, 0.42], [0.38, 0.22, 0.42]],
        say: 'También vigila la salud de tus routers y switches,',
      },
      {
        kind: 'shot', img: 'horus/servicios-infra', label: 'Servicios de infraestructura',
        path: [[0.16, 0.05, 0.84], [0.17, 0.2, 0.62]],
        v: [[0.17, 0.25, 0.42], [0.58, 0.25, 0.42]],
        say: 'y los servicios de tu infraestructura: DNS, RADIUS, portales e IPTV.',
      },
      {
        kind: 'shot', img: 'horus/mural-noc', label: 'Pantalla NOC', frame: 'tv',
        path: [FULL, [0.0, 0.05, 0.6]],
        v: [[0, 0.06, 0.45], [0.3, 0.06, 0.45]],
        say: 'Para la sala de operación, pantallas murales que rotan los dashboards solas.',
      },
      {
        kind: 'shot', img: 'horus/reportes', label: 'Reportes y alertas',
        path: [[0.16, 0.08, 0.84], [0.16, 0.1, 0.62]],
        v: [[0.17, 0.12, 0.42], [0.55, 0.12, 0.42]],
        say: 'Cada semana recibes un reporte de seguridad, y alertas por correo o Telegram cuando algo cambia.',
      },
      {
        kind: 'pair', a: 'horus/integraciones', b: 'argos/int-4-bloque-horus',
        aPath: [0.16, 0.1, 0.84], bPath: [0.15, 0.3, 0.85],
        labels: ['Horus Flow', 'ArgosISP'],
        say: 'Se integra con ArgosISP, para llevar la seguridad de la red a la gestión de tus clientes.',
      },
      {
        kind: 'shot', img: 'horus/plataforma-isps', label: 'Varios ISP',
        path: [[0.16, 0.02, 0.84], [0.17, 0.1, 0.6]],
        v: [[0.17, 0.12, 0.42], [0.58, 0.12, 0.42]],
        hi: [{ r: [0.175, 0.15, 0.815, 0.225], at: 0.4, label: 'Cada ISP, separado' }],
        say: 'Y una sola instalación atiende a varios ISP, cada uno con sus datos, usuarios y permisos separados.',
      },
      {
        kind: 'outro', title: 'Tu red, a la vista', sub: 'Agenda una demo con datos de tu propia red',
        say: 'Horus Flow. Tu red, a la vista.',
      },
    ],
  },

  // ───────────────────────────── 6 · ArgosIsp: presentación general ─────────────────────────────
  'argos-demo': {
    title: 'ArgosIsp · presentación',
    brand: 'argos',
    music: { energy: 0.7 },
    scenes: [
      {
        kind: 'title', eyebrow: 'Para ISP', title: 'La gestión de tu ISP, en orden',
        sub: 'Clientes, servicios PPPoE, facturación y cobros en un solo panel',
        say: 'ArgosISP es el sistema de gestión para tu ISP: clientes, servicios, facturación y cobros, en un solo panel.',
      },
      {
        kind: 'shot', img: 'argos/inicio', label: 'Inicio',
        path: [[0.15, 0.05, 0.85], [0.15, 0.18, 0.6]],
        v: [[0.16, 0.18, 0.42], [0.55, 0.18, 0.42]],
        hi: [{ r: [0.165, 0.19, 0.82, 0.105], at: 0.25, label: 'Servicios por estado' }],
        say: 'Desde el inicio ves el estado del negocio: servicios activos, limitados y cortados, lo facturado del mes, la cartera vencida y los cobros del día.',
      },
      {
        kind: 'shot', img: 'argos/clientes', label: 'Clientes',
        path: [[0.15, 0, 0.85], [0.15, 0.18, 0.6]],
        v: [[0.16, 0.2, 0.42], [0.55, 0.2, 0.42]],
        say: 'Cada cliente tiene su ficha: datos, datos fiscales, servicios, estado de cuenta y línea de tiempo.',
      },
      {
        kind: 'shot', img: 'argos/servicio-1', label: 'Servicio PPPoE',
        path: [[0.15, 0.06, 0.85], [0.15, 0.35, 0.85]],
        v: [[0.16, 0.2, 0.42], [0.58, 0.62, 0.42]],
        hi: [{ r: [0.585, 0.83, 0.405, 0.16], at: 0.55, label: 'Sesión en vivo' }],
        say: 'Los servicios PPPoE se autentican con RADIUS: plan, velocidad, direcciones IPv4 e IPv6 y la sesión activa, en vivo.',
      },
      {
        kind: 'shot', img: 'argos/routers-velocidad', label: 'Routers · velocidad',
        path: [[0.15, 0, 0.85], [0.62, 0, 0.38]],
        v: [[0.62, 0, 0.38], [0.62, 0.35, 0.38]],
        hi: [{ r: [0.665, 0.27, 0.135, 0.04], at: 0.55, label: 'Vista previa del cambio' }],
        say: 'La velocidad se controla en tus routers MikroTik, y antes de cada cambio ves una vista previa de lo que se va a tocar.',
      },
      {
        kind: 'shot', img: 'argos/facturas', label: 'Facturas',
        path: [[0.15, 0.05, 0.85], [0.4, 0.1, 0.6]],
        v: [[0.16, 0.1, 0.42], [0.58, 0.1, 0.42]],
        say: 'Las facturas se generan solas cada mes, con su estado de pago y de factura electrónica.',
      },
      {
        kind: 'shot', img: 'argos/cobro-2-recibo', label: 'Cobrar',
        path: [[0.15, 0.05, 0.85], [0.5, 0.05, 0.5]],
        v: [[0.16, 0.08, 0.42], [0.58, 0.08, 0.42]],
        hi: [{ r: [0.745, 0.1, 0.24, 0.53], at: 0.4, label: 'Recibo listo' }],
        say: 'Cobrar es rápido: buscas al cliente, registras el pago y sale el recibo.',
      },
      {
        kind: 'shot', img: 'argos/cobro-3-reactivado', label: 'Reactivación',
        path: [[0.15, 0.06, 0.85], [0.38, 0.3, 0.62]],
        v: [[0.15, 0.3, 0.45], [0.55, 0.3, 0.45]],
        hi: [{ r: [0.6, 0.505, 0.095, 0.04], at: 0.35, label: 'Servicio reactivado' }],
        say: 'Si el servicio estaba cortado, se reactiva en ese mismo momento.',
      },
      {
        kind: 'shot', img: 'argos/promesas', label: 'Promesas de pago',
        path: [[0.15, 0.08, 0.85], [0.15, 0.1, 0.6]],
        v: [[0.16, 0.12, 0.42], [0.55, 0.12, 0.42]],
        hi: [{ r: [0.835, 0.265, 0.06, 0.04], at: 0.5, label: 'Aprobar' }],
        say: 'Las promesas de pago se evalúan con reglas claras, y los comprobantes de depósito se aprueban con un clic.',
      },
      {
        kind: 'shot', img: 'argos/portal-corte', label: 'Página de corte', frame: 'phone',
        path: [[0.03, 0.02, 0.94], [0.03, 0.1, 0.94]],
        hi: [{ r: [0.04, 0.42, 0.92, 0.06], at: 0.5, label: 'Pagar en línea' }],
        say: 'Cuando un servicio se corta, el cliente ve una página con lo que debe y cómo reactivarlo.',
      },
      {
        kind: 'shot', img: 'argos/portal-cuenta', label: 'Portal del cliente', frame: 'phone',
        path: [[0.03, 0.02, 0.94], [0.03, 0.08, 0.94]],
        hi: [{ r: [0.035, 0.43, 0.93, 0.47], at: 0.45, label: 'Todo desde el teléfono' }],
        say: 'Y en su portal paga en línea, sube comprobantes, pide una promesa de pago y consulta sus facturas.',
      },
      {
        kind: 'shot', img: 'argos/reportes', label: 'Reportes',
        path: [[0.15, 0.08, 0.85], [0.15, 0.1, 0.8]],
        v: [[0.16, 0.12, 0.42], [0.55, 0.12, 0.42]],
        say: 'Tienes reportes de cartera, cobranza, cierres de caja y altas y bajas,',
      },
      {
        kind: 'shot', img: 'argos/bitacora', label: 'Bitácora',
        path: [[0.15, 0.05, 0.85], [0.15, 0.15, 0.6]],
        v: [[0.16, 0.15, 0.42], [0.55, 0.15, 0.42]],
        say: 'y una bitácora que registra cada acción.',
      },
      {
        kind: 'shot', img: 'argos/int-4-bloque-horus', label: 'Con Horus Flow',
        path: [[0.15, 0.3, 0.85], [0.17, 0.36, 0.55]],
        v: [[0.17, 0.38, 0.42], [0.5, 0.38, 0.42]],
        hi: [{ r: [0.175, 0.505, 0.81, 0.115], at: 0.35, label: 'Seguridad de la red' }],
        say: 'Y con Horus Flow conectado, la seguridad de la red aparece en la ficha de cada servicio.',
      },
      {
        kind: 'outro', title: 'La gestión de tu ISP, en orden', sub: 'Agenda una demo',
        say: 'ArgosISP. La gestión de tu ISP, en orden.',
      },
    ],
  },

  // ───────────────────────────── 7 · ArgosIsp: promo corta ─────────────────────────────
  'argos-promo': {
    title: 'ArgosIsp · promo',
    brand: 'argos',
    music: { bpm: 112, energy: 1 },
    scenes: [
      { kind: 'title', eyebrow: 'ArgosIsp', title: '¿Tu ISP sigue en hojas de cálculo?', say: '¿Tu ISP todavía se administra en hojas de cálculo?', min: 2.6 },
      {
        kind: 'shot', img: 'argos/inicio', label: 'Todo en un panel',
        path: [[0.15, 0.05, 0.85], [0.15, 0.18, 0.5]],
        v: [[0.16, 0.18, 0.42], [0.45, 0.18, 0.42]],
        say: 'ArgosISP reúne clientes, servicios y facturación,',
      },
      {
        kind: 'shot', img: 'argos/cobro-2-recibo', label: 'Cobro en segundos',
        path: [[0.15, 0.05, 0.85], [0.6, 0.05, 0.4]],
        v: [[0.6, 0.06, 0.42]],
        say: 'cobra en segundos,',
      },
      {
        kind: 'shot', img: 'argos/cobro-3-reactivado', label: 'Reactivación al instante',
        path: [[0.38, 0.3, 0.62], [0.5, 0.4, 0.4]],
        v: [[0.45, 0.35, 0.45]],
        hi: [{ r: [0.6, 0.505, 0.095, 0.04], at: 0.2, label: 'Servicio reactivado' }],
        say: 'reactiva el servicio al instante,',
      },
      {
        kind: 'shot', img: 'argos/portal-cuenta', label: 'Portal del cliente', frame: 'phone',
        path: [[0.03, 0.02, 0.94], [0.03, 0.08, 0.94]],
        say: 'y le da a tus clientes su propio portal.',
      },
      { kind: 'outro', title: 'Tu ISP, en orden', sub: 'Clientes, servicios y cobros en un solo panel', cta: 'Pide tu demo', say: 'ArgosISP. La gestión de tu ISP, en orden.' },
    ],
  },

  // ───────────────────────────── 8 · ArgosIsp: tutorial alta y cobro ─────────────────────────────
  'argos-tutorial': {
    title: 'ArgosIsp · alta de cliente y servicio',
    brand: 'argos',
    music: { energy: 0.55 },
    scenes: [
      {
        kind: 'title', eyebrow: 'Recorrido', title: 'Alta de cliente y servicio',
        sub: 'Del registro al servicio PPPoE activo, y su primer cobro',
        say: 'En este recorrido damos de alta un cliente en ArgosISP, le creamos su servicio PPPoE y registramos un cobro.',
      },
      { kind: 'chapter', n: '1', title: 'El cliente', say: 'Primero, el cliente.' },
      {
        kind: 'shot', img: 'argos/clientes', label: 'Clientes',
        path: [[0.15, 0, 0.85], [0.6, 0, 0.4]],
        v: [[0.58, 0, 0.42]],
        hi: [{ r: [0.875, 0.01, 0.12, 0.04], at: 0.45, label: 'Nuevo cliente' }],
        say: 'En Clientes pulsamos Nuevo cliente.',
      },
      {
        kind: 'shot', img: 'argos/alta-2-cliente-lleno', label: 'Nuevo cliente',
        path: [[0.15, 0.03, 0.6], [0.15, 0.35, 0.6]],
        v: [[0.16, 0.05, 0.5], [0.16, 0.45, 0.5]],
        hi: [{ r: [0.178, 0.195, 0.452, 0.03], at: 0.15, label: 'Datos generales' }],
        say: 'Completamos sus datos generales, la ubicación y, si hace falta, los datos fiscales para su factura.',
      },
      {
        kind: 'shot', img: 'argos/alta-3-cliente-creado', label: 'Ficha del cliente',
        path: [[0.15, 0, 0.85], [0.5, 0, 0.5]],
        v: [[0.16, 0.03, 0.42], [0.58, 0.03, 0.42]],
        hi: [{ r: [0.835, 0.1, 0.09, 0.03], at: 0.55, label: 'Nuevo servicio' }],
        say: 'Al crearlo se abre su ficha, y desde aquí pulsamos Nuevo servicio.',
      },
      { kind: 'chapter', n: '2', title: 'Su servicio PPPoE', say: 'Ahora, su servicio.' },
      {
        kind: 'shot', img: 'argos/alta-4-servicio-router', label: 'Router y tipo de conexión',
        path: [[0.15, 0, 0.85], [0.3, 0.05, 0.55]],
        v: [[0.3, 0.05, 0.55], [0.3, 0.3, 0.55]],
        hi: [{ r: [0.348, 0.555, 0.452, 0.15], at: 0.45, label: 'PPPoE, IP estática o DHCP' }],
        say: 'El asistente pide primero el router y el tipo de conexión: PPPoE, IP estática o DHCP con IP fija.',
      },
      {
        kind: 'shot', img: 'argos/alta-5-servicio-plan', label: 'Plan',
        path: [[0.3, 0.05, 0.55], [0.3, 0.25, 0.55]],
        v: [[0.3, 0.1, 0.55], [0.3, 0.3, 0.55]],
        hi: [{ r: [0.35, 0.445, 0.45, 0.08], at: 0.35, label: 'Hogar 10 Mbps' }],
        say: 'Elegimos uno de los planes disponibles en ese router.',
      },
      {
        kind: 'shot', img: 'argos/alta-6-paso-1', label: 'IPv4',
        path: [[0.3, 0.02, 0.55]],
        v: [[0.3, 0.02, 0.55]],
        hi: [{ r: [0.35, 0.32, 0.22, 0.08], at: 0.15, label: 'Automática' }],
        say: 'La IPv4 se toma sola del pool del router,',
      },
      {
        kind: 'shot', img: 'argos/alta-6-paso-2', label: 'IPv6',
        path: [[0.3, 0.02, 0.55]],
        v: [[0.3, 0.02, 0.55]],
        say: 'la IPv6 sigue el modo del router,',
      },
      {
        kind: 'shot', img: 'argos/alta-6-paso-3', label: 'Credenciales PPP',
        path: [[0.3, 0.02, 0.55]],
        v: [[0.3, 0.02, 0.55]],
        hi: [{ r: [0.348, 0.29, 0.465, 0.06], at: 0.15, label: 'Generadas' }],
        say: 'y el usuario y la contraseña PPPoE se generan automáticamente.',
      },
      {
        kind: 'shot', img: 'argos/alta-6-paso-4', label: 'Facturación e instalación',
        path: [[0.3, 0.05, 0.55], [0.3, 0.3, 0.55]],
        v: [[0.3, 0.1, 0.55], [0.3, 0.4, 0.55]],
        say: 'Revisamos la facturación y los datos de instalación,',
      },
      {
        kind: 'shot', img: 'argos/alta-6-paso-5', label: 'Confirmar',
        path: [[0.3, 0.05, 0.55], [0.3, 0.25, 0.55]],
        v: [[0.3, 0.1, 0.55], [0.3, 0.3, 0.55]],
        hi: [{ r: [0.725, 0.705, 0.09, 0.045], at: 0.55, label: 'Crear servicio' }],
        say: 'confirmamos, y creamos el servicio.',
      },
      {
        kind: 'shot', img: 'argos/alta-8-servicio-activo', label: 'Servicio activo',
        path: [[0.15, 0.06, 0.85], [0.15, 0.25, 0.85]],
        v: [[0.16, 0.08, 0.42], [0.58, 0.2, 0.42]],
        hi: [{ r: [0.305, 0.1, 0.048, 0.03], at: 0.15, label: 'Activo' }],
        say: 'El servicio queda activo, con su plan, su red y sus credenciales. Cada consulta de la contraseña queda en la bitácora.',
      },
      { kind: 'chapter', n: '3', title: 'El cobro', say: 'Por último, un cobro.' },
      {
        kind: 'shot', img: 'argos/cobro-1-pendiente', label: 'Cobrar',
        path: [[0.15, 0.05, 0.85], [0.15, 0.05, 0.6]],
        v: [[0.16, 0.1, 0.42], [0.55, 0.1, 0.42]],
        hi: [{ r: [0.165, 0.425, 0.565, 0.12], at: 0.3, label: 'Avisos vencidos' }, { r: [0.76, 0.675, 0.095, 0.04], at: 0.7, label: 'Registrar pago' }],
        say: 'En Cobrar buscamos al cliente: vemos lo vencido, con sus avisos pendientes ya marcados.',
      },
      {
        kind: 'shot', img: 'argos/cobro-2-recibo', label: 'Recibo',
        path: [[0.15, 0.05, 0.85], [0.5, 0.05, 0.5]],
        v: [[0.16, 0.08, 0.42], [0.58, 0.08, 0.42]],
        hi: [{ r: [0.745, 0.1, 0.24, 0.53], at: 0.4, label: 'Listo para imprimir' }],
        say: 'Registramos el pago, y sale el recibo listo para imprimir.',
      },
      {
        kind: 'shot', img: 'argos/cobro-3-reactivado', label: 'Reactivación',
        path: [[0.15, 0.06, 0.85], [0.38, 0.3, 0.62]],
        v: [[0.15, 0.3, 0.45], [0.55, 0.3, 0.45]],
        hi: [{ r: [0.6, 0.505, 0.095, 0.04], at: 0.35, label: 'Servicio reactivado' }],
        say: 'Como este servicio estaba suspendido, ArgosISP lo reactiva al instante.',
      },
      {
        kind: 'outro', title: 'Listo para operar', sub: 'Cliente, servicio y cobro en minutos',
        say: 'Cliente dado de alta, servicio activo y cobro registrado. Eso es ArgosISP.',
      },
    ],
  },
}
