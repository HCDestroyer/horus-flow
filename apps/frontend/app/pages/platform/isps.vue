<script setup lang="ts">
/**
 * ISP de la instalación (frontend.md §3.3–§3.4): routers, exportadores *Exportando* /
 * *Silenciosos*, túneles caídos, flujos/s y hallazgos críticos abiertos (solo conteos, nunca
 * clientes). Clic → entra al ISP.
 */
const { t } = useI18n()
const { $api } = useNuxtApp()
const { me } = useAuth()
useSectionGuard()(findSection('platform', '/platform/isps'))

const { data, error, status, refresh } = useTenantQuery('platform:overview', () =>
  unwrap($api.GET('/platform/overview')),
)
const member = (slug: string) => me.value?.memberships.some((m) => m.tenant_slug === slug)
</script>

<template>
  <AppPage :title="t('nav.items.platformIsps')" panel-id="platform-isps">
    <LoadingState v-if="status === 'pending' && !data" :rows="3" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <ul v-else-if="data" class="grid gap-3 lg:grid-cols-3" data-testid="platform-isps">
      <li v-for="isp in data.data" :key="isp.tenant_id">
        <UCard :ui="{ body: 'p-4 sm:p-4 flex flex-col gap-3' }">
          <div class="flex items-start justify-between gap-2">
            <NuxtLink
              v-if="member(isp.slug)"
              :to="`/t/${isp.slug}`"
              class="text-highlighted text-base font-semibold hover:underline"
            >
              {{ isp.name }}
            </NuxtLink>
            <span v-else class="text-highlighted text-base font-semibold">{{ isp.name }}</span>
            <UBadge
              color="neutral"
              variant="subtle"
              :label="t(`platform.tenantStatus.${isp.status}`)"
            />
          </div>
          <dl class="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
            <div>
              <dt class="text-muted text-xs">{{ t('platform.isps.routers') }}</dt>
              <dd class="text-highlighted font-medium tabular">{{ isp.routers_total }}</dd>
            </div>
            <div>
              <dt class="text-muted text-xs">{{ t('platform.isps.exporting') }}</dt>
              <dd class="text-highlighted font-medium tabular">{{ isp.exporters_exporting }}</dd>
            </div>
            <div>
              <dt class="text-muted text-xs">{{ t('platform.isps.silent') }}</dt>
              <dd
                class="font-medium tabular"
                :class="isp.exporters_silent ? 'text-error' : 'text-highlighted'"
              >
                <ExporterStateBadge v-if="isp.exporters_silent" state="silent" class="me-1" />{{
                  isp.exporters_silent
                }}
              </dd>
            </div>
            <div>
              <dt class="text-muted text-xs">{{ t('platform.isps.fps') }}</dt>
              <dd class="text-highlighted font-medium tabular">
                {{ formatNumber(isp.ingest_flows_per_second) }}
              </dd>
            </div>
            <div>
              <dt class="text-muted text-xs">{{ t('platform.isps.critical') }}</dt>
              <dd class="text-highlighted font-medium tabular">
                <SeverityBadge
                  v-if="isp.open_findings_critical"
                  severity="critical"
                  class="me-1"
                />{{ isp.open_findings_critical }}
              </dd>
            </div>
            <div>
              <dt class="text-muted text-xs">{{ t('platform.isps.tunnelsDown') }}</dt>
              <dd class="text-highlighted font-medium tabular">{{ isp.tunnels_down ?? 0 }}</dd>
            </div>
          </dl>
        </UCard>
      </li>
    </ul>
  </AppPage>
</template>
