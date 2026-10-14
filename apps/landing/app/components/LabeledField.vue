<script setup lang="ts">
// Campo con <label for> fijo. UFormField genera el `for` con useId(), que no coincide entre el
// HTML del servidor y el cliente cuando el formulario se hidrata en diferido; aquí la etiqueta
// apunta a un id estable y UFormField se encarga de la validación, la ayuda y el error.
withDefaults(
  defineProps<{
    id: string
    label: string
    name: string
    required?: boolean
    hint?: string
    help?: string
  }>(),
  { required: false, hint: undefined, help: undefined },
)
</script>

<template>
  <div>
    <div class="mb-1 flex items-center justify-between gap-2">
      <label :for="id" class="block text-sm font-medium text-default">
        {{ label }}<span v-if="required" class="ms-0.5 text-error" aria-hidden="true">*</span>
      </label>
      <span v-if="hint" class="text-sm text-muted">{{ hint }}</span>
    </div>
    <UFormField :name="name" :help="help" :required="required">
      <slot />
    </UFormField>
  </div>
</template>
