<script setup lang="ts">
/**
 * Switches de un nodo en su ficha (salud del nodo con switches): estado de cada switch y sus
 * uplinks caídos o inestables (`GET /switches/status?site_id=`), con enlace a su ficha.
 */
const props = defineProps<{ siteId: string }>()
const { t } = useI18n()
const { $api } = useNuxtApp()
const route = useRoute()
const slug = computed(() => String(route.params.slug))

const { data, error, refresh } = useTenantQuery(
  () => `switches:site:${props.siteId}`,
  () =>
    unwrap($api.GET('/switches/status', { params: { query: { site_id: props.siteId } } })).then(
      (r) => r.data,
    ),
  { watch: [() => props.siteId] },
)
useAutoRefresh(() => refresh(), 60_000)
</script>

<template>
  <section class="flex flex-col gap-3" data-testid="node-switches">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h2 class="text-highlighted text-base font-semibold">{{ t('switches.nodeCard.title') }}</h2>
      <UButton
        color="neutral"
        variant="ghost"
        size="sm"
        icon="i-lucide-network"
        :label="t('switches.nodeCard.all')"
        :to="`/t/${slug}/switches?site=${siteId}`"
      />
    </div>
    <ErrorState v-if="error" :error="error" @retry="refresh()" />
    <p v-else-if="data && !data.length" class="text-muted text-sm">
      {{ t('switches.nodeCard.empty') }}
    </p>
    <UCard
      v-for="s in data ?? []"
      :key="s.router_id"
      :ui="{ body: 'p-4 sm:p-4' }"
      :data-switch="s.name"
    >
      <div class="flex flex-wrap items-center justify-between gap-3">
        <NuxtLink
          :to="`/t/${slug}/switches/${s.router_id}`"
          class="text-highlighted font-mono font-semibold hover:underline"
          >{{ s.name }}</NuxtLink
        >
        <div class="flex flex-wrap items-center gap-3 text-sm">
          <span class="text-muted tabular">
            {{ t('switches.ports.summary', { monitored: s.ports_monitored, max: 64 }) }}
          </span>
          <UBadge
            v-if="s.uplinks_down"
            color="error"
            variant="subtle"
            size="sm"
            icon="i-lucide-circle-x"
            :label="t('switches.uplinksDown', { n: s.uplinks_down })"
          />
          <RouterStatusBadge :status="s.status" :reason="s.reason" compact />
        </div>
      </div>
    </UCard>
  </section>
</template>
