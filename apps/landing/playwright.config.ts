import { defineConfig, devices } from '@playwright/test'

/**
 * E2E de la landing contra el servidor Nitro de producción (`pnpm build` + node), con el
 * SMTP simulado (MAIL_TRANSPORT=file → tests/e2e/.outbox). `@playwright/test` va fijado a la
 * versión cuyo Chromium está en PLAYWRIGHT_BROWSERS_PATH; no se ejecuta `playwright install`.
 *
 * - `E2E_NO_BUILD=1` reutiliza `.output` (ya construido).
 * - `E2E_BASE_URL` apunta a un servidor ya levantado y no arranca ninguno.
 * - Las capturas solo con `pnpm screenshots` (escriben en docs/screenshots).
 */
const port = Number(process.env.PORT ?? 4174)
const baseURL = process.env.E2E_BASE_URL ?? `http://127.0.0.1:${port}`
const OUTBOX = 'tests/e2e/.outbox'

const env = [
  `PORT=${port}`,
  'NITRO_HOST=127.0.0.1',
  'MAIL_TRANSPORT=file',
  `MAIL_OUTBOX_DIR=${OUTBOX}`,
  'FORM_MIN_FILL_SECONDS=1',
  'RATE_LIMIT_MAX=100',
  `NUXT_PUBLIC_SITE_URL=https://horusflow.example`,
].join(' ')
const serve = `rm -rf ${OUTBOX} && ${env} node .output/server/index.mjs`

export default defineConfig({
  testDir: 'tests/e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  grepInvert: process.env.SCREENSHOTS ? undefined : /@screenshots/,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['github'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL,
    locale: 'es-GT',
    timezoneId: 'America/Guatemala',
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: process.env.E2E_BASE_URL
    ? undefined
    : {
        command: process.env.E2E_NO_BUILD ? serve : `pnpm build && ${serve}`,
        url: baseURL,
        reuseExistingServer: !process.env.CI,
        timeout: 300_000,
        stdout: 'ignore',
        stderr: 'pipe',
      },
})
