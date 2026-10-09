<script setup lang="ts">
/**
 * Dashboards del ISP (frontend.md §6). En I0 solo las plantillas de Horus ("NOC del ISP",
 * "Seguridad"), de solo lectura; crear, duplicar y editar llegan en I2.
 */
const { t } = useI18n()
const route = useRoute()
useSectionGuard()(findSection('tenant', 'dashboards'), String(route.params.slug))

const { data: dashboards, error, status, refresh } = useDashboardList()
const slug = computed(() => String(route.params.slug))

const ICONS: Record<string, string> = {
  noc_isp: 'i-lucide-monitor-dot',
  security: 'i-lucide-shield-alert',
}
</script>

<template>
  <AppPage :title="t('dashboards.title')" panel-id="dashboards">
    <p class="text-muted max-w-prose">{{ t('dashboards.intro') }}</p>
    <LoadingState v-if="status === 'pending' && !dashboards" :rows="2" />
    <ErrorState
      v-else-if="error"
      :error="error"
      :title="t('dashboards.listError')"
      @retry="refresh()"
    />
    <EmptyState
      v-else-if="dashboards && dashboards.length === 0"
      icon="i-lucide-layout-dashboard"
      :title="t('dashboards.empty')"
    />
    <ul v-else class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3" data-testid="dashboard-list">
      <li v-for="d in dashboards" :key="d.id">
        <ULink
          :to="`/t/${slug}/dashboards/${d.id}`"
          class="bg-default ring-default hover:bg-elevated/60 focus-visible:ring-primary flex h-full items-start gap-3 rounded-lg p-4 ring-1 transition-colors"
        >
          <UIcon
            :name="ICONS[d.template_key ?? ''] ?? 'i-lucide-layout-dashboard'"
            class="text-primary mt-0.5 size-5 shrink-0"
            aria-hidden="true"
          />
          <span class="min-w-0">
            <span class="text-highlighted block font-semibold">{{ d.name }}</span>
            <span class="text-muted block text-sm">
              {{ t(`dashboards.templates.${d.template_key ?? 'custom'}`) }}
            </span>
            <span class="text-dimmed mt-2 flex items-center gap-2 text-xs">
              <UBadge
                v-if="d.visibility === 'system'"
                color="neutral"
                variant="subtle"
                size="sm"
                :label="t('dashboards.system')"
              />
              {{ t('dashboards.widgetCount', d.widget_count) }}
            </span>
          </span>
        </ULink>
      </li>
    </ul>
  </AppPage>
</template>
