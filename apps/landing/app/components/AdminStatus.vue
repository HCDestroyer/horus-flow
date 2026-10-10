<script setup lang="ts">
// Estado de una solicitud: texto + punto de color (el color nunca va solo).
const props = defineProps<{ status: string }>()
const tone: Record<string, string> = {
  new: 'bg-(--ui-primary)',
  pending_payment: 'bg-amber-500',
  contacted: 'bg-slate-400',
  paid: 'bg-emerald-600',
  cancelled: 'bg-transparent ring-1 ring-(--ui-text-muted)',
}
</script>

<template>
  <span class="inline-flex items-center gap-1.5 text-sm whitespace-nowrap text-highlighted">
    <span
      class="size-2 rounded-full"
      :class="tone[props.status] ?? 'bg-slate-400'"
      aria-hidden="true"
    />
    {{ STATUS_LABELS[props.status] ?? props.status }}
  </span>
</template>
