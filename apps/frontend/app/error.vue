<script setup lang="ts">
import type { NuxtError } from '#app'
import { es } from '@nuxt/ui/locale'

/**
 * Errores de página (frontend.md §9.3): 404 (incluido un ISP ajeno) → "No encontrado";
 * 403 → mensaje de permiso; el resto, genérico. Sin detalles internos.
 */
const props = defineProps<{ error: NuxtError }>()

const { t } = useI18n()

const kind = computed(() => {
  if (props.error.statusCode === 404) return 'notFound'
  if (props.error.statusCode === 403) return 'forbidden'
  return 'generic'
})

useHead({ title: () => t(`errors.${kind.value}.title`) })

function goHome() {
  clearError({ redirect: '/' })
}
</script>

<template>
  <UApp :locale="es">
    <main
      id="main-content"
      class="bg-muted flex min-h-dvh items-center justify-center px-4"
      data-testid="error-page"
    >
      <UCard class="w-full max-w-md text-center" :ui="{ body: 'p-8 space-y-4' }">
        <p class="text-muted font-mono text-sm tabular">{{ error.statusCode }}</p>
        <h1 class="text-highlighted text-xl font-semibold">{{ t(`errors.${kind}.title`) }}</h1>
        <p class="text-muted">{{ t(`errors.${kind}.description`) }}</p>
        <UButton icon="i-lucide-arrow-left" :label="t('errors.goHome')" @click="goHome" />
      </UCard>
    </main>
  </UApp>
</template>
