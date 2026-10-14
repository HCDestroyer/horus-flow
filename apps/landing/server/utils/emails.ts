// Plantillas de correo: aviso a ventas (en español) y confirmación al cliente (en su idioma),
// instrucciones de pago (link Neo o transferencia) y aviso de pago confirmado.
// Texto plano + HTML mínimo con todo escapado.
import type { Catalog, Localized } from '../../shared/catalog'
import type { Lead, Purchase } from '../../shared/schemas'
import type { MailMessage } from './mailer'

const SELLER_LINE =
  'Connection And Solutions Company, Sociedad Anónima (C&S Company) · San Martín Jilotepeque, Chimaltenango, Guatemala'

const CLIENTS: Record<Lead['clients'], { es: string; en: string }> = {
  lt300: { es: 'Hasta 300', en: 'Up to 300' },
  '300to2000': { es: 'De 300 a 2 000', en: '300 to 2,000' },
  '2000to10000': { es: 'De 2 000 a 10 000', en: '2,000 to 10,000' },
  gt10000: { es: 'Más de 10 000', en: 'More than 10,000' },
}

const ROUTERS: Record<Lead['routers'], { es: string; en: string }> = {
  '1': { es: '1', en: '1' },
  '2to5': { es: 'De 2 a 5', en: '2 to 5' },
  '6to20': { es: 'De 6 a 20', en: '6 to 20' },
  gt20: { es: 'Más de 20', en: 'More than 20' },
}

const PERIOD = {
  monthly: { es: 'Mensual', en: 'Monthly' },
  annual: { es: 'Anual', en: 'Annual' },
}

export function countryName(code: string, locale: 'es' | 'en'): string {
  if (code === 'ZZ') return locale === 'es' ? 'Otro país' : 'Other country'
  try {
    return new Intl.DisplayNames([locale], { type: 'region' }).of(code) ?? code
  } catch {
    return code
  }
}

export function escapeHtml(s: string): string {
  return s
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;')
}

/** Quita saltos de línea de lo que va en un asunto (evita inyección de cabeceras). */
function oneLine(s: string, max = 80): string {
  return s.replace(/[\r\n]+/g, ' ').slice(0, max)
}

function render(
  title: string,
  rows: [string, string][],
  footer: string[],
): { text: string; html: string } {
  const text = [title, '', ...rows.map(([k, v]) => `${k}: ${v || '—'}`), '', ...footer].join('\n')
  const html =
    `<!doctype html><html><body style="font-family:system-ui,sans-serif;color:#111;line-height:1.5">` +
    `<h1 style="font-size:18px">${escapeHtml(title)}</h1>` +
    `<table cellpadding="6" style="border-collapse:collapse">` +
    rows
      .map(
        ([k, v]) =>
          `<tr><th align="left" style="vertical-align:top;color:#555;font-weight:600">${escapeHtml(k)}</th>` +
          `<td style="white-space:pre-wrap">${escapeHtml(v || '—')}</td></tr>`,
      )
      .join('') +
    `</table>` +
    footer.map((f) => `<p style="color:#555">${escapeHtml(f)}</p>`).join('') +
    `</body></html>`
  return { text, html }
}

/** Nombre del plan en el catálogo vigente (o el id si no existe). */
export function planName(catalog: Catalog | null, id: string, locale: 'es' | 'en'): string {
  return catalog?.plans.find((p) => p.id === id)?.name[locale] ?? id
}

export function amountText(amount: number | null, currency: string, locale: 'es' | 'en'): string {
  if (amount === null) return locale === 'es' ? 'A cotizar' : 'To be quoted'
  return new Intl.NumberFormat(locale === 'es' ? 'es-GT' : 'en-US', {
    style: 'currency',
    currency,
    minimumFractionDigits: Number.isInteger(amount) ? 0 : 2,
    maximumFractionDigits: 2,
  }).format(amount)
}

export function leadSalesEmail(lead: Lead, reference: string, to: string): MailMessage {
  const kind = lead.kind === 'demo' ? 'Solicitud de demo' : 'Contacto'
  const { text, html } = render(
    `${kind} ${reference}`,
    [
      ['Referencia', reference],
      ['Nombre', lead.name],
      ['Empresa / ISP', lead.company],
      ['País', countryName(lead.country, 'es')],
      ['Clientes', CLIENTS[lead.clients].es],
      ['Routers MikroTik', ROUTERS[lead.routers].es],
      ['Correo', lead.email],
      ['Teléfono / WhatsApp', lead.phone],
      ['Mensaje', lead.message],
      ['Idioma de la página', lead.locale],
      ['Consentimiento de privacidad', 'Sí'],
    ],
    ['Enviado desde la landing de Horus Flow. Responde a este correo para escribir al cliente.'],
  )
  return {
    tag: 'sales',
    to,
    replyTo: lead.email,
    subject: `[Horus Flow] ${kind} · ${oneLine(lead.company)} · ${reference}`,
    text,
    html,
  }
}

