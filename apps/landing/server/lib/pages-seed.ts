// Semilla de la migración 2 (páginas editables). NO SE EDITA: es parte de una migración
// publicada. Para cambiar un texto de la web, usa /admin › Páginas.
//
// - Aviso legal, privacidad y términos: los textos de app/content/legal.ts convertidos a Markdown
//   sin cambiar una palabra (cada sección es un "## título" seguido de sus párrafos).
// - Cookies, uso aceptable, soporte, IA y seguridad: BORRADORES nuevos en español, punto de
//   partida para la revisión de un abogado; [POR CONFIRMAR] marca cada dato que debe dar
//   C&S Company. Se publican con el aviso de borrador.
// - El cuerpo en inglés queda vacío: la web muestra el texto en español con el aviso "solo en
//   español" hasta que se escriba.
import { legalDocs, titular, type LegalDoc } from '../../app/content/legal'

export interface SeedPage {
  id: string
  kind: 'legal' | 'info'
  slugEs: string
  slugEn: string
  titleEs: string
  titleEn: string
  summaryEs: string
  summaryEn: string
  bodyEs: string
  published: boolean
  showInFooter: boolean
  draftNotice: boolean
  sort: number
  updatedAt: string
}

/** Secciones → Markdown: "## título" y un párrafo por bloque. */
export function legalDocToMarkdown(doc: LegalDoc): string {
  return doc.sections
    .map((s) => [`## ${s.title}`, ...s.paragraphs].join('\n\n'))
    .join('\n\n')
    .concat('\n')
}

const ORIGINAL = '2026-10-10T12:00:00.000Z'
const NEW_DRAFTS = '2026-10-14T12:00:00.000Z'

const cookies = `## 1. Quién es el responsable

Este sitio web pertenece a ${titular}

## 2. Qué son las cookies y el almacenamiento local

Una cookie es un pequeño archivo que un sitio guarda en tu navegador. El almacenamiento local es un espacio parecido del propio navegador. Esta política explica cuáles usa este sitio y para qué.

## 3. Lo que usa este sitio

- **Ninguna cookie de seguimiento, publicidad ni analítica de terceros.** El sitio no carga herramientas de analítica ni píxeles de redes sociales.
- **Preferencia de apariencia (almacenamiento local).** El sitio sigue la apariencia clara u oscura de tu sistema y puede guardar esa preferencia en tu navegador. No sale de tu dispositivo.
- **Pago con PayPal (solo en la página de compra).** Si eliges pagar con PayPal, se carga el botón de PayPal, que puede usar sus propias cookies técnicas para completar el pago. Se rigen por la política de PayPal.
- **Panel de administración.** El panel, reservado al personal autorizado de C&S Company, usa una cookie de sesión y otra de protección de formularios, estrictamente necesarias, que caducan al cerrar la sesión o al poco tiempo.

## 4. Base legal

Las cookies y el almacenamiento descritos son técnicos o necesarios para el servicio que pides, por lo que no requieren tu consentimiento previo [POR CONFIRMAR en la revisión legal: normativa aplicable en Guatemala y en los países de los clientes].

## 5. Cómo gestionarlas

Puedes borrar las cookies y el almacenamiento local, o bloquearlos, desde la configuración de tu navegador. Si los bloqueas, el sitio sigue funcionando; el pago con PayPal podría no completarse.

## 6. Cambios

Si el sitio empieza a usar otras cookies, actualizaremos esta política antes de hacerlo y, si la ley lo exige, te pediremos el consentimiento. Para cualquier duda, escribe a info@kns.gt.
`

const acceptableUse = `## 1. Objeto

Esta política fija los usos permitidos y prohibidos de Horus Flow y de este sitio. Forma parte de las condiciones de la licencia de Horus Flow que otorga ${titular}

## 2. Uso permitido

Horus Flow está pensado para que un proveedor de Internet (ISP) vea el tráfico de su propia red, detecte equipos infectados o abusivos (botnets, escaneos, ataques) y decida qué hacer. El cliente solo debe conectar routers y redes que administre o sobre los que tenga autorización.

## 3. Usos prohibidos

- Vigilar comunicaciones de personas concretas o usar los datos de tráfico para fines distintos de la operación y la seguridad de la red.
- Analizar redes de terceros sin su autorización.
- Copiar, redistribuir, descompilar o hacer ingeniería inversa del software, o revenderlo como servicio a terceros, salvo autorización escrita de C&S Company.
- Intentar eludir la licencia, los controles de acceso o los límites del producto.
- Atacar, sobrecargar o probar la seguridad de este sitio o de la infraestructura de C&S Company fuera de lo previsto en la política de seguridad y divulgación responsable.
- Usar el asistente de inteligencia artificial para fines ilícitos o para intentar obtener datos que el usuario no tiene permiso para ver.

## 4. Responsabilidad del cliente

Horus Flow solo lee datos de tráfico y recomienda acciones; no ejecuta cambios en los routers. Cada ISP decide qué medidas aplica en su red y responde de ellas, y debe cumplir la normativa de protección de datos y de telecomunicaciones que le sea aplicable, incluida la información a sus abonados [POR CONFIRMAR en la revisión legal].

## 5. Incumplimiento

C&S Company puede suspender el soporte o resolver la licencia ante un incumplimiento grave de esta política, conforme al contrato de licencia [POR CONFIRMAR: procedimiento y plazos de aviso].

## 6. Contacto

Para avisar de un uso indebido, escribe a info@kns.gt.
`

