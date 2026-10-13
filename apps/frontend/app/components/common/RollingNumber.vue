<script setup lang="ts">
/**
 * Número vivo (frontend.md §13.1): cada dígito rueda hasta su valor nuevo y el valor da un
 * destello breve cuando cambia, para que se note que el dato se mueve sin alarmar. El texto
 * real sigue en el DOM (lectores de pantalla, copiar, pruebas); la tira 0–9 es CSS
 * (`.roll-digit` en main.css). Con movimiento reducido el cambio es instantáneo.
 *
 * Los dígitos se identifican desde la derecha para que unidades y decenas no se mezclen
 * cuando el número cambia de longitud (999 → 1.000).
 */
const props = defineProps<{ value: string | number }>()

const text = computed(() => String(props.value))
const chars = computed(() => {
  const s = text.value
  return Array.from(s, (ch, i) => ({ ch, key: s.length - i, digit: /\d/.test(ch) }))
})

/** Alterna entre dos clases idénticas para reiniciar la animación del destello. */
const pulse = ref<'' | 'ping-a' | 'ping-b'>('')
watch(text, (now, before) => {
  if (before === undefined || now === before) return
  pulse.value = pulse.value === 'ping-a' ? 'ping-b' : 'ping-a'
})
</script>

<template>
  <span :class="pulse"
    ><template v-for="c in chars" :key="c.key"
      ><span v-if="c.digit" class="roll-digit" :data-d="c.ch">{{ c.ch }}</span
      ><template v-else>{{ c.ch }}</template></template
    ></span
  >
</template>
