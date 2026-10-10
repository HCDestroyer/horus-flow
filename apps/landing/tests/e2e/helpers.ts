import { readdir, readFile } from 'node:fs/promises'
import { join } from 'node:path'
import type { Page } from '@playwright/test'

export const OUTBOX = 'tests/e2e/.outbox'

/** Mensajes del SMTP simulado que mencionan la referencia (asunto o texto). */
export async function mailsFor(reference: string) {
  let files: string[]
  try {
    files = await readdir(OUTBOX)
  } catch {
    return []
  }
  const out: { to: string; subject: string; text: string; file: string }[] = []
  for (const f of files) {
    const msg = JSON.parse(await readFile(join(OUTBOX, f), 'utf8'))
    if (String(msg.subject).includes(reference) || String(msg.text).includes(reference)) {
      out.push({ to: msg.to?.[0]?.address ?? '', subject: msg.subject, text: msg.text, file: f })
    }
  }
  return out
}

/** Recorre la página para que carguen las imágenes diferidas. */
export async function scrollThrough(page: Page) {
  await page.evaluate(async () => {
    for (let y = 0; y < document.body.scrollHeight; y += 500) {
      window.scrollTo(0, y)
      await new Promise((r) => setTimeout(r, 50))
    }
    window.scrollTo(0, 0)
  })
  await page.waitForLoadState('networkidle')
}

/** Espera a que la app esté hidratada (los formularios necesitan JavaScript). */
export async function hydrated(page: Page) {
  await page.waitForFunction(() => {
    const el = document.querySelector('#__nuxt') as (HTMLElement & { __vue_app__?: unknown }) | null
    return Boolean(el?.__vue_app__)
  })
}