const support = `## 1. Qué incluye

La licencia vigente de Horus Flow incluye soporte técnico 24/7 en todos los planes y las actualizaciones del producto, según el contrato de licencia de ${titular}

## 2. Canales

- Correo: info@kns.gt.
- Teléfono o WhatsApp de soporte: [POR CONFIRMAR].
- Las solicitudes se atienden en español; en inglés [POR CONFIRMAR].

## 3. Niveles de gravedad y tiempos de respuesta

Los tiempos siguientes son una propuesta pendiente de acordar [POR CONFIRMAR]; los que valen son los del contrato de licencia.

- **Crítica**: Horus Flow no funciona o no recibe datos de ningún router. Primera respuesta: [POR CONFIRMAR].
- **Alta**: una función importante falla y no hay alternativa. Primera respuesta: [POR CONFIRMAR].
- **Media**: un fallo con alternativa o que afecta a una parte del servicio. Primera respuesta: [POR CONFIRMAR].
- **Baja**: dudas, consultas de configuración y sugerencias. Primera respuesta: [POR CONFIRMAR].

## 4. Disponibilidad

Horus Flow se instala en los servidores del cliente: su disponibilidad depende también de ese hardware, de la red y de la configuración del cliente. C&S Company no garantiza un porcentaje de disponibilidad salvo que el contrato lo indique [POR CONFIRMAR].

## 5. Actualizaciones

Las correcciones de seguridad se publican como versiones de parche y se pueden aplicar con la herramienta de administración de Horus Flow o, si está activada, con la actualización automática de parches.

## 6. Qué no incluye

Salvo acuerdo, el soporte no incluye la administración de la red del cliente, la configuración de equipos de terceros ni la reparación del hardware del servidor [POR CONFIRMAR].

## 7. Cómo pedir ayuda

Indica el nombre del ISP, la versión de Horus Flow y una descripción del problema. No envíes datos de abonados ni capturas de tráfico sin anonimizar; el paquete de diagnóstico de Horus Flow ya enmascara IPs, correos y tokens.
`

const ai = `## 1. Resumen

Horus Flow incluye un asistente de inteligencia artificial (IA) **opcional** que ayuda a interpretar los datos de la red. Viene **desactivado** y cada ISP decide si lo activa. Esta página explica cómo trata los datos. El responsable del software es ${titular}

## 2. Quién elige el proveedor

Cada ISP elige el proveedor del modelo de IA:

- **Anthropic (Claude)**, con la clave de API propia del ISP. La clave se guarda cifrada en el servidor de Horus Flow del ISP.
- **Un servidor local compatible con la API de OpenAI** (por ejemplo, Ollama o vLLM) en la red del ISP. En ese caso ningún dato sale de la red del ISP.

Si el ISP usa Anthropic, la relación con ese proveedor y su tratamiento de los datos se rigen por las condiciones que el ISP acepte con él [POR CONFIRMAR en la revisión legal].

## 3. Qué datos ve la IA

La IA solo ve datos **enmascarados**:

- Las IP de los abonados se sustituyen por seudónimos como «Cliente 3 · 10.20.x.x».
- Nunca se le envían nombres, alias ni datos de contacto de los abonados.

## 4. Qué puede hacer la IA

- Consulta los datos con **herramientas de solo lectura** y con los mismos permisos que el usuario que pregunta: no puede ver nada que ese usuario no pueda ver.
- Sus respuestas se contrastan con los datos de Horus Flow y se marcan como «verificada» o «parcialmente verificada».
- **Horus Flow nunca ejecuta nada en los routers.** Si la IA propone una acción, la decide y la aplica una persona.

## 5. Registro y conservación

- Cada consulta queda registrada en la auditoría: usuario, proveedor, modelo, tokens usados y herramientas consultadas. La respuesta no se guarda en la auditoría.
- Los análisis se guardan **enmascarados** durante **90 días** por defecto (cada ISP puede cambiar el plazo), para volver a leerlos sin gastar consultas.
- Hay un límite diario de consultas por ISP.

## 6. Acceso desde Claude Desktop (MCP)

Horus Flow incluye un servidor MCP que permite consultar Horus desde Claude Desktop con un **token personal** de cada usuario. Se aplican los mismos permisos, el mismo enmascaramiento y la misma auditoría.

## 7. Tus derechos

Los abonados del ISP deben dirigirse a su ISP, que es quien decide sobre sus datos. Para cualquier duda sobre esta página, escribe a info@kns.gt.
`

