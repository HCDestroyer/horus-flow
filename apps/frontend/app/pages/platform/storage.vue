<script setup lang="ts">
/**
 * Almacenamiento (I1-31, frontend.md §3.4, ADR-0019): uso del disco local y destinos
 * remotos. Sin destino remoto, aviso permanente "Sin copia remota configurada". El alta de
 * destinos (SFTP, Drive, MEGA, Dropbox) llega en I3.
 */
const { t } = useI18n()
const { $api } = useNuxtApp()
useSectionGuard()(findSection('platform', '/platform/storage'))

const {
  data: destinations,
  error,
  refresh,
} = useTenantQuery('platform:destinations', () =>
  unwrap($api.GET('/platform/remote-destinations')).then((r) => r.data),
)
const { data: system } = useTenantQuery('platform:system', () => unwrap($api.GET('/system/status')))
const local = computed(() => system.value?.components.find((c) => c.name === 'local_storage'))
const ratio = computed(() => system.value?.disk_usage_ratio ?? null)
</script>

<template>
  <AppPage :title="t('nav.items.platformStorage')" panel-id="platform-storage">
    <DiskUsageNotice :ratio="ratio" />
    <NoRemoteCopyNotice v-if="destinations && !destinations.length" />
    <ErrorState v-if="error" :error="error" @retry="refresh()" />

    <UCard data-testid="local-storage">
      <h2 class="text-highlighted text-base font-semibold">{{ t('platform.storage.local') }}</h2>
      <p v-if="ratio !== null" class="text-highlighted mt-2 text-2xl font-semibold tabular">
        {{ formatPercent(ratio) }}
      </p>
      <UProgress
        v-if="ratio !== null"
        :model-value="ratio * 100"
        :color="ratio >= 0.85 ? 'error' : 'primary'"
        class="mt-2"
        :aria-label="t('platform.storage.usage')"
      />
      <p class="text-muted mt-2 text-sm">{{ local?.detail ?? t('platform.storage.usage') }}</p>
      <p v-if="ratio !== null && ratio >= 0.85" class="text-error mt-1 text-sm">
        {{ t('platform.storage.high') }}
      </p>
      <p class="text-dimmed mt-3 text-xs">{{ t('platform.storage.byTypePending') }}</p>
    </UCard>

    <UCard>
      <h2 class="text-highlighted text-base font-semibold">{{ t('platform.storage.remote') }}</h2>
      <p v-if="destinations && !destinations.length" class="text-muted mt-2 text-sm">
        {{ t('platform.storage.remoteNone') }}
      </p>
      <ul v-else class="mt-2 flex flex-col gap-2">
        <li
          v-for="dst in destinations ?? []"
          :key="dst.id"
          class="flex flex-wrap justify-between gap-2 text-sm"
        >
          <span class="text-highlighted">{{ dst.name }} · {{ dst.kind.toUpperCase() }}</span>
          <span class="text-muted"
            >{{ dst.status }} · <RelativeTime :at="dst.last_success_at"
          /></span>
        </li>
      </ul>
    </UCard>
  </AppPage>
</template>
