// ESLint flat config: @nuxt/eslint + eslint-config-prettier (igual que apps/frontend).
import prettier from 'eslint-config-prettier'
import withNuxt from './.nuxt/eslint.config.mjs'

export default withNuxt(
  {
    ignores: ['.output/**', '.nuxt/**', 'dist/**', 'playwright-report/**', 'test-results/**'],
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
