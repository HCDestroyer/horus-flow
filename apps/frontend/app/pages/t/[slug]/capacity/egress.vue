<script setup lang="ts">
/**
 * Salida internacional agregada (`GET /capacity/egress`): suma de los enlaces marcados como
 * salida internacional, su pico diario frente a la capacidad agregada y el pronóstico con la
 * recomendación de upgrade. Solo con alcance completo del ISP.
 */
const { t } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const slug = computed(() => String(route.params.slug))
useSectionGuard()(findSection('tenant', 'capacity'), slug.value)

const DAYS = ['30', '90', '182', '365'] as const
const days = useQueryParam('days', '90', DAYS)
const dayOptions = computed(() =>
  DAYS.map((d) => ({ label: t(`capacity.detail.days.${d}`), value: d })),
)

const { data, error, status, refresh } = useTenantQuery(
  () => `capacity:egress:${days.value}`,
  () => unwrap($api.GET('/capacity/egress', { params: { query: { days: Number(days.value) } } })),
  { watch: [days] },
)
useAutoRefresh(() => refresh(), 120_000)
useHead({ title: () => t('capacity.egress.title') })
</script>

<template>
  <AppPage :title="t('capacity.egress.title')" panel-id="capacity-egress">
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
      />
    </template>
    <LoadingState v-if="status === 'pending' && !data" :rows="5" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <template v-else-if="data">
      <p class="text-muted text-sm">
        {{ t('capacity.egress.intro', { n: data.egress?.members.length ?? 0 }) }}
      </p>
      <CapacitySubjectView :detail="data" />
    </template>
  </AppPage>
</template>