export function leadCustomerEmail(lead: Lead, reference: string, salesEmail: string): MailMessage {
  const es = lead.locale === 'es'
  const { text, html } = render(
    es
      ? `Hemos recibido tu solicitud (${reference})`
      : `We have received your request (${reference})`,
    [
      [es ? 'Referencia' : 'Reference', reference],
      [es ? 'Empresa / ISP' : 'Company / ISP', lead.company],
      [es ? 'Clientes' : 'Subscribers', CLIENTS[lead.clients][lead.locale]],
      [es ? 'Routers MikroTik' : 'MikroTik routers', ROUTERS[lead.routers][lead.locale]],
    ],
    [
      es
        ? `Gracias, ${lead.name}. Te escribiremos pronto para agendar la demo. Si necesitas algo antes, responde a este correo o escribe a ${salesEmail}.`
        : `Thank you, ${lead.name}. We will be in touch soon to schedule the demo. If you need anything sooner, reply to this email or write to ${salesEmail}.`,
      SELLER_LINE,
    ],
  )
  return {
    tag: 'customer',
    to: lead.email,
    replyTo: salesEmail,
    subject: es
      ? `Horus Flow: solicitud recibida (${reference})`
      : `Horus Flow: request received (${reference})`,
    text,
    html,
  }
}

export function purchaseSalesEmail(
  p: Purchase,
  reference: string,
  amount: number | null,
  plan: string,
  to: string,
): MailMessage {
  const { text, html } = render(
    `Solicitud de compra ${reference}`,
    [
      ['Referencia', reference],
      ['Plan', plan],
      ['Periodo', PERIOD[p.period].es],
      ['Moneda', p.currency],
      ['Importe', amountText(amount, p.currency, 'es')],
      ['Razón social', p.legalName],
      ['NIT', p.nit],
      ['País', countryName(p.country, 'es')],
      ['Dirección de facturación', p.address],
      ['Contacto', p.contactName],
      ['Correo', p.email],
      ['Teléfono / WhatsApp', p.phone],
      ['Comentarios', p.notes],
      ['Idioma de la página', p.locale],
      ['Acepta términos y privacidad', 'Sí'],
    ],
    [
      'Solicitud enviada desde la landing de Horus Flow. El cliente elige ahora el método de pago; el estado se sigue en el panel (/admin › Solicitudes).',
    ],
  )
  return {
    tag: 'sales',
    to,
    replyTo: p.email,
    subject: `[Horus Flow] Compra · ${oneLine(plan)} · ${oneLine(p.legalName)} · ${reference}`,
    text,
    html,
  }
}

export function purchaseCustomerEmail(
  p: Purchase,
  reference: string,
  amount: number | null,
  plan: string,
  salesEmail: string,
): MailMessage {
  const es = p.locale === 'es'
  let body: string
  if (amount === null) {
    body = es
      ? `Gracias, ${p.contactName}. Ventas revisará tu solicitud y te enviará la cotización. No se ha cobrado nada.`
      : `Thank you, ${p.contactName}. Sales will review your request and send you a quote. Nothing has been charged.`
  } else {
    body = es
      ? `Gracias, ${p.contactName}. Puedes pagar con PayPal, con link de pago Neo o por transferencia desde la página de compra. Si pagas por link Neo o transferencia, indica la referencia ${reference}. No se ha cobrado nada hasta que confirmemos el pago.`
      : `Thank you, ${p.contactName}. You can pay with PayPal, a Neo payment link or a bank transfer from the purchase page. If you pay by Neo link or transfer, include the reference ${reference}. Nothing has been charged until we confirm the payment.`
  }
  const { text, html } = render(
    es ? `Solicitud de compra ${reference}` : `Purchase request ${reference}`,
    [
      [es ? 'Referencia' : 'Reference', reference],
      ['Plan', plan],
      [es ? 'Periodo' : 'Billing period', PERIOD[p.period][p.locale]],
      [es ? 'Importe' : 'Amount', amountText(amount, p.currency, p.locale)],
      [es ? 'Razón social' : 'Legal name', p.legalName],
    ],
    [
      body,
      es ? `¿Dudas? Escribe a ${salesEmail}.` : `Questions? Write to ${salesEmail}.`,
      SELLER_LINE,
    ],
  )
  return {
    tag: 'customer',
    to: p.email,
    replyTo: salesEmail,
    subject: es
      ? `Horus Flow: solicitud de compra ${reference}`
      : `Horus Flow: purchase request ${reference}`,
    text,
    html,
  }
}

// ---- Pagos ---------------------------------------------------------------------------------

export interface PurchaseSummary {
  reference: string
  locale: 'es' | 'en'
  contactName: string
  email: string
  company: string
  plan: string
  period: 'monthly' | 'annual'
  currency: string
  amount: number | null
}

export interface BankAccount {
  bank: string
  type: Localized
  number: string
  holder: string
  currency: string
}

