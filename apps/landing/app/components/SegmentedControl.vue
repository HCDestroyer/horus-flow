<script setup lang="ts" generic="T extends string">
// Control segmentado accesible: radios nativos (flechas para moverse, espacio para elegir) con
// aspecto de selector. Es un control flotante de la capa funcional: lleva vidrio (`glass`)
// salvo que se pida `solid` (cuando va dentro de una superficie de contenido).
const props = withDefaults(
  defineProps<{
    legend: string
    name: string
    options: { value: T; label: string }[]
    solid?: boolean
  }>(),
  { solid: false },
)
const model = defineModel<T>({ required: true })
</script>

<template>
  <fieldset
    :class="[
      props.solid ? 'border border-default bg-muted' : 'glass',
      'inline-flex rounded-full p-1',
    ]"
  >
    <legend class="sr-only">{{ legend }}</legend>
    <label
      v-for="o in options"
      :key="o.value"
      class="relative flex min-h-11 min-w-24 cursor-pointer items-center justify-center rounded-full px-4 text-[0.95rem] font-medium transition-colors has-[:checked]:bg-(--ui-primary) has-[:checked]:text-(--ui-bg) has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-(--ui-primary) text-toned hover:text-highlighted has-[:checked]:hover:text-(--ui-bg)"
    >
      <input v-model="model" type="radio" :name="name" :value="o.value" class="sr-only" />
      {{ o.label }}
    </label>
  </fieldset>
</template>
