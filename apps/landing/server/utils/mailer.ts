// Envío de correo con nodemailer. Tres modos (MAIL_TRANSPORT):
//   smtp — SMTP real (SMTP_HOST, SMTP_PORT, SMTP_SECURE, SMTP_USER, SMTP_PASS).
//   file — SMTP simulado: cada mensaje se guarda como JSON en MAIL_OUTBOX_DIR (desarrollo, e2e).
//   log  — sin SMTP: solo se registra que habría un envío (sin datos personales) y se responde OK.
import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import nodemailer from 'nodemailer'
import type { ServerConfig } from './config'
import { log, maskEmail } from './log'

export interface MailMessage {
  to: string
  replyTo?: string
  subject: string
  text: string
  html: string
  /** Etiqueta interna para el registro (sales | customer). */
  tag: string
}

export interface Mailer {
  readonly mode: ServerConfig['mail']['mode']
  send(message: MailMessage): Promise<void>
}

export function createMailer(config: ServerConfig['mail']): Mailer {
  if (config.mode === 'smtp' && config.smtp) {
    const transport = nodemailer.createTransport({
      host: config.smtp.host,
      port: config.smtp.port,
      secure: config.smtp.secure,
      auth: config.smtp.user ? { user: config.smtp.user, pass: config.smtp.pass } : undefined,
      connectionTimeout: 10_000,
      greetingTimeout: 10_000,
      socketTimeout: 20_000,
    })
    return {
      mode: 'smtp',
      async send(m) {
        await transport.sendMail({
          from: config.from,
          to: m.to,
          replyTo: m.replyTo,
          subject: m.subject,
          text: m.text,
          html: m.html,
        })
      },
    }
  }

  if (config.mode === 'file') {
    const transport = nodemailer.createTransport({ jsonTransport: true })
    let seq = 0
    return {
      mode: 'file',
      async send(m) {
        const info = await transport.sendMail({
          from: config.from,
          to: m.to,
          replyTo: m.replyTo,
          subject: m.subject,
          text: m.text,
          html: m.html,
        })
        await mkdir(config.outboxDir, { recursive: true })
        const name = `${Date.now()}-${process.pid}-${++seq}-${m.tag}.json`
        await writeFile(join(config.outboxDir, name), String(info.message), 'utf8')
      },
    }
  }

  return {
    mode: 'log',
    async send(m) {
      log('info', 'mail.skipped', { reason: 'smtp_not_configured', tag: m.tag, to: maskEmail(m.to) })
    },
  }
}
