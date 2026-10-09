<script setup lang="ts">
import type { CommandPaletteGroup, CommandPaletteItem, DropdownMenuItem } from '@nuxt/ui'

/**
 * Selector de ISP (frontend.md §3.2, I0-15): patrón *workspace switcher* desde la cabecera
 * de la barra lateral. Solo aparece con más de un ISP (con uno, `TenantHeader` muestra el
 * nombre como texto). Con más de 7 ISP incluye búsqueda. El cambio conserva la sección,
 * pide el token del ISP nuevo y vacía cachés y suscripciones del anterior.
 */
const props = withDefaults(defineProps<{ collapsed?: boolean }>(), { collapsed: false })

const { t } = useI18n()
const { membership, memberships, switchTo } = useTenant()

// Componentes resueltos en tiempo de compilación (el contenedor cambia con la búsqueda).
const UPopover = resolveComponent('UPopover')
const UDropdownMenu = resolveComponent('UDropdownMenu')

const SEARCH_THRESHOLD = 7
const open = ref(false)
const searchable = computed(() => memberships.value.length > SEARCH_THRESHOLD)

function monogram(name: string) {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0]?.toUpperCase())
    .join('')
}

const current = computed(() => membership.value)

async function select(slug: string) {
  open.value = false
  await switchTo(slug)
}

const items = computed<DropdownMenuItem[][]>(() => [
  [{ type: 'label', label: t('tenant.switchTo') }],
  memberships.value.map((m) => ({
    label: m.tenant_name,
    type: 'checkbox' as const,
    checked: m.tenant_slug === current.value?.tenant_slug,
    avatar: { text: monogram(m.tenant_name), size: '2xs' as const },
    onSelect: (e: Event) => {
      e.preventDefault()
      select(m.tenant_slug)
    },
  })),
])

const groups = computed<CommandPaletteGroup<CommandPaletteItem>[]>(() => [
  {
    id: 'tenants',
    label: t('tenant.switchTo'),
    items: memberships.value.map((m) => ({
      id: m.tenant_slug,
      label: m.tenant_name,
      avatar: { text: monogram(m.tenant_name) },
      active: m.tenant_slug === current.value?.tenant_slug,
      onSelect: () => select(m.tenant_slug),
    })),
  },
])
</script>

<template>
  <component
    :is="searchable ? UPopover : UDropdownMenu"
    v-model:open="open"
    v-bind="
      searchable
        ? { content: { align: 'start', side: 'bottom' } }
        : { items, content: { align: 'start' }, ui: { content: 'min-w-60' } }
    "
  >
    <UButton
      color="neutral"
      variant="ghost"
      block
      :square="props.collapsed"
      class="data-[state=open]:bg-elevated -mx-1.5 justify-start gap-2.5 px-1.5 py-1"
      :aria-label="t('tenant.switcherLabel', { tenant: current?.tenant_name ?? '' })"
      data-testid="tenant-switcher"
    >
      <span
        class="bg-primary text-inverted flex size-8 shrink-0 items-center justify-center rounded-md text-sm font-semibold"
        aria-hidden="true"
      >
        {{ monogram(current?.tenant_name ?? '') }}
      </span>
      <span v-if="!props.collapsed" class="min-w-0 flex-1 text-start">
        <span
          class="text-muted block text-[11px] leading-tight font-medium tracking-wide uppercase"
        >
          {{ t('tenant.current') }}
        </span>
        <span class="text-highlighted block truncate text-sm leading-tight font-semibold">
          {{ current?.tenant_name }}
        </span>
      </span>
      <UIcon
        v-if="!props.collapsed"
        name="i-lucide-chevrons-up-down"
        class="text-dimmed size-4 shrink-0"
        aria-hidden="true"
      />
    </UButton>

    <template v-if="searchable" #content>
      <UCommandPalette
        :groups="groups"
        :placeholder="t('tenant.searchPlaceholder')"
        class="h-80 w-72"
        data-testid="tenant-search"
      />
    </template>
  </component>
</template>
