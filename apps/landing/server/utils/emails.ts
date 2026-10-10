// Plantillas de correo: aviso a ventas (en español) y confirmación al cliente (en su idioma).
// Texto plano + HTML mínimo con todo escapado.
import { pricing } from '../../app/config/pricing'
import type { Lead, Purchase } from '../../shared/schemas'
import type { MailMessage } from './mailer'

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

function render(title: string, rows: [string, string][], footer: string[]): { text: string; html: string } {
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

function planName(id: Purchase['plan'], locale: 'es' | 'en'): string {
  return pricing.plans.find((p) => p.id === id)?.name[locale] ?? id
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
    es ? `Hemos recibido tu solicitud (${reference})` : `We have received your request (${reference})`,
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
      'Connection And Solutions Company, Sociedad Anónima (C&S Company) · Guatemala',
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

function amountText(amount: number | null, currency: string, locale: 'es' | 'en'): string {
  if (amount === null) return locale === 'es' ? 'A cotizar' : 'To be quoted'
  return new Intl.NumberFormat(locale === 'es' ? 'es-GT' : 'en-US', {
    style: 'currency',
    currency,
    maximumFractionDigits: 0,
  }).format(amount)
}

export function purchaseSalesEmail(
  p: Purchase,
  reference: string,
  amount: number | null,
  provider: string,
  to: string,
): MailMessage {
  const { text, html } = render(
    `Solicitud de compra ${reference}`,
    [
      ['Referencia', reference],
      ['Plan', planName(p.plan, 'es')],
      ['Periodo', PERIOD[p.period].es],
      ['Moneda', p.currency],
      ['Importe', amountText(amount, p.currency, 'es')],
      ['Precios confirmados', pricing.confirmed ? 'Sí' : 'No (enviar cotización)'],
      ['Proveedor de pago', provider],
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
    ['Solicitud enviada desde la landing de Horus Flow. No se ha cobrado nada.'],
  )
  return {
    tag: 'sales',
    to,
    replyTo: p.email,
    subject: `[Horus Flow] Compra · ${planName(p.plan, 'es')} · ${oneLine(p.legalName)} · ${reference}`,
    text,
    html,
  }
}

export function purchaseCustomerEmail(
  p: Purchase,
  reference: string,
  amount: number | null,
  salesEmail: string,
): MailMessage {
  const es = p.locale === 'es'
  const { text, html } = render(
    es ? `Solicitud de compra ${reference}` : `Purchase request ${reference}`,
    [
      [es ? 'Referencia' : 'Reference', reference],
      ['Plan', planName(p.plan, p.locale)],
      [es ? 'Periodo' : 'Billing period', PERIOD[p.period][p.locale]],
      [es ? 'Importe' : 'Amount', amountText(amount, p.currency, p.locale)],
      [es ? 'Razón social' : 'Legal name', p.legalName],
    ],
    [
      es
        ? `Gracias, ${p.contactName}. Ventas revisará tu solicitud y te enviará la cotización y los datos para la transferencia. Usa la referencia ${reference} en el concepto del pago. No se ha cobrado nada.`
        : `Thank you, ${p.contactName}. Sales will review your request and send you the quote and bank transfer details. Use the reference ${reference} as the payment description. Nothing has been charged.`,
      es ? `¿Dudas? Escribe a ${salesEmail}.` : `Questions? Write to ${salesEmail}.`,
      'Connection And Solutions Company, Sociedad Anónima (C&S Company) · Guatemala',
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
