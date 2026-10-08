import { defineVitestConfig } from '@nuxt/test-utils/config'

// Unitarios (conventions.md §5): Vitest + @nuxt/test-utils. Los tests de lógica pura corren en
// `node`; los de componentes declaran `// @vitest-environment nuxt` en el archivo.
export default defineVitestConfig({
  test: {
    include: ['tests/unit/**/*.test.ts'],
    environment: 'node',
    environmentOptions: {
      nuxt: {
        domEnvironment: 'happy-dom',
      },
    },
  },
})
