<script setup lang="ts">
// Par de campos ES/EN para un texto de la web.
import type { Localized } from '#shared/catalog'

const props = defineProps<{ id: string; label: string; multiline?: boolean; required?: boolean }>()
const model = defineModel<Localized>({ required: true })
</script>

<template>
  <fieldset class="min-w-0">
    <legend class="mb-1 text-sm font-medium">
      {{ props.label }}<span v-if="required" class="ms-0.5 text-error" aria-hidden="true">*</span>
    </legend>
    <div class="grid gap-2 sm:grid-cols-2">
      <div v-for="lang in ['es', 'en'] as const" :key="lang" class="flex items-start gap-2">
        <label
          :for="`${id}-${lang}`"
          class="mt-2.5 w-6 shrink-0 text-xs font-semibold text-muted uppercase"
        >
          <span aria-hidden="true">{{ lang }}</span>
          <span class="sr-only"
            >{{ props.label }} ({{ lang === 'es' ? 'español' : 'inglés' }})</span
          >
        </label>
        <UTextarea
          v-if="multiline"
          :id="`${id}-${lang}`"
          v-model="model[lang]"
          :rows="2"
          autoresize
          class="w-full"
          :required="required"
        />
        <UInput
          v-else
          :id="`${id}-${lang}`"
          v-model="model[lang]"
          class="w-full"
          :required="required"
        />
      </div>
    </div>
  </fieldset>
</template>
