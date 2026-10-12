<script setup lang="ts">
/**
 * Campo de secreto con "Mostrar/Ocultar" (write-only: nunca se rellena con un valor guardado).
 * `autocomplete="new-password"` para que el navegador no proponga la contraseña de Horus.
 */
defineOptions({ inheritAttrs: false })
const model = defineModel<string>({ default: '' })
const { t } = useI18n()
const show = ref(false)
</script>

<template>
  <UInput
    v-model="model"
    v-bind="$attrs"
    :type="show ? 'text' : 'password'"
    autocomplete="new-password"
    spellcheck="false"
    class="w-full"
    :ui="{ base: 'font-mono', trailing: 'pe-1' }"
  >
    <template #trailing>
      <UButton
        color="neutral"
        variant="link"
        size="sm"
        :icon="show ? 'i-lucide-eye-off' : 'i-lucide-eye'"
        :aria-label="show ? t('credentials.hide') : t('credentials.show')"
        :aria-pressed="show"
        @click="show = !show"
      />
    </template>
  </UInput>
</template>
