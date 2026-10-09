<script setup lang="ts">
/**
 * Ficha del nodo (I1-19): su router principal con el estado del exportador y los prefijos
 * de clientes del nodo (con importación, alta manual y propuestas del modo descubrimiento).
 */
const { t } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const { membership } = useTenant()
const slug = computed(() => String(route.params.slug))
const id = computed(() => String(route.params.nodeId))

useSectionGuard()(findSection('tenant', 'nodes'), slug.value)

const {
  data: site,
  error,
  status,
  refresh,
} = useTenantQuery(
  () => `site:${id.value}`,
  () => unwrap($api.GET('/sites/{site_id}', { params: { path: { site_id: id.value } } })),
)
watch(error, (e) => {
  if (e instanceof ApiError && e.status === 404) {
    showError(createError({ statusCode: 404, statusMessage: 'Not Found' }))
  }
})
const { data: routers } = useTenantQuery(
  () => `routers:${id.value}`,
  () =>
    unwrap($api.GET('/routers', { params: { query: { site_id: id.value } } })).then((r) => r.data),
)
const primary = computed(() => routers.value?.find((r) => r.id === site.value?.primary_router_id))
const { data: exporter } = useTenantQuery(
  () => `router:${primary.value?.id ?? ''}:exporter`,
  () =>
    primary.value
      ? unwrap(
          $api.GET('/flow-exporters/{router_id}', {
            params: { path: { router_id: primary.value.id } },
          }),
        )
      : Promise.resolve(null),
  { watch: [() => primary.value?.id] },
)

const breadcrumb = computed(() => [
  { label: membership.value?.tenant_name ?? '', to: `/t/${slug.value}` },
  { label: t('nav.items.nodes'), to: `/t/${slug.value}/nodes` },
  { label: site.value?.name ?? '' },
])
useHead({ title: () => site.value?.name ?? t('nav.items.nodes') })
</script>

<template>
  <AppPage :title="site?.name ?? t('nav.items.nodes')" panel-id="node-detail">
    <UBreadcrumb :items="breadcrumb" class="min-w-0" />
    <LoadingState v-if="status === 'pending' && !site" :rows="4" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <template v-else-if="site">
      <UCard v-for="r in routers ?? []" :key="r.id" :ui="{ body: 'p-4 sm:p-4' }">
        <div class="flex flex-col gap-2 md:flex-row md:items-center md:justify-between">
          <div class="flex min-w-0 flex-col gap-1">
            <NuxtLink
              :to="`/t/${slug}/routers/${r.id}`"
              class="text-highlighted font-mono font-semibold hover:underline"
              >{{ r.name }}</NuxtLink
            >
            <span class="text-muted text-sm">{{ r.display_name }} · {{ r.model ?? '—' }}</span>
          </div>
          <div class="flex flex-wrap items-center gap-3">
            <ExporterStateBadge
              v-if="r.id === primary?.id"
              :state="exporter?.state ?? 'pending_configuration'"
            />
            <UButton
              v-if="r.onboarding_state === 'pending_configuration'"
              :to="`/t/${slug}/routers/${r.id}/connection`"
              icon="i-lucide-plug"
              :label="t('nodes.connect')"
              size="sm"
            />
            <UButton
              v-else
              :to="`/t/${slug}/routers/${r.id}`"
              color="neutral"
              variant="outline"
              size="sm"
              :label="t('nodes.openRouter')"
            />
          </div>
        </div>
      </UCard>
      <PrefixesPanel
        :site-id="site.id"
        :router-id="primary?.id ?? null"
        :router-ready="primary ? primary.onboarding_state !== 'pending_configuration' : false"
      />
    </template>
  </AppPage>
</template>
