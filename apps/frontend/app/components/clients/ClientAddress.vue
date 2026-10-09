<script setup lang="ts">
/**
 * Identidad del cliente (frontend.md §8.1, D22): IP en monoespaciada y, si existe, el alias.
 * Un cliente IPv6 es su **prefijo delegado** (`2001:db8:4a28:1800::/56`), independiente del
 * IPv4 del mismo abonado; se marca con "IPv6".
 */
const props = withDefaults(
  defineProps<{
    address: string
    alias?: string | null
    prefixLen?: number | null
    /** Alias encima y la IP debajo (tablas) o en línea. */
    stacked?: boolean
  }>(),
  { alias: null, prefixLen: null, stacked: true },
)
const { t } = useI18n()
const v6 = computed(() => props.address.includes(':'))
const shown = computed(() =>
  v6.value && props.prefixLen && !props.address.includes('/')
    ? `${props.address}/${props.prefixLen}`
    : props.address,
)
</script>

<template>
  <span
    class="inline-flex min-w-0 flex-col"
    :class="{ 'flex-row flex-wrap items-baseline gap-x-2': !stacked }"
  >
    <span v-if="alias" class="text-highlighted min-w-0 font-medium break-words">{{ alias }}</span>
    <span class="flex min-w-0 flex-wrap items-center gap-1.5">
      <span
        class="font-mono break-all"
        :class="alias ? 'text-muted text-xs' : 'text-highlighted'"
        data-testid="client-address"
        >{{ shown }}</span
      >
      <UBadge
        v-if="v6"
        size="sm"
        color="neutral"
        variant="outline"
        :label="t('clients.ipv6')"
        :title="t('clients.ipv6Hint')"
      />
    </span>
  </span>
</template>