const security = `## 1. Nuestro compromiso

Horus Flow es software de seguridad de red para ISPs y nos tomamos en serio cualquier vulnerabilidad. Esta política explica cómo avisarnos. El responsable es ${titular}

## 2. Cómo reportar una vulnerabilidad

Escribe a info@kns.gt con el asunto «[SEGURIDAD] Horus Flow» [POR CONFIRMAR: dirección dedicada y clave PGP]. No publiques detalles hasta que esté corregida.

Incluye, si puedes:

- la versión afectada y el modo de despliegue;
- la descripción del fallo y su impacto (por ejemplo, acceso entre ISPs, escalada de privilegios o exposición de datos de clientes);
- los pasos para reproducirlo o una prueba de concepto;
- si lo has visto explotado.

No envíes datos reales de abonados ni capturas de tráfico sin anonimizar.

## 3. Qué puedes esperar

- Acuse de recibo: 3 días hábiles.
- Primera evaluación (gravedad y alcance): 10 días hábiles.
- Corrección de vulnerabilidades críticas o altas: lo antes posible, en una versión de parche.

Te mantendremos informado y, si lo deseas, te reconoceremos en las notas de la versión que lo corrige.

## 4. Alcance

Entran en el alcance el software Horus Flow (servidor, interfaz web, instalador y herramientas de administración), sus imágenes publicadas y este sitio web. Las vulnerabilidades de componentes de terceros repórtalas a su proyecto y avísanos también si afectan a Horus Flow.

## 5. Buenas prácticas al investigar

- No accedas, modifiques ni borres datos que no sean tuyos; si te encuentras datos de terceros, detente y avísanos.
- No hagas pruebas de denegación de servicio, ingeniería social ni pruebas físicas.
- Prueba solo en instalaciones propias o con autorización de su titular.

Si actúas de buena fe y sigues esta política, no emprenderemos acciones legales contra ti por tu investigación [POR CONFIRMAR en la revisión legal].
`

