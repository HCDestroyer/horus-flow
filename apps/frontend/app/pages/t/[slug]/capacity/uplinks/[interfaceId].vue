<script setup lang="ts">
/**
 * Detalle de un enlace de subida (`GET /capacity/uplinks/{id}`): picos diarios vs capacidad
 * con el pronóstico y su banda, sondas de su puerta de enlace y la configuración del enlace
 * (permiso `snmp.manage`). Ventana de la serie en la URL (`days`).
 */
const { t } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const can = useCan()
const slug = computed(() => String(route.params.slug))
const interfaceId = computed(() => String(route.params.interfaceId))
useSectionGuard()(findSection('tenant', 'capacity'), slug.value)

const DAYS = ['30', '90', '182', '365'] as const
const days = useQueryParam('days', '90', DAYS)
const dayOptions = computed(() =>
  DAYS.map((d) => ({ label: t(`capacity.detail.days.${d}`), value: d })),
)

const { data, error, status, refresh } = useTenantQuery(
  () => `capacity:uplink:${interfaceId.value}:${days.value}`,
  () =>
    unwrap(
      $api.GET('/capacity/uplinks/{interface_id}', {
        params: { path: { interface_id: interfaceId.value }, query: { days: Number(days.value) } },
      }),
    ),
  { watch: [interfaceId, days] },
)
useAutoRefresh(() => refresh(), 120_000)

const uplink = computed(() => data.value?.uplink ?? null)
const configOpen = ref(false)
const title = computed(() => uplink.value?.label ?? t('capacity.detail.title'))
useHead({ title: () => title.value })
</script>

<template>
  <AppPage :title="title" panel-id="capacity-uplink">
    <template #toolbar-left>
      <UButton
        :to="`/t/${slug}/capacity`"
        color="neutral"
        variant="ghost"
        icon="i-lucide-arrow-left"
        :label="t('capacity.detail.back')"
      />
      <USelect
        v-model="days"
        :items="dayOptions"
        class="w-40"
        :aria-label="t('capacity.filters.series')"
        data-testid="detail-days"
      />
    </template>
    <template v-if="can('snmp.manage') && uplink" #toolbar-right>
      <UButton
        color="neutral"
        variant="outline"
        icon="i-lucide-settings-2"
        :label="t('capacity.actions.configureShort')"
        data-testid="detail-configure"
        @click="configOpen = true"
      />
    </template>
    <LoadingState v-if="status === 'pending' && !data" :rows="5" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <template v-else-if="data && uplink">
      <p class="text-muted flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
        <span class="font-mono">{{ uplink.interface_name }}</span>
        <span>{{ uplink.router_name }}</span>
        <span v-if="uplink.site_name">{{ uplink.site_name }}</span>
        <UBadge
          v-if="uplink.kind"
          color="neutral"
          variant="soft"
          size="sm"
          :label="t(`capacity.kind.${uplink.kind}`)"
        />
        <span v-if="uplink.provider">{{ uplink.provider }}</span>
      </p>
      <CapacitySubjectView :detail="data" />
    </template>
    <UplinkConfigModal v-model:open="configOpen" :uplink="uplink" @saved="refresh()" />
  </AppPage>
</template>
