<script setup lang="ts">
/**
 * Página del shell (frontend.md §5.1): navbar con el título de la sección (nunca el nombre de
 * la app, HIG toolbars › Titles), toolbar opcional (filtros a la izquierda, una acción
 * primaria a la derecha) y cuerpo con scroll propio. Pone el `<title>` con el ISP (§3.1).
 */
const props = defineProps<{
  title: string
  /** Panel único por página; se usa para recordar tamaños. */
  panelId?: string
}>()

const { t } = useI18n()
const { membership } = useTenant()

useHead({
  title: () =>
    membership.value ? `${props.title} · ${membership.value.tenant_name}` : props.title,
})
</script>

<template>
  <UDashboardPanel :id="panelId ?? 'main-panel'" :ui="{ body: 'gap-6 sm:gap-6' }">
    <template #header>
      <UDashboardNavbar :title="title" class="chrome-material">
        <template #leading>
          <UDashboardSidebarCollapse :aria-label="t('toolbar.collapse')" />
        </template>
        <template #right>
          <slot name="navbar-right" />
          <UTooltip :text="t('toolbar.help')">
            <UButton
              icon="i-lucide-circle-help"
              color="neutral"
              variant="ghost"
              :aria-label="t('toolbar.help')"
            />
          </UTooltip>
        </template>
      </UDashboardNavbar>
      <UDashboardToolbar v-if="$slots['toolbar-left'] || $slots['toolbar-right']">
        <template #left>
          <slot name="toolbar-left" />
        </template>
        <template #right>
          <slot name="toolbar-right" />
        </template>
      </UDashboardToolbar>
    </template>

    <template #body>
      <div id="main-content" tabindex="-1" class="flex flex-col gap-6 outline-none">
        <slot />
      </div>
    </template>
  </UDashboardPanel>
</template>
