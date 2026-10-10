<script setup lang="ts">
// Campo de URL de un link de pago Neo, con etiqueta solo para lectores de pantalla (la tabla ya
// muestra plan y periodo).
defineProps<{ id: string; label: string }>()
const model = defineModel<string>({ required: true })
</script>

<template>
  <div>
    <label :for="id" class="sr-only">{{ label }}</label>
    <UInput :id="id" v-model="model" type="url" placeholder="https://" class="w-full" />
  </div>
</template>
