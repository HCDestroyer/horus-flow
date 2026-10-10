// Tema de Nuxt UI: lapislázuli como único acento y neutros fríos. Los valores exactos por modo
// (con su contraste calculado) están en assets/css/main.css. Los controles de formulario miden
// al menos 44 px de alto (HIG accessibility.md › Mobility: 44×44 pt por defecto).
export default defineAppConfig({
  ui: {
    colors: {
      primary: 'lapis',
      neutral: 'slate',
      error: 'red',
    },
    button: {
      slots: {
        base: 'cursor-pointer justify-center',
      },
    },
    input: {
      slots: { base: 'min-h-12' },
    },
    select: {
      slots: { base: 'min-h-12' },
    },
    textarea: {
      slots: { base: 'min-h-12' },
    },
    checkbox: {
      slots: { base: 'size-6' },
    },
  },
})
