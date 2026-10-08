// Tema de Nuxt UI: colores semánticos (frontend.md §13.2). Los tonos concretos por modo
// (contraste AA en claro y oscuro) se fijan en assets/css/main.css.
export default defineAppConfig({
  ui: {
    colors: {
      // Marca provisional hasta P-20: azul petróleo sobrio, lejos del cliché "NOC" verde ácido.
      primary: 'teal',
      secondary: 'indigo',
      success: 'green',
      info: 'blue',
      warning: 'amber',
      error: 'red',
      neutral: 'zinc',
    },
    button: {
      slots: {
        base: 'cursor-pointer',
      },
    },
  },
})
