// ⚠️ BORRADORES PENDIENTES DE REVISIÓN LEGAL. Redactados como punto de partida; un abogado
// debe revisarlos (ley aplicable, plazos de conservación, datos registrales) antes de publicar
// la landing en producción. Los textos marcados [POR CONFIRMAR] requieren un dato de C&S Company.
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
  'Connection And Solutions Company, Sociedad Anónima ("C&S Company"), con domicilio en 2da avenida, San Martín Jilotepeque, Chimaltenango, Guatemala; inscrita en el Registro Mercantil [POR CONFIRMAR: número de registro], NIT [POR CONFIRMAR]; correo info@kns.gt.'

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
          'Solo los que nos das en los formularios: nombre, empresa o ISP, país, número aproximado de clientes y de routers, correo, teléfono o WhatsApp (opcional) y tu mensaje; en una solicitud de compra, además, razón social, NIT (opcional), dirección de facturación (opcional) y comentarios, y el estado del pago (método elegido, referencia e identificadores de la operación de PayPal si pagas con PayPal).',
          'Las solicitudes se guardan en la base de datos del servidor del sitio, a la que solo accede personal autorizado de C&S Company con usuario, contraseña y segundo factor. No recibimos ni guardamos datos de tarjeta: si pagas con PayPal o con un link de pago Neo, los introduces en la página de ese proveedor.',
          'Para proteger los formularios frente al abuso, el servidor usa tu dirección IP en memoria durante unos minutos (límite de envíos). No la guarda junto a tu solicitud y los registros técnicos solo conservan una versión parcial.',
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
          'Si eliges pagar con PayPal, PayPal recibe el importe, la referencia de la solicitud y los datos que tú le des en su página. Si pagas con un link de pago Neo o por transferencia, la entidad que procesa el pago recibe los datos de la operación. Cada uno trata esos datos según su propia política de privacidad.',
        ],
      },
      {
        title: '6. Cuánto tiempo los guardamos',
        paragraphs: [
          'Mientras dure la relación comercial y, después, durante los plazos que exija la ley [POR CONFIRMAR]. Si no llegamos a una relación comercial, los borramos a los [POR CONFIRMAR] meses.',
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
          'En la página de compra, si eliges PayPal, se carga el botón de PayPal, que puede usar sus propias cookies técnicas para completar el pago. El panel de administración, reservado al personal de C&S Company, usa una cookie de sesión estrictamente necesaria.',
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
          'Enviar una solicitud de compra genera un número de referencia y no supone ningún cobro por sí misma. El pago se hace después, con el método que elijas. La licencia se formaliza con un contrato de licencia escrito, que prevalece sobre estos términos.',
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
          'Los precios publicados en el sitio están en USD y en GTQ, por periodo mensual o anual, y son los vigentes en el momento de la solicitud. El importe que se cobra es siempre el que calcula el servidor a partir de los precios publicados, nunca el que envíe el navegador.',
          'Medios de pago: PayPal (en USD, el importe en dólares del plan elegido), link de pago Neo y transferencia bancaria a las cuentas que se indican en la página de compra y en el correo de instrucciones. En los pagos por link Neo o por transferencia hay que indicar la referencia de la solicitud; C&S Company confirma el pago al recibirlo.',
          'Impuestos y facturación [POR CONFIRMAR: si los precios incluyen IVA y cómo se emite la factura electrónica (FEL)]. Política de devoluciones [POR CONFIRMAR].',
        ],
      },
      {
        title: '5. Soporte y actualizaciones',
        paragraphs: [
          'La licencia vigente incluye soporte técnico 24/7 en todos los planes y las actualizaciones del producto. Los canales de soporte y, en su caso, los tiempos de respuesta son los que se indican en el sitio y en el contrato de licencia.',
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
