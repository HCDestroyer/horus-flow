<script setup lang="ts">
import type { MdInline } from '~/utils/markdown'

/** Texto en línea de una respuesta del asistente (negrita, cursiva y código; siempre texto). */
defineProps<{ nodes: MdInline[] }>()
</script>

<template>
  <template v-for="(n, i) in nodes" :key="i">
    <strong v-if="n.type === 'strong'" class="text-highlighted font-semibold">{{ n.text }}</strong>
    <em v-else-if="n.type === 'em'">{{ n.text }}</em>
    <code
      v-else-if="n.type === 'code'"
      class="bg-elevated rounded px-1 py-0.5 font-mono text-xs break-all"
      >{{ n.text }}</code
    >
    <template v-else>{{ n.text }}</template>
  </template>
</template>
