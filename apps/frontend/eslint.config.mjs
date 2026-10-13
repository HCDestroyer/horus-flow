// ESLint flat config (conventions.md §4): @nuxt/eslint + eslint-config-prettier.
import prettier from 'eslint-config-prettier'
import withNuxt from './.nuxt/eslint.config.mjs'

export default withNuxt(
  {
    ignores: [
      '.output/**',
      '.nuxt/**',
      'dist/**',
      'playwright-report/**',
      'test-results/**',
      // Generados desde los contratos (pnpm api:generate).
      'types/api/schema.d.ts',
      'types/api/widget-catalog.ts',
      // Pantalla NOC ligera: ES5 a propósito para TVs antiguas (tests/unit/kiosk-lite.test.ts).
      'public/kiosk-lite.js',
    ],
  },
  {
    rules: {
      'vue/no-v-html': 'error',
      'no-console': 'warn',
      'vue/multi-word-component-names': 'off',
    },
  },
  prettier,
)
