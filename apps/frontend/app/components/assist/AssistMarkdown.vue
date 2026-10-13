<script setup lang="ts">
import { parseMarkdown } from '~/utils/markdown'

/**
 * Respuesta del asistente de IA en Markdown seguro: se convierte a bloques (`utils/markdown.ts`)
 * y se pinta con plantillas, sin `v-html`. Los bloques de código (comandos RouterOS sugeridos)
 * salen en `CodeBlock` con su botón de copiar: Horus no los ejecuta.
 */
const props = defineProps<{ source: string }>()
const { t } = useI18n()
const blocks = computed(() => parseMarkdown(props.source))
const codeLabel = (lang: string) =>
  ['routeros', 'rsc', 'mikrotik', ''].includes(lang)
    ? t('assist.analysis.codeRouteros')
    : t('assist.analysis.code', { lang })
</script>

<template>
  <div class="flex min-w-0 flex-col gap-3 text-sm" data-testid="assist-markdown">
    <template v-for="(b, i) in blocks" :key="i">
      <component
        :is="`h${b.level}`"
        v-if="b.type === 'heading'"
        class="text-highlighted font-semibold"
        :class="b.level === 2 ? 'mt-1 text-base' : 'text-sm'"
      >
        <AssistInline :nodes="b.inline" />
      </component>
      <p v-else-if="b.type === 'paragraph'" class="text-default leading-relaxed break-words">
        <AssistInline :nodes="b.inline" />
      </p>
      <component
        :is="b.ordered ? 'ol' : 'ul'"
        v-else-if="b.type === 'list'"
        class="text-default flex flex-col gap-1 ps-5 leading-relaxed break-words"
        :class="b.ordered ? 'list-decimal' : 'list-disc'"
      >
        <li v-for="(item, j) in b.items" :key="j"><AssistInline :nodes="item" /></li>
      </component>
      <blockquote
        v-else-if="b.type === 'quote'"
        class="border-default text-muted border-s-2 ps-3 break-words"
      >
        <AssistInline :nodes="b.inline" />
      </blockquote>
      <CodeBlock
        v-else-if="b.type === 'code'"
        :code="b.text"
        :label="codeLabel(b.lang)"
        :copy-label="t('assist.analysis.copy')"
      />
      <USeparator v-else-if="b.type === 'hr'" />
    </template>
  </div>
</template>
