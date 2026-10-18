<script setup lang="ts">
import type { ProtectionLevel } from '~/utils/transport'

/**
 * Nivel de protección del transporte de un router (docs/tunnels.md §2): icono + texto + color,
 * nunca solo color. `unencrypted_internet` es un aviso permanente (error); `unencrypted_private`,
 * advertencia; `encrypted`, correcto.
 */
const props = withDefaults(
  defineProps<{ level: ProtectionLevel; mode?: string | null; compact?: boolean }>(),
  { mode: null, compact: false },
)
const { t } = useI18n()
const visual = computed(() => protectionVisual(props.level))
const label = computed(() =>
  props.compact
    ? t(`transport.protection.short.${props.level}`)
    : t(`transport.protection.${props.level}`),
)
</script>

<template>
  <UBadge
    :color="visual.color"
    variant="subtle"
    :icon="visual.icon"
    :label="mode ? `${t(`transport.mode.${mode}`)} · ${label}` : label"
    :title="t(`transport.protection.hint.${level}`)"
    :data-protection="level"
    data-testid="protection-badge"
  />
</template>
