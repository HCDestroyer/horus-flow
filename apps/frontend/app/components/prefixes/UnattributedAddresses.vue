<script setup lang="ts">
import type { RelativeRange } from '~~/types/api'

/**
 * Detalle de un rango sin atribuir (frontend.md §8.3): las IP del rango que más tráfico
 * generan en el periodo, para decidir si declararlo como clientes o localizar una IP
 * concreta. Dato personal: solo con `customers.read` (la API lo exige también).
 */
const props = defineProps<{ siteId: string; prefix: string; range: RelativeRange }>()
const { t, d } = useI18n()
const { $api } = useNuxtApp()

const { data, error, status, refresh } = useTenantQuery(
  () => `traffic:unattributed-addresses:${props.siteId}:${props.prefix}:${props.range}`,
  () =>
    unwrap(
      $api.POST('/sites/{site_id}/unattributed-addresses', {
        params: { path: { site_id: props.siteId } },
        body: { prefix: props.prefix, range: props.range, n: 50 },
      }),
    ).then((r) => r.data),
  { watch: [() => props.range] },
)
const total = computed(() => (data.value ?? []).reduce((acc, r) => acc + Number(r.bytes), 0))
</script>

<template>
  <div class="mt-2 ml-0 sm:ml-4" :data-testid="`unattributed-addresses`" :data-prefix="prefix">
    <div v-if="status === 'pending' && !data" class="py-2">
      <LoadingState :rows="3" />
    </div>
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <p v-else-if="!data?.length" class="text-muted py-2 text-sm">
      {{ t('traffic.rest.addresses.empty') }}
    </p>
    <div v-else class="overflow-x-auto">
      <p class="text-muted mb-1 text-xs">
        {{ t('traffic.rest.addresses.hint', { n: data.length }) }}
      </p>
      <table class="w-full text-sm">
        <thead class="text-muted text-xs">
          <tr>
            <th class="py-1 pr-3 text-left font-normal">{{ t('traffic.rest.addresses.ip') }}</th>
            <th class="py-1 pr-3 text-right font-normal">
              {{ t('traffic.rest.addresses.bytes') }}
            </th>
            <th class="hidden py-1 pr-3 text-right font-normal sm:table-cell">
              {{ t('traffic.rest.addresses.share') }}
            </th>
            <th class="py-1 text-right font-normal">{{ t('traffic.rest.addresses.lastSeen') }}</th>
          </tr>
        </thead>
        <tbody class="divide-default divide-y">
          <tr v-for="r in data" :key="r.address" :data-address="r.address">
            <td class="text-highlighted py-1.5 pr-3 font-mono">{{ r.address }}</td>
            <td class="py-1.5 pr-3 text-right tabular">
              {{ joinUnit(formatBytes(Number(r.bytes))) }}
            </td>
            <td class="text-muted hidden py-1.5 pr-3 text-right tabular sm:table-cell">
              {{ total ? formatPercent(Number(r.bytes) / total) : '—' }}
            </td>
            <td class="text-muted py-1.5 text-right whitespace-nowrap">
              <time :datetime="r.last_seen_at" :title="d(new Date(r.last_seen_at), 'long')">
                {{ d(new Date(r.last_seen_at), 'short') }}
              </time>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
