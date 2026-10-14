import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitest/config'

// Tests de las rutas de servidor y de la lógica pura (validación, antispam, rate limit, envío
// simulado). No necesitan Nuxt: la lógica vive en módulos sin auto-imports.
export default defineConfig({
  resolve: {
    alias: {
      '#shared': fileURLToPath(new URL('./shared', import.meta.url)),
      '~': fileURLToPath(new URL('./app', import.meta.url)),
    },
  },
  test: {
    include: ['tests/unit/**/*.test.ts'],
    environment: 'node',
  },
})
