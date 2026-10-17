<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { Customer } from '~~/types/api'

/**
 * Posibles comerciales (I2, roadmap §I2, frontend.md §8.1): clientes con sugerencia comercial
 * del scoring o uso comercial sospechado y sin tipo fijado a mano
 * (`GET /customers?commercial_candidate=true`). Cada fila explica por qué (razones con los
 * números reales) y se confirma o descarta con `set-kind` (permiso `customers.kind.write`),
 * que bloquea el tipo y la saca de la lista. La IP nunca va en la URL.
 */
const { t } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const can = useCan()
const slug = computed(() => String(route.params.slug))
useSectionGuard()(findSection('tenant', 'clients'), slug.value)

const { siteName } = useSites()
const { decide, busy } = useKindDecision()
const canKind = computed(() => can('customers.kind.write'))

const rows = ref<Customer[]>([])
const cursor = ref<string | null>(null)
const loading = ref(false)
const loadingMore = ref(false)
const error = shallowRef<unknown>(null)

function fetchPage(next: string | null) {
  return unwrap(
    $api.GET('/customers', {
      params: { query: { commercial_candidate: true, cursor: next ?? undefined, limit: 100 } },
    }),
  )
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const page = await fetchPage(null)
    rows.value = sortCandidates(page.data)
    cursor.value = page.page.next_cursor
  } catch (e) {
    error.value = e
    rows.value = []
  } finally {
    loading.value = false
  }
}

async function loadMore() {
  if (!cursor.value) return
  loadingMore.value = true
  try {
    const page = await fetchPage(cursor.value)
    rows.value = sortCandidates([...rows.value, ...page.data])
    cursor.value = page.page.next_cursor
  } catch (e) {
    error.value = e
  } finally {
    loadingMore.value = false
  }
}

onMounted(load)

async function onDecide(c: Customer, decision: 'confirm' | 'discard') {
  try {
    const updated = await decide(c, decision)
    if (updated) rows.value = rows.value.filter((r) => r.id !== c.id)
    else await load()
  } catch (e) {
    error.value = e
  }
}

const detailHref = (c: Customer) => `/t/${slug.value}/clients/${c.id}`
const prefixLen = (c: Customer) => (c.address.includes(':') ? 56 : null)
const levelIcon = (c: Customer) =>
  candidateLevel(c) === 'commercial' ? 'i-lucide-building-2' : 'i-lucide-building'
const levelLabel = (c: Customer) =>
  t(`clients.scoring.level.${candidateLevel(c) === 'commercial' ? 'commercial' : 'suspected'}`)

const columns = computed<TableColumn<Customer>[]>(() => [
  { id: 'client', header: () => t('clients.candidates.col.client') },
  { id: 'site', header: () => t('clients.candidates.col.site') },
  {
    id: 'score',
    header: () => t('clients.candidates.col.score'),
    meta: { class: { th: 'text-end', td: 'text-end' } },
  },
  { id: 'suggestion', header: () => t('clients.candidates.col.suggestion') },
  { id: 'reasons', header: () => t('clients.candidates.col.reasons') },
  ...(canKind.value
    ? [
        {
          id: 'actions',
          header: () => t('clients.candidates.col.actions'),
          meta: { class: { th: 'text-end', td: 'text-end' } },
        },
      ]
    : []),
])

useHead({ title: () => t('clients.candidates.title') })
</script>

