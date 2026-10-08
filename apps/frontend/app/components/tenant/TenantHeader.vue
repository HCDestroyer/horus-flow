<script setup lang="ts">
/**
 * Cabecera de la barra lateral: nombre del ISP con monograma (frontend.md §3.1). Responde
 * "¿dónde estoy?". Con un solo ISP es texto plano; el selector (`UDropdownMenu`) llega en
 * I0-15 por el slot `switcher`.
 */
const props = withDefaults(
  defineProps<{
    name: string
    collapsed?: boolean
    superadmin?: boolean
  }>(),
  { collapsed: false, superadmin: false },
)

const { t } = useI18n()

const monogram = computed(() =>
  props.name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0]?.toUpperCase())
    .join(''),
)
</script>

<template>
  <div class="flex min-w-0 items-center gap-2.5" data-testid="tenant-header">
    <slot name="switcher" :monogram="monogram">
      <div
        class="bg-primary text-inverted flex size-8 shrink-0 items-center justify-center rounded-md text-sm font-semibold"
        aria-hidden="true"
      >
        {{ monogram }}
      </div>
      <div v-if="!collapsed" class="min-w-0">
        <p class="text-muted text-[11px] leading-tight font-medium tracking-wide uppercase">
          {{ t('tenant.current') }}
        </p>
        <p class="text-highlighted truncate text-sm leading-tight font-semibold">{{ name }}</p>
      </div>
      <span v-else class="sr-only">{{ t('tenant.current') }}: {{ name }}</span>
    </slot>
    <UBadge
      v-if="superadmin && !collapsed"
      color="neutral"
      variant="outline"
      size="sm"
      :label="t('tenant.superadmin')"
      class="ms-auto"
    />
  </div>
</template>
