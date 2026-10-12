<script setup lang="ts">
/**
 * Qué hace (y qué no) la copia remota (ADR-0019, disaster-recovery.md §3.6): se cifra con age
 * antes de salir del servidor, el destino nunca ve los datos en claro, para restaurar hace falta
 * la llave age del paquete de secretos offline, Horus solo copia (no borra datos locales) y la
 * retención del destino borra allí las copias viejas.
 */
withDefaults(defineProps<{ retentionDays?: number | null; compact?: boolean }>(), {
  retentionDays: null,
  compact: false,
})
const { t } = useI18n()
</script>

<template>
  <section
    class="border-default bg-elevated/40 rounded-md border p-3 sm:p-4"
    aria-labelledby="remote-explainer-title"
    data-testid="remote-explainer"
  >
    <h3
      id="remote-explainer-title"
      class="text-highlighted flex items-center gap-2 text-sm font-semibold"
    >
      <UIcon name="i-lucide-shield-check" class="text-primary size-4 shrink-0" aria-hidden="true" />
      {{ t('platform.storage.explain.title') }}
    </h3>
    <ul class="text-default mt-2 flex list-disc flex-col gap-1.5 ps-5 text-sm">
      <li>{{ t('platform.storage.explain.encrypted') }}</li>
      <li>
        {{ t('platform.storage.explain.restore') }}
        <code class="text-highlighted font-mono text-xs break-all">horusctl secrets export</code>
        · {{ t('platform.storage.explain.docs') }}
        <code class="text-highlighted font-mono text-xs break-all"
          >docs/disaster-recovery.md §3.6</code
        >.
      </li>
      <li v-if="!compact">{{ t('platform.storage.explain.copyOnly') }}</li>
      <li>
        {{
          retentionDays
            ? t('platform.storage.explain.retentionN', { n: retentionDays })
            : t('platform.storage.explain.retention')
        }}
      </li>
    </ul>
  </section>
</template>
