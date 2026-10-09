<script setup lang="ts">
import type { Finding } from '~~/types/api'

/**
 * Lista de hallazgos (frontend.md §8.2): severidad (icono + texto + color), resumen legible,
 * cliente, nodo, tipo, confianza, estado y última vez. Tabla en escritorio, tarjetas en
 * móvil. `fresh` marca los recién llegados por tiempo real (realce que se desvanece).
 */
const props = withDefaults(
  defineProps<{
    findings: Finding[]
    slug: string
    siteName: (id: string) => string
    showCustomer?: boolean
    fresh?: Set<string>
  }>(),
  { showCustomer: true, fresh: () => new Set() },
)
const { t, te } = useI18n()
const href = (f: Finding) => `/t/${props.slug}/security/findings/${f.id}`
const kindLabel = (k: string) =>
  te(`findingKind.${k}`) ? t(`findingKind.${k}`) : t('findings.kindOther')
</script>

<template>
  <div>
    <div class="ring-default bg-default hidden overflow-hidden rounded-lg ring-1 lg:block">
      <table class="w-full text-sm" data-testid="findings-table">
        <caption class="sr-only">
          {{
            t('findings.tableCaption')
          }}
        </caption>
        <thead class="bg-elevated/50 text-muted text-left text-xs">
          <tr>
            <th scope="col" class="px-3 py-2 font-medium">{{ t('findings.col.severity') }}</th>
            <th scope="col" class="px-3 py-2 font-medium">{{ t('findings.col.summary') }}</th>
            <th v-if="showCustomer" scope="col" class="px-3 py-2 font-medium">
              {{ t('findings.col.customer') }}
            </th>
            <th scope="col" class="px-3 py-2 font-medium">{{ t('findings.col.site') }}</th>
            <th scope="col" class="px-3 py-2 font-medium">{{ t('findings.col.confidence') }}</th>
            <th scope="col" class="px-3 py-2 font-medium">{{ t('findings.col.state') }}</th>
            <th scope="col" class="px-3 py-2 font-medium">{{ t('findings.col.lastSeen') }}</th>
          </tr>
        </thead>
        <tbody class="divide-default divide-y">
          <tr
            v-for="f in findings"
            :key="f.id"
            class="hover:bg-elevated/40 align-top transition-colors"
            :class="{ 'finding-fresh': fresh.has(f.id) }"
            :data-finding-id="f.id"
          >
            <td class="px-3 py-2.5"><SeverityBadge :severity="f.severity" /></td>
            <td class="px-3 py-2.5">
              <NuxtLink
                :to="href(f)"
                class="text-highlighted font-medium hover:underline"
                data-testid="finding-link"
              >
                {{ f.summary.text }}
              </NuxtLink>
              <span class="text-muted block text-xs">{{ kindLabel(f.kind) }}</span>
            </td>
            <td v-if="showCustomer" class="px-3 py-2.5">
              <NuxtLink
                v-if="f.customer && !f.customer.address_masked"
                :to="`/t/${slug}/clients/${f.customer_id}`"
                class="hover:underline"
              >
                <ClientAddress :address="f.customer.address" :alias="f.customer.alias" />
              </NuxtLink>
              <ClientAddress
                v-else-if="f.customer"
                :address="f.customer.address"
                :alias="f.customer.alias"
              />
            </td>
            <td class="text-muted px-3 py-2.5">{{ siteName(f.site_id) }}</td>
            <td class="px-3 py-2.5 whitespace-nowrap">
              <ConfidenceLabel :level="f.confidence_level" :value="f.confidence" />
            </td>
            <td class="px-3 py-2.5"><FindingStateBadge :state="f.state" /></td>
            <td class="text-muted px-3 py-2.5 whitespace-nowrap">
              <RelativeTime :at="f.last_seen_at" />
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <ul class="flex flex-col gap-2 lg:hidden" data-testid="findings-cards">
      <li
        v-for="f in findings"
        :key="f.id"
        :class="{ 'finding-fresh': fresh.has(f.id) }"
        class="rounded-md"
      >
        <NuxtLink
          :to="href(f)"
          class="bg-default ring-default hover:bg-elevated flex flex-col gap-1.5 rounded-md p-3 ring-1"
        >
          <span class="flex flex-wrap items-center justify-between gap-2">
            <SeverityBadge :severity="f.severity" />
            <RelativeTime :at="f.last_seen_at" class="text-muted text-xs" />
          </span>
          <span class="text-highlighted font-medium">{{ f.summary.text }}</span>
          <span class="text-muted flex flex-wrap gap-x-3 gap-y-1 text-sm">
            <ClientAddress
              v-if="showCustomer && f.customer"
              :address="f.customer.address"
              :alias="f.customer.alias"
              :stacked="false"
            />
            <span>{{ siteName(f.site_id) }}</span>
            <ConfidenceLabel :level="f.confidence_level" :value="f.confidence" />
            <FindingStateBadge :state="f.state" />
          </span>
        </NuxtLink>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.finding-fresh {
  animation: finding-fresh 4s ease-out;
}
@keyframes finding-fresh {
  from {
    background-color: color-mix(in oklab, var(--ui-primary) 18%, transparent);
  }
  to {
    background-color: transparent;
  }
}
@media (prefers-reduced-motion: reduce) {
  .finding-fresh {
    animation: none;
    background-color: color-mix(in oklab, var(--ui-primary) 10%, transparent);
  }
}
</style>