export type InstructionsMethod =
  | { kind: 'transfer'; accounts: BankAccount[]; instructions: Localized }
  | { kind: 'neo'; url: string }

/** Instrucciones de pago al cliente: transferencia (cuentas) o link Neo. */
export function paymentInstructionsEmail(
  p: PurchaseSummary,
  method: InstructionsMethod,
  salesEmail: string,
): MailMessage {
  const es = p.locale === 'es'
  const rows: [string, string][] = [
    [es ? 'Referencia' : 'Reference', p.reference],
    ['Plan', `${p.plan} · ${PERIOD[p.period][p.locale]}`],
    [es ? 'Importe' : 'Amount', amountText(p.amount, p.currency, p.locale)],
  ]
  const footer: string[] = []
  if (method.kind === 'transfer') {
    method.accounts.forEach((a, i) => {
      const n = method.accounts.length > 1 ? ` ${i + 1}` : ''
      rows.push([
        (es ? 'Cuenta' : 'Account') + n,
        [
          a.bank,
          `${a.type[p.locale] || a.type.es} · ${a.currency}`,
          `${es ? 'Número' : 'Number'}: ${a.number}`,
          `${es ? 'Titular' : 'Holder'}: ${a.holder}`,
        ].join('\n'),
      ])
    })
    const extra = method.instructions[p.locale] || method.instructions.es
    if (extra) footer.push(extra)
    footer.push(
      es
        ? `Indica la referencia ${p.reference} en el concepto de la transferencia y envíanos el comprobante a ${salesEmail}. Confirmaremos el pago al recibirlo.`
        : `Include the reference ${p.reference} as the transfer description and send the receipt to ${salesEmail}. We will confirm the payment once received.`,
    )
  } else {
    rows.push([es ? 'Link de pago Neo' : 'Neo payment link', method.url])
    footer.push(
      es
        ? `Abre el link de pago Neo e indica la referencia ${p.reference}. Confirmaremos el pago al recibirlo.`
        : `Open the Neo payment link and include the reference ${p.reference}. We will confirm the payment once received.`,
    )
  }
  footer.push(SELLER_LINE)
  const { text, html } = render(
    es ? `Instrucciones de pago (${p.reference})` : `Payment instructions (${p.reference})`,
    rows,
    footer,
  )
  return {
    tag: 'customer',
    to: p.email,
    replyTo: salesEmail,
    subject: es
      ? `Horus Flow: instrucciones de pago ${p.reference}`
      : `Horus Flow: payment instructions ${p.reference}`,
    text,
    html,
  }
}

export interface PaidInfo {
  amount: number
  currency: string
  method: string
  transactionId: string
}

/** Pago confirmado: al cliente (en su idioma). */
export function paymentConfirmedCustomerEmail(
  p: PurchaseSummary,
  paid: PaidInfo,
  salesEmail: string,
): MailMessage {
  const es = p.locale === 'es'
  const { text, html } = render(
    es ? `Pago recibido (${p.reference})` : `Payment received (${p.reference})`,
    [
      [es ? 'Referencia' : 'Reference', p.reference],
      ['Plan', `${p.plan} · ${PERIOD[p.period][p.locale]}`],
      [es ? 'Importe pagado' : 'Amount paid', amountText(paid.amount, paid.currency, p.locale)],
      [es ? 'Método' : 'Method', paid.method],
    ],
    [
      es
        ? `Gracias, ${p.contactName}. Hemos recibido tu pago. Te escribiremos para formalizar la licencia y ayudarte con la instalación. Soporte 24/7: ${salesEmail}.`
        : `Thank you, ${p.contactName}. We have received your payment. We will contact you to finalize the license and help with the installation. 24/7 support: ${salesEmail}.`,
      SELLER_LINE,
    ],
  )
  return {
    tag: 'customer',
    to: p.email,
    replyTo: salesEmail,
    subject: es
      ? `Horus Flow: pago recibido ${p.reference}`
      : `Horus Flow: payment received ${p.reference}`,
    text,
    html,
  }
}

/** Pago confirmado: aviso a ventas. */
export function paymentConfirmedSalesEmail(
  p: PurchaseSummary,
  paid: PaidInfo,
  to: string,
): MailMessage {
  const { text, html } = render(
    `Pago confirmado ${p.reference}`,
    [
      ['Referencia', p.reference],
      ['Empresa', p.company],
      ['Plan', `${p.plan} · ${PERIOD[p.period].es}`],
      ['Importe pagado', amountText(paid.amount, paid.currency, 'es')],
      ['Método', paid.method],
      ['Transacción', paid.transactionId],
      ['Cliente', `${p.contactName} <${p.email}>`],
    ],
    ['La solicitud ya figura como "Pagada" en el panel (/admin › Solicitudes).'],
  )
  return {
    tag: 'sales',
    to,
    replyTo: p.email,
    subject: `[Horus Flow] Pago confirmado · ${oneLine(p.company)} · ${p.reference}`,
    text,
    html,
  }
}
