<script setup lang="ts">
import type { SecurityState } from '~~/types/api'

/**
 * Estado de seguridad del cliente (D18; `widget-data.schema.json#/$defs/SecurityState`):
 * `infected` se muestra como "Infectado", siempre junto a su confianza (nunca la palabra
 * sola). Icono + texto + color.
 */
const props = defineProps<{ state: SecurityState | string; confidence?: number | null }>()
const { t, te } = useI18n()

const VISUAL: Record<string, { cls: string; icon: string }> = {
  infected: { cls: 'text-error', icon: 'i-lucide-bug' },
  suspected: { cls: 'text-warning', icon: 'i-lucide-scan-eye' },
  mitigated: { cls: 'text-muted', icon: 'i-lucide-shield-check' },
  clean: { cls: 'text-muted', icon: 'i-lucide-shield' },
}
const visual = computed(() => VISUAL[props.state] ?? VISUAL.clean!)
const label = computed(() =>
  te(`securityState.${props.state}`) ? t(`securityState.${props.state}`) : String(props.state),
)
</script>

<template>
  <span
    class="inline-flex shrink-0 items-center gap-1 font-medium"
    :class="visual.cls"
    :data-security-state="state"
  >
    <UIcon :name="visual.icon" class="size-[1.1em] shrink-0" aria-hidden="true" />
    <span>{{ label }}</span>
    <span
      v-if="confidence !== undefined && confidence !== null"
      class="text-muted font-normal tabular"
    >
      · {{ t('securityState.confidence', { pct: formatPercent(confidence) }) }}
    </span>
  </span>
</template>
