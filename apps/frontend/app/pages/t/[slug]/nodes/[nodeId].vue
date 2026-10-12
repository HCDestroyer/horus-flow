<script setup lang="ts">
import type { DropdownMenuItem } from '@nuxt/ui'

/**
 * Ficha del nodo (I1-19): datos del nodo (editar con `If-Match`; borrar solo si no tiene
 * routers, `SITE_NOT_EMPTY`), sus routers con el estado del exportador del principal ("Agregar
 * router" abre el alta y sigue con el script de conexión) y los prefijos de clientes del nodo
 * (importación, alta manual, confirmación y propuestas del modo descubrimiento).
 */
const { t } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const can = useCan()
const toast = useToast()
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
const { data: routers, refresh: refreshRouters } = useTenantQuery(
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

// --- Editar, borrar y agregar router --------------------------------------------------------
const editOpen = ref(false)
const routerOpen = ref(false)
const deleteOpen = ref(false)
const deleting = ref(false)
const deleteBlocked = ref<string | null>(null)
const blockedByRouters = ref(false)

function routerSaved() {
  refreshRouters()
  refresh()
}

function openDelete() {
  deleteBlocked.value = null
  blockedByRouters.value = false
  deleteOpen.value = true
}

async function remove() {
  if (!site.value) return
  deleting.value = true
  deleteBlocked.value = null
  blockedByRouters.value = false
  try {
    await unwrap(
      $api.DELETE('/sites/{site_id}', {
        params: {
          path: { site_id: site.value.id },
          header: { 'If-Match': ifMatch(site.value.version) },
        },
      }),
    )
    deleteOpen.value = false
    toast.add({
      title: t('devices.site.deleted', { name: site.value.name }),
      icon: 'i-lucide-check',
      color: 'success',
    })
    // La lista de nodos se vuelve a pedir al entrar (sin la caché del nodo borrado).
    clearNuxtData((key) => key.endsWith(':sites'))
    await navigateTo(`/t/${slug.value}/nodes`)
  } catch (e) {
    if (e instanceof ApiError && e.code === 'SITE_NOT_EMPTY') {
      deleteBlocked.value = t('devices.site.notEmptyTitle')
      blockedByRouters.value = true
      refreshRouters()
    } else if (e instanceof ApiError && e.status === 412) {
      deleteBlocked.value = t('devices.site.deleteConflict')
      refresh()
    } else {
      deleteBlocked.value = t(describeError(e).messageKey)
    }
  } finally {
    deleting.value = false
  }
}

const more = computed<DropdownMenuItem[]>(() =>
  can('sites.delete')
    ? [
        {
          label: t('devices.site.delete'),
          icon: 'i-lucide-trash-2',
          color: 'error',
          onSelect: openDelete,
        },
      ]
    : [],
)

const facts = computed(() => {
  const s = site.value
  if (!s) return []
  return [
    { key: 'code', value: s.code },
    { key: 'kindLabel', value: s.kind ? t(`devices.site.kind.${s.kind}`) : null },
    { key: 'address', value: s.address },
    { key: 'timezone', value: s.timezone },
    {
      key: 'coordinates',
      value: s.latitude != null && s.longitude != null ? `${s.latitude}, ${s.longitude}` : null,
    },
  ]
})

const breadcrumb = computed(() => [
  { label: membership.value?.tenant_name ?? '', to: `/t/${slug.value}` },
  { label: t('nav.items.nodes'), to: `/t/${slug.value}/nodes` },
  { label: site.value?.name ?? '' },
])
useHead({ title: () => site.value?.name ?? t('nav.items.nodes') })
</script>

<template>
  <AppPage :title="site?.name ?? t('nav.items.nodes')" panel-id="node-detail">
    <template v-if="site" #navbar-right>
      <UButton
        v-if="can('sites.update')"
        color="neutral"
        variant="outline"
        icon="i-lucide-pencil"
        :label="t('devices.site.edit')"
        data-testid="edit-node"
        @click="editOpen = true"
      />
      <UDropdownMenu v-if="more.length" :items="more">
        <UButton
          color="neutral"
          variant="ghost"
          icon="i-lucide-ellipsis"
          :aria-label="t('common.more')"
          data-testid="node-more"
        />
      </UDropdownMenu>
    </template>
    <UBreadcrumb :items="breadcrumb" class="min-w-0" />
    <LoadingState v-if="status === 'pending' && !site" :rows="4" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <template v-else-if="site">
      <UCard :ui="{ body: 'p-4 sm:p-4' }" data-testid="node-facts">
        <dl class="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2 lg:grid-cols-5">
          <div v-for="f in facts" :key="f.key" class="flex min-w-0 flex-col" :data-fact="f.key">
            <dt class="text-muted">{{ t(`devices.site.${f.key}`) }}</dt>
            <dd class="text-default break-words">{{ f.value || '—' }}</dd>
          </div>
        </dl>
      </UCard>

      <section class="flex flex-col gap-3" data-testid="node-routers">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h2 class="text-highlighted text-base font-semibold">{{ t('devices.routers') }}</h2>
          <UButton
            v-if="can('devices.create') && routers?.length"
            icon="i-lucide-plus"
            :label="t('devices.nodes.addRouter')"
            data-testid="add-router"
            @click="routerOpen = true"
          />
        </div>
        <UCard v-if="routers && !routers.length" class="py-4" data-testid="node-routers-empty">
          <EmptyState
            icon="i-lucide-router"
            :title="t('devices.nodes.noRoutersTitle')"
            :description="t('devices.nodes.noRoutersBody')"
            :actions="
              can('devices.create')
                ? [
                    {
                      label: t('devices.nodes.addFirstRouter'),
                      icon: 'i-lucide-plus',
                      onClick: () => (routerOpen = true),
                    },
                  ]
                : []
            "
          />
          <div class="mx-auto mt-4 max-w-md">
            <SetupSteps current="router" />
          </div>
        </UCard>
        <UCard
          v-for="r in routers ?? []"
          :key="r.id"
          :ui="{ body: 'p-4 sm:p-4' }"
          :data-router="r.name"
        >
          <div class="flex flex-col gap-2 md:flex-row md:items-center md:justify-between">
            <div class="flex min-w-0 flex-col gap-1">
              <span class="flex flex-wrap items-center gap-2">
                <NuxtLink
                  :to="`/t/${slug}/routers/${r.id}`"
                  class="text-highlighted font-mono font-semibold hover:underline"
                  >{{ r.name }}</NuxtLink
                >
                <UBadge
                  :color="r.id === primary?.id ? 'primary' : 'neutral'"
                  variant="subtle"
                  size="sm"
                  :label="
                    r.id === primary?.id
                      ? t('devices.router.primaryBadge')
                      : t('devices.router.secondaryBadge')
                  "
                />
              </span>
              <span class="text-muted text-sm"
                >{{ r.display_name ?? '—' }} · {{ r.model ?? '—' }}</span
              >
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
      </section>

      <PrefixesPanel
        :site-id="site.id"
        :router-id="primary?.id ?? null"
        :router-ready="primary ? primary.onboarding_state !== 'pending_configuration' : false"
      />

      <SiteFormModal
        v-model:open="editOpen"
        :site="site"
        @saved="refresh()"
        @conflict="refresh()"
      />
      <RouterFormModal
        v-model:open="routerOpen"
        :site-id="site.id"
        :site-has-primary="!!site.primary_router_id"
        @saved="routerSaved"
      />
      <DeleteConfirmModal
        v-model:open="deleteOpen"
        :title="t('devices.site.deleteTitle', { name: site.name })"
        :confirm-label="t('devices.site.deleteSubmit')"
        :loading="deleting"
        :blocked="deleteBlocked"
        @confirm="remove"
      >
        <p class="text-default text-sm">{{ t('devices.site.deleteBody') }}</p>
        <template v-if="blockedByRouters" #blocked>
          <p>{{ t('devices.site.notEmptyBody', { n: routers?.length ?? 0 }) }}</p>
          <ul class="mt-1 list-disc ps-5">
            <li v-for="r in routers ?? []" :key="r.id">
              <NuxtLink :to="`/t/${slug}/routers/${r.id}`" class="font-mono underline">{{
                r.name
              }}</NuxtLink>
            </li>
          </ul>
        </template>
      </DeleteConfirmModal>
    </template>
  </AppPage>
</template>
