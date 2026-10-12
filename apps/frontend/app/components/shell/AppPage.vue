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

const { t, te } = useI18n()
const { membership } = useTenant()
const route = useRoute()

// Ayuda contextual: para qué sirve la sección actual y los atajos de la consola.
const section = computed(() => sectionOfPath(route.path))
const helpText = computed(() => {
  const key = section.value && `sections.${section.value.id}.question`
  return key && te(key) ? t(key) : t('help.generic')
})
const isMac = computed(() => import.meta.client && /Mac|iP(hone|ad)/.test(navigator.platform))

// El cuerpo del panel hace scroll propio: debe poder enfocarse para desplazarlo con el
// teclado aunque el contenido no tenga controles (WCAG 2.1.1, axe scrollable-region-focusable).
const content = useTemplateRef<HTMLElement>('content')
onMounted(() => content.value?.parentElement?.setAttribute('tabindex', '0'))

useHead({
  title: () =>
    membership.value ? `${props.title} · ${membership.value.tenant_name}` : props.title,
})
</script>

<template>
  <UDashboardPanel :id="panelId ?? 'main-panel'" :ui="{ body: 'gap-6 sm:gap-6' }">
    <template #header>
      <UDashboardNavbar :title="title" class="chrome-material" data-testid="app-navbar">
        <template #leading>
          <UDashboardSidebarCollapse :aria-label="t('toolbar.collapse')" />
        </template>
        <template #right>
          <slot name="navbar-right" />
          <UPopover :content="{ align: 'end' }">
            <UTooltip :text="t('toolbar.help')">
              <UButton
                icon="i-lucide-circle-help"
                color="neutral"
                variant="ghost"
                :aria-label="t('toolbar.help')"
                data-testid="help-button"
              />
            </UTooltip>
            <template #content>
              <div
                class="flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-3 p-4"
                data-testid="help-panel"
              >
                <div>
                  <p class="text-highlighted font-semibold">{{ title }}</p>
                  <p class="text-default mt-1 text-sm">{{ helpText }}</p>
                </div>
                <dl class="text-sm">
                  <dt class="text-muted text-xs">{{ t('help.shortcuts') }}</dt>
                  <dd class="mt-1 flex items-center gap-1.5">
                    <UKbd :value="isMac ? 'meta' : 'ctrl'" />
                    <UKbd value="K" />
                    <span class="text-default">{{ t('help.search') }}</span>
                  </dd>
                </dl>
                <p class="text-muted text-xs">{{ t('help.errors') }}</p>
              </div>
            </template>
          </UPopover>
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
      <div id="main-content" ref="content" tabindex="-1" class="flex flex-col gap-6 outline-none">
        <slot />
      </div>
    </template>
  </UDashboardPanel>
</template>
