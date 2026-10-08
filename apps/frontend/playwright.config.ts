import { defineConfig, devices } from '@playwright/test'

/**
 * E2E de humo (conventions.md §5): la SPA generada contra la API simulada.
 * `@playwright/test` va fijado a la versión cuyo Chromium está en la imagen de CI
 * (PLAYWRIGHT_BROWSERS_PATH); no se ejecuta `playwright install`.
 *
 * - `E2E_NO_BUILD=1` reutiliza `.output/public` (útil en CI tras `pnpm build`).
 * - `E2E_BASE_URL` apunta a un despliegue ya levantado y no arranca servidor.
 */
const port = Number(process.env.PORT ?? 4173)
const baseURL = process.env.E2E_BASE_URL ?? `http://127.0.0.1:${port}`
const serve = 'node tests/e2e/serve.mjs'

export default defineConfig({
  testDir: 'tests/e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  // Las capturas solo con `pnpm screenshots` (escriben en docs/screenshots).
  grepInvert: process.env.SCREENSHOTS ? undefined : /@screenshots/,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['github'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL,
    locale: 'es-ES',
    timezoneId: 'America/Bogota',
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
