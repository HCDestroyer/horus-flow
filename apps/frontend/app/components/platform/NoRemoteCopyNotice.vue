<script setup lang="ts">
/**
 * Aviso permanente "Sin copia remota configurada" (frontend.md §3.4, ADR-0019): mientras no
 * haya destino remoto, en Almacenamiento y en Estado del sistema, con su consecuencia y el
 * acceso para añadir uno (`action`: abrir el alta aquí; `to`: ir a Almacenamiento).
 */
const props = withDefaults(
  defineProps<{ action?: { label: string; onClick: () => void }; to?: string }>(),
  { action: undefined, to: undefined },
)
const { t } = useI18n()

const actions = computed(() => {
  if (props.action) {
    return [
      {
        label: props.action.label,
        icon: 'i-lucide-plus',
        color: 'warning' as const,
        variant: 'solid' as const,
        onClick: props.action.onClick,
        'data-testid': 'no-remote-copy-add',
      },
    ]
  }
  if (props.to) {
    return [
      {
        label: t('platform.noRemote.configure'),
        icon: 'i-lucide-arrow-right',
        color: 'warning' as const,
        variant: 'outline' as const,
        to: props.to,
        'data-testid': 'no-remote-copy-link',
      },
    ]
  }
  return []
})
</script>

<template>
  <UAlert
    color="warning"
    variant="subtle"
    icon="i-lucide-hard-drive-download"
    :title="t('platform.noRemote.title')"
    :description="t('platform.noRemote.body')"
    :actions="actions"
    data-testid="no-remote-copy"
  />
</template>
