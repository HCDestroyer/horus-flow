<script setup lang="ts">
/**
 * "Copiar" (frontend.md §11, secretos y comandos): copia al portapapeles y lo confirma en el
 * propio botón (feedback en el sitio, sin toast) durante 2 s.
 */
const props = withDefaults(
  defineProps<{
    value: string
    label?: string
    size?: 'xs' | 'sm' | 'md'
    variant?: 'outline' | 'ghost' | 'soft' | 'solid'
  }>(),
  { label: undefined, size: 'sm', variant: 'outline' },
)

const { t } = useI18n()
const copied = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined

async function copy() {
  try {
    await navigator.clipboard.writeText(props.value)
  } catch {
    // Sin permiso de portapapeles (HTTP, iframe): selección manual como alternativa.
    const area = document.createElement('textarea')
    area.value = props.value
    document.body.appendChild(area)
    area.select()
    document.execCommand('copy')
    area.remove()
  }
  copied.value = true
  clearTimeout(timer)
  timer = setTimeout(() => (copied.value = false), 2000)
}
onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <UButton
    :size="size"
    color="neutral"
    :variant="variant"
    :icon="copied ? 'i-lucide-check' : 'i-lucide-copy'"
    :label="copied ? t('common.copied') : (label ?? t('common.copy'))"
    :aria-live="copied ? 'polite' : undefined"
    data-testid="copy-button"
    @click="copy"
  />
</template>
