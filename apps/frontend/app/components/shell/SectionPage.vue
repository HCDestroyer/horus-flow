<script setup lang="ts">
import type { NavSection } from '~/utils/navigation'

/**
 * Página de una sección del mapa de navegación que aún no tiene contenido en esta versión.
 * Dice qué pregunta responderá la sección (frontend.md §1, "Propósito") sin inventar datos.
 */
const props = defineProps<{ section: NavSection }>()

const { t } = useI18n()
</script>

<template>
  <AppPage :title="t(props.section.labelKey)" :panel-id="`section-${props.section.id}`">
    <UCard class="py-6">
      <EmptyState
        :icon="props.section.icon"
        :title="t(`sections.${props.section.id}.question`)"
        :description="t('sections.notYet')"
      />
    </UCard>
  </AppPage>
</template>
