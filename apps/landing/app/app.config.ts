// Tema de Nuxt UI: lapislázuli como único acento y neutros fríos. Los valores exactos por modo
// (con su contraste calculado) están en assets/css/main.css.
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
  },
})
