// ⚠️ BORRADORES PENDIENTES DE REVISIÓN LEGAL. Redactados como punto de partida; un abogado
// debe revisarlos (ley aplicable, plazos de conservación, datos del titular) antes de publicar
// la landing en producción. Los textos marcados [POR DEFINIR] requieren un dato de C&S Company.
// Solo en español mientras dure la revisión (la versión en inglés muestra este mismo texto).

export interface LegalSection {
  title: string
  paragraphs: string[]
}

export interface LegalDoc {
  title: string
  updated: string
  sections: LegalSection[]
}

const titular =
  'Connection And Solutions Company, Sociedad Anónima ("C&S Company"), con domicilio en Guatemala [POR DEFINIR: dirección y datos registrales], correo info@kns.gt.'

export const legalDocs: Record<'notice' | 'privacy' | 'terms', LegalDoc> = {
  notice: {
    title: 'Aviso legal',
    updated: '2026-10-10',
    sections: [
      {
        title: '1. Titular del sitio',
        paragraphs: [`Este sitio web pertenece a ${titular}`],
      },
      {
        title: '2. Objeto',
        paragraphs: [
          'El sitio informa sobre Horus Flow, software de visibilidad de tráfico y detección de botnets para proveedores de Internet, y permite solicitar demostraciones, cotizaciones y licencias.',
        ],
      },
      {
        title: '3. Propiedad intelectual',
        paragraphs: [
          'Horus Flow es software propietario de C&S Company. Los textos, imágenes, marcas y demás contenidos del sitio están protegidos por la legislación de propiedad intelectual. No se concede ninguna licencia sobre ellos salvo la necesaria para consultar el sitio.',
          'Las capturas de pantalla del producto muestran datos simulados: no corresponden a ningún proveedor ni cliente real.',
        ],
      },
      {
        title: '4. Responsabilidad',
        paragraphs: [
          'La información del sitio es orientativa y puede cambiar sin previo aviso. Las cifras de rendimiento proceden de pruebas en un servidor de pruebas y se indican con sus condiciones; no constituyen una garantía de resultado en otra instalación.',
        ],
      },
      {
        title: '5. Ley aplicable',
        paragraphs: [
          'Este aviso se rige por las leyes de la República de Guatemala [POR CONFIRMAR en la revisión legal].',
        ],
      },
    ],
  },

  privacy: {
    title: 'Política de privacidad',
    updated: '2026-10-10',
    sections: [
      {
        title: '1. Responsable',
        paragraphs: [`El responsable del tratamiento es ${titular}`],
      },
      {
        title: '2. Qué datos recogemos',
        paragraphs: [
          'Solo los que nos das en los formularios: nombre, empresa o ISP, país, número aproximado de clientes y de routers, correo, teléfono o WhatsApp (opcional) y tu mensaje; en una solicitud de compra, además, razón social, NIT (opcional), dirección de facturación (opcional) y comentarios.',
          'Para proteger los formularios frente al abuso, el servidor usa tu dirección IP en memoria durante unos minutos (límite de envíos). No la guarda en una base de datos y los registros técnicos solo conservan una versión parcial.',
        ],
      },
      {
        title: '3. Para qué los usamos',
        paragraphs: [
          'Para responder a tu solicitud, preparar la demostración o la cotización, tramitar la licencia y facturarla. No usamos tus datos para publicidad de terceros ni los vendemos.',
        ],
      },
      {
        title: '4. Base legal',
        paragraphs: [
          'Tu consentimiento, que das al enviar el formulario, y la aplicación de medidas precontractuales a petición tuya [POR CONFIRMAR en la revisión legal].',
        ],
      },
      {
        title: '5. Con quién los compartimos',
        paragraphs: [
          'Con el proveedor de correo electrónico que usamos para recibir y responder las solicitudes y, si C&S Company lo configura, con su herramienta de gestión de clientes (CRM). Ambos actúan por cuenta de C&S Company.',
        ],
      },
      {
        title: '6. Cuánto tiempo los guardamos',
        paragraphs: [
          'Mientras dure la relación comercial y, después, durante los plazos que exija la ley [POR DEFINIR]. Si no llegamos a una relación comercial, los borramos a los [POR DEFINIR] meses.',
        ],
      },
      {
        title: '7. Tus derechos',
        paragraphs: [
          'Puedes pedir acceso, rectificación o supresión de tus datos, u oponerte a su uso, escribiendo a info@kns.gt.',
        ],
      },
      {
        title: '8. Cookies y almacenamiento local',
        paragraphs: [
          'El sitio no usa cookies de seguimiento ni herramientas de analítica de terceros. Puede guardar en tu navegador la preferencia de apariencia (clara u oscura) que sigue a la de tu sistema.',
        ],
      },
    ],
  },

  terms: {
    title: 'Términos y condiciones',
    updated: '2026-10-10',
    sections: [
      {
        title: '1. Objeto',
        paragraphs: [
          'Estos términos regulan el uso de este sitio y el envío de solicitudes de demostración, cotización y compra de licencias de Horus Flow a C&S Company.',
        ],
      },
      {
        title: '2. Las solicitudes no son un contrato',
        paragraphs: [
          'Enviar una solicitud de compra genera un número de referencia y no supone ningún cobro ni compromiso para ninguna de las partes. La licencia solo existe cuando ambas partes firman un contrato de licencia escrito, que prevalece sobre estos términos.',
        ],
      },
      {
        title: '3. Licencia',
        paragraphs: [
          'Horus Flow es software propietario. Se licencia por instalación, en el plan y con las condiciones que fije el contrato. Sin contrato no se concede ningún derecho de uso. Quedan prohibidas la copia, la redistribución, la ingeniería inversa y su uso como servicio para terceros, salvo autorización escrita.',
        ],
      },
      {
        title: '4. Precios y pago',
        paragraphs: [
          'Los precios se comunican en la cotización, en USD o en GTQ. Mientras el sitio indique "Precio de lanzamiento: solicita cotización", las cifras no son públicas. El pago se hace por transferencia bancaria una vez aceptada la cotización, indicando la referencia de la solicitud [POR CONFIRMAR: impuestos, facturación y otros medios de pago].',
        ],
      },
      {
        title: '5. Soporte y actualizaciones',
        paragraphs: [
          'La licencia vigente incluye soporte técnico por correo y las actualizaciones del producto. El alcance del soporte (horarios, tiempos de respuesta) se fija en el contrato.',
        ],
      },
      {
        title: '6. Responsabilidad',
        paragraphs: [
          'Horus Flow solo lee los datos de tráfico y recomienda acciones; las decisiones y los cambios en la red del cliente son responsabilidad de quien los aplica. Las limitaciones de responsabilidad aplicables serán las del contrato de licencia.',
        ],
      },
      {
        title: '7. Ley aplicable y jurisdicción',
        paragraphs: [
          'Estos términos se rigen por las leyes de la República de Guatemala, y cualquier controversia se someterá a los tribunales de la Ciudad de Guatemala [POR CONFIRMAR en la revisión legal].',
        ],
      },
    ],
  },
}
