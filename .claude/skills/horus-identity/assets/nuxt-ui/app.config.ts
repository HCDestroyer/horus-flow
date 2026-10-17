// Tema de Nuxt UI: colores semánticos (frontend.md §13.2). Los tonos concretos por modo
// (contraste AA en claro y oscuro) se fijan en assets/css/main.css.
export default defineAppConfig({
  ui: {
    colors: {
      // Marca del ecosistema Horus (P-20): azul petróleo sobrio, un único color primario.
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
      // Luz de la acción primaria (una por vista, §13.1); transparente en claro y con "más contraste".
      compoundVariants: [
        {
          color: 'primary',
          variant: 'solid',
          // hover/active más oscuro en claro y al 90 % en oscuro (Nuxt UI aclara al 75 %: el texto
          // quedaba en 3,4:1, por debajo de AA).
          class:
            'shadow-[0_0_18px_var(--hf-glow-brand)] hover:shadow-[0_0_26px_var(--hf-glow-brand)] hover:bg-(--ui-color-primary-800) active:bg-(--ui-color-primary-800) dark:hover:bg-primary/90 dark:active:bg-primary/90',
        },
      ],
    },
    // Vidrio y luz (frontend.md §13.1): vidrio fuerte en la capa funcional flotante (menús,
    // modales, paneles laterales); las tarjetas del contenido, material casi sólido.
    card: {
      slots: { root: 'rounded-(--hf-radius-card)' },
      variants: {
        variant: {
          outline: { root: 'bg-(--hf-card) ring ring-default divide-y divide-default' },
        },
      },
    },
    modal: {
      slots: { content: 'bg-(--hf-glass-strong) backdrop-blur-2xl backdrop-saturate-150' },
    },
    slideover: {
      slots: { content: 'bg-(--hf-glass-strong) backdrop-blur-2xl backdrop-saturate-150' },
    },
    popover: {
      slots: { content: 'bg-(--hf-glass-strong) backdrop-blur-2xl backdrop-saturate-150' },
    },
    dropdownMenu: {
      slots: { content: 'bg-(--hf-glass-strong) backdrop-blur-2xl backdrop-saturate-150' },
    },
  },
})
