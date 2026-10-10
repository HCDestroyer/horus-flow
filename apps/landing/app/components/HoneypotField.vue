<script setup lang="ts">
// Campo trampa para bots: fuera de la vista, fuera del orden de tabulación y oculto a los
// lectores de pantalla. Una persona nunca lo rellena.
const { t } = useI18n()
const model = defineModel<string>({ required: true })
</script>

<template>
  <div aria-hidden="true" class="pointer-events-none absolute -left-[10000px] h-px w-px overflow-hidden">
    <label>
      {{ t('form.honeypot') }}
      <input v-model="model" type="text" name="website" tabindex="-1" autocomplete="off" />
    </label>
  </div>
</template>