export const SEED_PAGES: SeedPage[] = [
  {
    id: 'notice',
    kind: 'legal',
    slugEs: 'aviso-legal',
    slugEn: 'notice',
    titleEs: legalDocs.notice.title,
    titleEn: 'Legal notice',
    summaryEs:
      'Titular del sitio de Horus Flow, propiedad intelectual, responsabilidad y ley aplicable.',
    summaryEn:
      'Owner of the Horus Flow website, intellectual property, liability and governing law.',
    bodyEs: legalDocToMarkdown(legalDocs.notice),
    published: true,
    showInFooter: true,
    draftNotice: true,
    sort: 10,
    updatedAt: ORIGINAL,
  },
  {
    id: 'privacy',
    kind: 'legal',
    slugEs: 'privacidad',
    slugEn: 'privacy',
    titleEs: legalDocs.privacy.title,
    titleEn: 'Privacy policy',
    summaryEs:
      'Qué datos recoge el sitio de Horus Flow, para qué, con quién se comparten y tus derechos.',
    summaryEn:
      'What data the Horus Flow website collects, why, who it is shared with and your rights.',
    bodyEs: legalDocToMarkdown(legalDocs.privacy),
    published: true,
    showInFooter: true,
    draftNotice: true,
    sort: 20,
    updatedAt: ORIGINAL,
  },
  {
    id: 'terms',
    kind: 'legal',
    slugEs: 'terminos',
    slugEn: 'terms',
    titleEs: legalDocs.terms.title,
    titleEn: 'Terms and conditions',
    summaryEs:
      'Condiciones del sitio, de las solicitudes de compra y de la licencia de Horus Flow.',
    summaryEn: 'Conditions of the website, purchase requests and the Horus Flow licence.',
    bodyEs: legalDocToMarkdown(legalDocs.terms),
    published: true,
    showInFooter: true,
    draftNotice: true,
    sort: 30,
    updatedAt: ORIGINAL,
  },
  {
    id: 'cookies',
    kind: 'legal',
    slugEs: 'cookies',
    slugEn: 'cookies',
    titleEs: 'Política de cookies',
    titleEn: 'Cookie policy',
    summaryEs:
      'El sitio de Horus Flow no usa cookies de seguimiento: qué guarda en tu navegador y por qué.',
    summaryEn:
      'The Horus Flow website uses no tracking cookies: what it stores in your browser and why.',
    bodyEs: cookies,
    published: true,
    showInFooter: true,
    draftNotice: true,
    sort: 40,
    updatedAt: NEW_DRAFTS,
  },
  {
    id: 'acceptable-use',
    kind: 'legal',
    slugEs: 'uso-aceptable',
    slugEn: 'acceptable-use',
    titleEs: 'Política de uso aceptable',
    titleEn: 'Acceptable use policy',
    summaryEs: 'Usos permitidos y prohibidos de Horus Flow y de este sitio.',
    summaryEn: 'Permitted and prohibited uses of Horus Flow and this website.',
    bodyEs: acceptableUse,
    published: true,
    showInFooter: true,
    draftNotice: true,
    sort: 50,
    updatedAt: NEW_DRAFTS,
  },
  {
    id: 'support',
    kind: 'legal',
    slugEs: 'soporte',
    slugEn: 'support',
    titleEs: 'Soporte y niveles de servicio',
    titleEn: 'Support and service levels',
    summaryEs:
      'Soporte técnico 24/7 de Horus Flow: canales, niveles de gravedad y actualizaciones.',
    summaryEn: 'Horus Flow 24/7 technical support: channels, severity levels and updates.',
    bodyEs: support,
    published: true,
    showInFooter: true,
    draftNotice: true,
    sort: 60,
    updatedAt: NEW_DRAFTS,
  },
  {
    id: 'ai',
    kind: 'legal',
    slugEs: 'inteligencia-artificial',
    slugEn: 'ai',
    titleEs: 'Inteligencia artificial y tratamiento de datos',
    titleEn: 'Artificial intelligence and data processing',
    summaryEs:
      'Asistente de IA opcional de Horus Flow: proveedor elegido por el ISP, datos enmascarados, solo lectura y auditoría.',
    summaryEn:
      'Optional Horus Flow AI assistant: provider chosen by the ISP, masked data, read-only and audited.',
    bodyEs: ai,
    published: true,
    showInFooter: true,
    draftNotice: true,
    sort: 70,
    updatedAt: NEW_DRAFTS,
  },
  {
    id: 'security',
    kind: 'legal',
    slugEs: 'seguridad',
    slugEn: 'security',
    titleEs: 'Seguridad y divulgación responsable',
    titleEn: 'Security and responsible disclosure',
    summaryEs:
      'Cómo reportar una vulnerabilidad de Horus Flow o de este sitio y qué puedes esperar.',
    summaryEn: 'How to report a vulnerability in Horus Flow or this website and what to expect.',
    bodyEs: security,
    published: true,
    showInFooter: true,
    draftNotice: true,
    sort: 80,
    updatedAt: NEW_DRAFTS,
  },
]

const q = (s: string) => `'${s.replaceAll("'", "''")}'`

/** Instantánea de una página (mismo formato que server/lib/pages.ts guarda en page_versions). */
function snapshot(p: SeedPage) {
  return {
    id: p.id,
    kind: p.kind,
    slug: { es: p.slugEs, en: p.slugEn },
    title: { es: p.titleEs, en: p.titleEn },
    summary: { es: p.summaryEs, en: p.summaryEn },
    body: { es: p.bodyEs, en: '' },
    published: p.published,
    showInFooter: p.showInFooter,
    draftNotice: p.draftNotice,
    sort: p.sort,
    createdAt: p.updatedAt,
    updatedAt: p.updatedAt,
    updatedBy: 'sistema',
  }
}

/** INSERT de la semilla (páginas + primera versión de cada una). */
export function seedPagesSql(pages: SeedPage[] = SEED_PAGES): string {
  return pages
    .map(
      (p) =>
        `INSERT INTO pages (id, kind, slug_es, slug_en, title_es, title_en, summary_es, summary_en,
           body_es, body_en, published, show_in_footer, draft_notice, sort, created_at, updated_at, updated_by)
         VALUES (${[
           q(p.id),
           q(p.kind),
           q(p.slugEs),
           q(p.slugEn),
           q(p.titleEs),
           q(p.titleEn),
           q(p.summaryEs),
           q(p.summaryEn),
           q(p.bodyEs),
           q(''),
           p.published ? 1 : 0,
           p.showInFooter ? 1 : 0,
           p.draftNotice ? 1 : 0,
           p.sort,
           q(p.updatedAt),
           q(p.updatedAt),
           q('sistema'),
         ].join(', ')});
         INSERT INTO page_versions (page_id, created_at, admin_id, admin_email, note, restored_from, snapshot)
         VALUES (${q(p.id)}, ${q(p.updatedAt)}, NULL, 'sistema', 'Semilla inicial (migración 2)', NULL, ${q(JSON.stringify(snapshot(p)))});`,
    )
    .join('\n')
}
