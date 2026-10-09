<script setup lang="ts">
/**
 * Un dashboard (I0-16, frontend.md §6): grilla de solo lectura con los widgets de la
 * plantilla, cada uno con sus datos y estados. `?scale=wall` abre la vista mural: pantalla
 * completa, tipografía de pantalla mural y nada que dependa de hover (§7.3–§7.4).
 */
const { t } = useI18n()
const route = useRoute()
const { membership } = useTenant()
const config = useRuntimeConfig()

useSectionGuard()(findSection('tenant', 'dashboards'), String(route.params.slug))

const id = computed(() => String(route.params.id))
const wall = computed(() => route.query.scale === 'wall')
setPageLayout(wall.value ? 'wall' : 'default')
watch(wall, (w) => setPageLayout(w ? 'wall' : 'default'))

const { data: dashboard, error, status, refresh } = useDashboard(id)
const tenantName = computed(() => membership.value?.tenant_name ?? '')

useHead({
  title: () =>
    [dashboard.value?.name ?? t('dashboards.title'), tenantName.value].filter(Boolean).join(' · '),
})

watch(error, (raw) => {
  const e = originalError(raw)
  if (e instanceof ApiError && e.status === 404) {
    showError(createError({ statusCode: 404, statusMessage: 'Not Found' }))
  }
})

// Vista mural: Esc vuelve; el botón de salida solo aparece al mover el puntero o con foco.
const exitVisible = ref(false)
let hideTimer: ReturnType<typeof setTimeout> | undefined
function poke() {
  exitVisible.value = true
  clearTimeout(hideTimer)
  hideTimer = setTimeout(() => (exitVisible.value = false), 3000)
}
function exitWall() {
  navigateTo({ path: route.path, query: {} })
}
function onKey(e: KeyboardEvent) {
  if (wall.value && e.key === 'Escape') exitWall()
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
  clearTimeout(hideTimer)
})
</script>

<template>
  <div
    v-if="wall"
    class="relative h-dvh"
    :class="{ 'cursor-none': !exitVisible }"
    data-testid="dashboard-wall"
    @pointermove="poke"
  >
    <h1 class="sr-only">{{ dashboard?.name }}</h1>
    <DashboardGrid
      v-if="dashboard"
      :dashboard="dashboard"
      :tenant-name="tenantName"
      scale="wall"
      :live="config.public.apiMock"
    />
    <UButton
      color="neutral"
      variant="solid"
      icon="i-lucide-minimize-2"
      :label="t('dashboards.exitWall')"
      class="absolute end-3 bottom-3 transition-opacity focus-visible:opacity-100"
      :class="exitVisible ? 'opacity-100' : 'opacity-0'"
      data-testid="exit-wall"
      @click="exitWall"
      @focus="poke"
    />
  </div>

  <AppPage v-else :title="dashboard?.name ?? t('dashboards.title')" panel-id="dashboard">
    <template #navbar-right>
      <UBadge
        v-if="dashboard?.visibility === 'system'"
        color="neutral"
        variant="subtle"
        icon="i-lucide-lock"
        :label="t('dashboards.systemReadOnly')"
        class="hidden sm:inline-flex"
      />
      <UButton
        v-if="dashboard"
        color="neutral"
        variant="outline"
        icon="i-lucide-maximize-2"
        :aria-label="t('dashboards.wall')"
        :to="{ path: route.path, query: { scale: 'wall' } }"
        data-testid="open-wall"
      >
        <!-- En móvil solo el icono: el título de la página necesita el ancho. -->
        <span class="hidden sm:inline">{{ t('dashboards.wall') }}</span>
      </UButton>
    </template>

    <LoadingState v-if="status === 'pending' && !dashboard" :rows="4" />
    <ErrorState
      v-else-if="error"
      :error="error"
      :title="t('dashboards.loadError')"
      @retry="refresh()"
    />
    <DashboardGrid
      v-else-if="dashboard"
      :dashboard="dashboard"
      :tenant-name="tenantName"
      :live="config.public.apiMock"
    />
  </AppPage>
</template>
