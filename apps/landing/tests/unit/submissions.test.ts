import { mkdtemp, readdir, readFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { loadConfig, type ServerConfig } from '../../server/utils/config'
import { createMailer, type MailMessage, type Mailer } from '../../server/utils/mailer'
import { RateLimiter } from '../../server/utils/rate-limit'
import { REFERENCE_RE } from '../../server/utils/reference'
import { submitLead, submitPurchase, type SubmissionDeps } from '../../server/utils/submissions'
import { signBody } from '../../server/utils/webhook'

const NOW = Date.UTC(2026, 9, 10, 12, 0, 0)
const IP = '203.0.113.7'

const lead = (over: Record<string, unknown> = {}) => ({
  kind: 'demo',
  name: 'Ana Pérez',
  company: 'Fibra Sur',
  country: 'GT',
  clients: '300to2000',
  routers: '2to5',
  email: 'ana@fibrasur.example',
  phone: '+502 5555 1234',
  message: 'Tenemos 3 nodos.',
  consent: true,
  locale: 'es',
  website: '',
  startedAt: NOW - 10_000,
  ...over,
})

const purchase = (over: Record<string, unknown> = {}) => ({
  plan: 'medium',
  period: 'annual',
  currency: 'GTQ',
  legalName: 'Fibra Sur, S.A.',
  nit: '1234567-8',
  country: 'GT',
  address: '',
  contactName: 'Ana Pérez',
  email: 'ana@fibrasur.example',
  phone: '',
  notes: '',
  consent: true,
  locale: 'en',
  website: '',
  startedAt: NOW - 10_000,
  ...over,
})

function fakeMailer(mode: Mailer['mode'] = 'smtp', fail?: (m: MailMessage) => boolean) {
  const sent: MailMessage[] = []
  const mailer: Mailer = {
    mode,
    async send(m) {
      if (fail?.(m)) throw new Error('SMTP caído')
      sent.push(m)
    },
  }
  return { mailer, sent }
}

function makeDeps(env: Record<string, string> = {}, mailer?: Mailer) {
  const config: ServerConfig = loadConfig({ SMTP_HOST: 'smtp.example', ...env })
  const fm = fakeMailer()
  const logs: { level: string; event: string; fields?: Record<string, unknown> }[] = []
  const fetch = vi.fn(async () => ({ ok: true, status: 200 }))
  const deps: SubmissionDeps = {
    config,
    mailer: mailer ?? fm.mailer,
    limiter: new RateLimiter(config.rateLimit.max, config.rateLimit.windowMs),
    log: (level, event, fields) => logs.push({ level, event, fields }),
    fetch,
    now: () => NOW,
    store: null,
  }
  return { deps, sent: fm.sent, logs, fetch }
}

describe('POST /api/lead', () => {
  let ctx: ReturnType<typeof makeDeps>
  beforeEach(() => {
    ctx = makeDeps()
  })

  it('acepta una solicitud válida, avisa a ventas y confirma al cliente', async () => {
    const res = await submitLead(lead(), IP, ctx.deps)
    expect(res.status).toBe(200)
    expect(res.body.ok).toBe(true)
    const ref = (res.body as { reference: string }).reference
    expect(ref).toMatch(REFERENCE_RE)
    expect(ref.startsWith('HF-D-20261010-')).toBe(true)

    expect(ctx.sent).toHaveLength(2)
    const [sales, customer] = ctx.sent
    expect(sales!.to).toBe('info@kns.gt')
    expect(sales!.replyTo).toBe('ana@fibrasur.example')
    expect(sales!.subject).toContain(ref)
    expect(sales!.text).toContain('Fibra Sur')
    expect(sales!.text).toContain('Guatemala')
    expect(customer!.to).toBe('ana@fibrasur.example')
    expect(customer!.subject).toContain('solicitud recibida')
  })

  it('el destino de ventas es configurable', async () => {
    const c = makeDeps({ SALES_EMAIL: 'ventas@example.gt' })
    await submitLead(lead(), IP, c.deps)
    expect(c.sent[0]!.to).toBe('ventas@example.gt')
  })

  it('honeypot: responde OK sin enviar nada', async () => {
    const res = await submitLead(lead({ website: 'http://spam.example' }), IP, ctx.deps)
    expect(res.status).toBe(200)
    expect(res.body.ok).toBe(true)
    expect(ctx.sent).toHaveLength(0)
    expect(ctx.logs.some((l) => l.event === 'form.honeypot')).toBe(true)
  })

  it('rechaza un envío más rápido que el tiempo mínimo', async () => {
    const res = await submitLead(lead({ startedAt: NOW - 1000 }), IP, ctx.deps)
    expect(res.status).toBe(400)
    expect(res.body).toMatchObject({ ok: false, code: 'TOO_FAST' })
    expect(ctx.sent).toHaveLength(0)
  })

  it('rechaza un envío sin marca de tiempo', async () => {
    const res = await submitLead(lead({ startedAt: undefined }), IP, ctx.deps)
    expect(res.body).toMatchObject({ code: 'TOO_FAST' })
  })

  it('valida en servidor y devuelve las claves de error por campo', async () => {
    const res = await submitLead(
      lead({ email: 'no-es-correo', consent: false, clients: 'muchos', name: 'A', phone: 'abc' }),
      IP,
      ctx.deps,
    )
    expect(res.status).toBe(422)
    expect(res.body).toMatchObject({
      ok: false,
      code: 'VALIDATION',
      fields: {
        email: 'email',
        consent: 'consent',
        clients: 'required',
        name: 'tooShort',
        phone: 'phone',
      },
    })
    expect(ctx.sent).toHaveLength(0)
  })

  it('rechaza cuerpos que no son objetos', async () => {
    const res = await submitLead(null, IP, ctx.deps)
    expect(res.body.ok).toBe(false)
  })

  it('aplica el rate limit por IP', async () => {
    const c = makeDeps({ RATE_LIMIT_MAX: '2' })
    expect((await submitLead(lead(), IP, c.deps)).status).toBe(200)
    expect((await submitLead(lead(), IP, c.deps)).status).toBe(200)
    const blocked = await submitLead(lead(), IP, c.deps)
    expect(blocked.status).toBe(429)
    expect(blocked.body).toMatchObject({ code: 'RATE_LIMITED' })
    expect(blocked.retryAfterSec).toBeGreaterThan(0)
    // Otra IP no se ve afectada.
    expect((await submitLead(lead(), '198.51.100.1', c.deps)).status).toBe(200)
  })

  it('no registra datos personales completos', async () => {
    await submitLead(lead(), IP, ctx.deps)
    const dump = JSON.stringify(ctx.logs)
    expect(dump).not.toContain('ana@fibrasur.example')
    expect(dump).not.toContain('Ana Pérez')
    expect(dump).not.toContain('5555')
    expect(dump).toContain('a***@fibrasur.example')
  })

  it('sin SMTP en desarrollo registra y responde OK', async () => {
    const config = loadConfig({})
    expect(config.mail.mode).toBe('log')
    const logs: string[] = []
    const c = makeDeps({}, createMailer(config.mail))
    c.deps.config = config
    c.deps.log = (_l, e) => logs.push(e)
    const res = await submitLead(lead(), IP, c.deps)
    expect(res.status).toBe(200)
  })

  it('en producción sin SMTP ni webhook responde 503 (no pierde la solicitud en silencio)', async () => {
    const config = loadConfig({ NODE_ENV: 'production' })
    const c = makeDeps({}, fakeMailer('log').mailer)
    c.deps.config = config
    const res = await submitLead(lead(), IP, c.deps)
    expect(res.status).toBe(503)
    expect(res.body).toMatchObject({ code: 'UNAVAILABLE' })
  })

  it('si falla el correo a ventas responde 502', async () => {
    const fm = fakeMailer('smtp', (m) => m.tag === 'sales')
    const c = makeDeps({}, fm.mailer)
    const res = await submitLead(lead(), IP, c.deps)
    expect(res.status).toBe(502)
  })

  it('si falla solo la confirmación al cliente, la solicitud sigue siendo OK', async () => {
    const fm = fakeMailer('smtp', (m) => m.tag === 'customer')
    const c = makeDeps({}, fm.mailer)
    const res = await submitLead(lead(), IP, c.deps)
    expect(res.status).toBe(200)
    expect(fm.sent).toHaveLength(1)
  })

  it('envía el webhook firmado con HMAC si está configurado', async () => {
    const c = makeDeps({
      LEADS_WEBHOOK_URL: 'https://crm.example/hook',
      LEADS_WEBHOOK_SECRET: 's3cr3t',
    })
    await submitLead(lead(), IP, c.deps)
    expect(c.fetch).toHaveBeenCalledTimes(1)
    const [url, init] = c.fetch.mock.calls[0] as unknown as [
      string,
      { headers: Record<string, string>; body: string },
    ]
    expect(url).toBe('https://crm.example/hook')
    expect(init.headers['x-horus-signature']).toBe(signBody('s3cr3t', init.body))
    expect(JSON.parse(init.body)).toMatchObject({ type: 'lead', company: 'Fibra Sur' })
  })

  it('un webhook caído no rompe la solicitud', async () => {
    const c = makeDeps({ LEADS_WEBHOOK_URL: 'https://crm.example/hook' })
    c.fetch.mockResolvedValueOnce({ ok: false, status: 500 })
    const res = await submitLead(lead(), IP, c.deps)
    expect(res.status).toBe(200)
    expect(c.logs.some((l) => l.event === 'webhook.failed')).toBe(true)
  })

  it('el asunto no admite saltos de línea (inyección de cabeceras)', async () => {
    await submitLead(lead({ company: 'ACME\r\nBcc: x@evil.example' }), IP, ctx.deps)
    expect(ctx.sent[0]!.subject).not.toMatch(/[\r\n]/)
  })

  it('el HTML del correo escapa lo que escribe el cliente', async () => {
    await submitLead(lead({ message: '<script>alert(1)</script>' }), IP, ctx.deps)
    expect(ctx.sent[0]!.html).not.toContain('<script>')
    expect(ctx.sent[0]!.html).toContain('&lt;script&gt;')
  })
})

describe('POST /api/purchase', () => {
  it('sin base de datos: genera la referencia y avisa, sin importe ni métodos de pago', async () => {
    const c = makeDeps()
    const res = await submitPurchase(purchase(), IP, c.deps)
    expect(res.status).toBe(200)
    expect(res.body).toMatchObject({
      ok: true,
      amount: null,
      methods: { paypal: false, neo: false, transfer: false },
    })
    const ref = (res.body as { reference: string }).reference
    expect(ref).toMatch(/^HF-P-20261010-/)
    const [sales, customer] = c.sent
    expect(sales!.text).toContain('1234567-8')
    expect(customer!.subject).toBe(`Horus Flow: purchase request ${ref}`)
  })

  it('valida plan, NIT y consentimiento', async () => {
    const c = makeDeps()
    const res = await submitPurchase(
      purchase({ plan: 'enterprise-plus', nit: 'ABC', consent: false, legalName: '' }),
      IP,
      c.deps,
    )
    expect(res.status).toBe(422)
    expect(res.body).toMatchObject({
      fields: { plan: 'required', nit: 'invalid', consent: 'consent', legalName: 'required' },
    })
  })

  it('acepta NIT vacío, "CF" y con dígito K', async () => {
    for (const nit of ['', 'CF', '576937-K', '12345678']) {
      const c = makeDeps()
      const res = await submitPurchase(purchase({ nit }), IP, c.deps)
      expect(res.status, nit).toBe(200)
    }
  })

  it('honeypot y tiempo mínimo también protegen la compra', async () => {
    const c = makeDeps()
    expect((await submitPurchase(purchase({ website: 'x' }), IP, c.deps)).status).toBe(200)
    expect(c.sent).toHaveLength(0)
    expect((await submitPurchase(purchase({ startedAt: NOW }), IP, c.deps)).status).toBe(400)
  })
})

describe('SMTP simulado (MAIL_TRANSPORT=file)', () => {
  it('escribe cada mensaje como JSON en la carpeta de salida', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'outbox-'))
    const config = loadConfig({ MAIL_TRANSPORT: 'file', MAIL_OUTBOX_DIR: dir })
    const c = makeDeps({}, createMailer(config.mail))
    c.deps.config = config
    const res = await submitLead(lead(), IP, c.deps)
    expect(res.status).toBe(200)
    const files = (await readdir(dir)).sort()
    expect(files).toHaveLength(2)
    const sales = JSON.parse(
      await readFile(
        join(
          dir,
          files.find((f) => f.endsWith('-sales.json'))!,
        ),
        'utf8',
      ),
    )
    expect(sales.to[0].address).toBe('info@kns.gt')
    expect(sales.subject).toContain('Solicitud de demo')
  })
})