<template>
  <AppPage :title="t('clients.candidates.title')" panel-id="clients-commercial">
    <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
      <p class="text-muted max-w-3xl text-sm">{{ t('clients.candidates.intro') }}</p>
      <UButton
        color="neutral"
        variant="ghost"
        icon="i-lucide-arrow-left"
        :label="t('clients.candidates.back')"
        :to="`/t/${slug}/clients`"
        class="self-start"
      />
    </div>

    <LoadingState v-if="loading && !rows.length" :rows="5" />
    <ErrorState v-else-if="error" :error="error" @retry="load" />
    <UCard v-else-if="!rows.length" class="py-6">
      <EmptyState
        icon="i-lucide-building-2"
        :title="t('clients.candidates.empty')"
        :description="t('clients.candidates.emptyHint')"
      />
    </UCard>

    <template v-else>
      <p class="text-muted text-sm" aria-live="polite" data-testid="candidates-count">
        {{ t('clients.candidates.count', { n: formatNumber(rows.length) }, rows.length) }}
      </p>

      <!-- Escritorio: tabla. -->
      <UCard class="hidden lg:block" :ui="{ body: 'p-0 sm:p-0' }">
        <UTable
          :data="rows"
          :columns="columns"
          :loading="loading"
          class="w-full"
          :ui="{ td: 'align-top py-2.5', th: 'text-xs whitespace-nowrap' }"
          data-testid="candidates-table"
        >
          <template #client-cell="{ row }">
            <NuxtLink
              :to="detailHref(row.original)"
              class="hover:underline focus-visible:underline"
              data-testid="candidate-link"
            >
              <ClientAddress
                :address="row.original.address"
                :alias="row.original.alias"
                :prefix-len="prefixLen(row.original)"
              />
            </NuxtLink>
          </template>
          <template #site-cell="{ row }">
            <span class="text-muted">{{ siteName(row.original.site_id) }}</span>
          </template>
          <template #score-cell="{ row }">
            <span class="text-highlighted font-semibold tabular" data-testid="candidate-score">{{
              commercialScore(row.original) ?? '—'
            }}</span>
            <span class="text-muted block text-xs tabular">/100</span>
          </template>
          <template #suggestion-cell="{ row }">
            <span class="flex flex-col items-start gap-1">
              <UBadge
                :color="scoreColor(commercialScore(row.original))"
                variant="subtle"
                :icon="levelIcon(row.original)"
                :label="levelLabel(row.original)"
              />
              <ClientKindBadge
                :kind="row.original.kind"
                :source="row.original.kind_source"
                :locked="row.original.kind_locked"
                :confidence="row.original.kind_confidence"
              />
            </span>
          </template>
          <template #reasons-cell="{ row }">
            <ul class="flex max-w-md flex-col gap-1 text-sm" data-testid="candidate-reasons">
              <li
                v-for="r in topReasons(row.original.suggested_reasons, 3)"
                :key="r.code"
                class="flex gap-1.5"
              >
                <UIcon
                  name="i-lucide-dot"
                  class="text-muted mt-0.5 size-4 shrink-0"
                  aria-hidden="true"
                />
                <span>{{ r.detail }}</span>
              </li>
            </ul>
          </template>
          <template #actions-cell="{ row }">
            <span class="inline-flex flex-wrap justify-end gap-2">
              <UButton
                size="sm"
                icon="i-lucide-building-2"
                :label="t('clients.candidates.confirm')"
                :loading="busy === row.original.id"
                data-testid="candidate-confirm"
                @click="onDecide(row.original, 'confirm')"
              />
              <UButton
                size="sm"
                color="neutral"
                variant="outline"
                icon="i-lucide-house"
                :label="t('clients.candidates.discard')"
                :disabled="busy === row.original.id"
                data-testid="candidate-discard"
                @click="onDecide(row.original, 'discard')"
              />
            </span>
          </template>
        </UTable>
      </UCard>

      <!-- Móvil y tableta: lista (frontend.md §5.2). -->
      <ul class="flex flex-col gap-2 lg:hidden" data-testid="candidates-list">
        <li
          v-for="c in rows"
          :key="c.id"
          class="bg-default ring-default flex flex-col gap-2 rounded-md p-3 ring-1"
        >
          <span class="flex min-w-0 items-start justify-between gap-2">
            <NuxtLink :to="detailHref(c)" class="hover:underline">
              <ClientAddress :address="c.address" :alias="c.alias" :prefix-len="prefixLen(c)" />
            </NuxtLink>
            <span class="text-highlighted shrink-0 font-semibold tabular"
              >{{ commercialScore(c) ?? '—' }}/100</span
            >
          </span>
          <span class="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
            <span class="text-muted">{{ siteName(c.site_id) }}</span>
            <UBadge
              :color="scoreColor(commercialScore(c))"
              variant="subtle"
              :icon="levelIcon(c)"
              :label="levelLabel(c)"
            />
          </span>
          <ul class="flex flex-col gap-1 text-sm">
            <li v-for="r in topReasons(c.suggested_reasons, 2)" :key="r.code" class="text-muted">
              {{ r.detail }}
            </li>
          </ul>
          <span v-if="canKind" class="flex flex-wrap gap-2">
            <UButton
              size="sm"
              icon="i-lucide-building-2"
              :label="t('clients.candidates.confirm')"
              :loading="busy === c.id"
              @click="onDecide(c, 'confirm')"
            />
            <UButton
              size="sm"
              color="neutral"
              variant="outline"
              icon="i-lucide-house"
              :label="t('clients.candidates.discard')"
              :disabled="busy === c.id"
              @click="onDecide(c, 'discard')"
            />
          </span>
        </li>
      </ul>

      <div v-if="cursor" class="flex justify-center">
        <UButton
          color="neutral"
          variant="outline"
          icon="i-lucide-chevrons-down"
          :loading="loadingMore"
          :label="t('common.loadMore')"
          @click="loadMore"
        />
      </div>
    </template>

    <p class="text-dimmed flex items-center gap-1.5 text-xs">
      <UIcon name="i-lucide-shield-check" class="size-4 shrink-0" aria-hidden="true" />
      {{ t('clients.privacy') }}
    </p>
  </AppPage>
</template>
