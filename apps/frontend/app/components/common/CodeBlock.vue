<script setup lang="ts">
/**
 * Bloque de comandos o script (monoespaciado). Las líneas se parten en lugar de desbordar
 * (320 px sin scroll horizontal, WCAG 1.4.10); "Copiar" y, opcionalmente, "Descargar".
 */
const props = withDefaults(
  defineProps<{
    code: string
    label: string
    /** Nombre del archivo para "Descargar" (p. ej. `alta-rt-centro.rsc`). */
    filename?: string
    maxHeight?: string
    copyLabel?: string
  }>(),
  { filename: undefined, maxHeight: undefined, copyLabel: undefined },
)

const { t } = useI18n()

function download() {
  const blob = new Blob([props.code], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = props.filename!
  a.click()
  URL.revokeObjectURL(url)
}
</script>

<template>
  <figure class="bg-muted ring-default min-w-0 rounded-md ring-1">
    <figcaption
      class="border-default flex min-w-0 flex-wrap items-center justify-between gap-2 border-b px-3 py-1.5"
    >
      <span class="text-muted min-w-0 text-xs font-medium">{{ label }}</span>
      <span class="flex shrink-0 gap-1.5">
        <CopyButton :value="code" size="xs" variant="ghost" :label="copyLabel" />
        <UButton
          v-if="filename"
          size="xs"
          color="neutral"
          variant="ghost"
          icon="i-lucide-download"
          :label="t('common.download', { name: filename.split('.').pop() })"
          @click="download"
        />
      </span>
    </figcaption>
    <pre
      class="text-default m-0 overflow-y-auto px-3 py-2 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap"
      :style="maxHeight ? { maxHeight } : undefined"
      tabindex="0"
      :aria-label="label"
    ><code>{{ code }}</code></pre>
  </figure>
</template>
