<script setup lang="ts">
import type { DropdownMenuItem } from '@nuxt/ui'

/**
 * Menú de usuario al pie de la barra lateral: cuenta, apariencia Sistema/Claro/Oscuro
 * (frontend.md §5.3, desviación justificada de HIG dark-mode para salas NOC) y cerrar sesión.
 */
const props = withDefaults(
  defineProps<{
    name: string
    email: string
    collapsed?: boolean
  }>(),
  { collapsed: false },
)

const emit = defineEmits<{ logout: [] }>()

const { t } = useI18n()
const colorMode = useColorMode()

type ThemePreference = 'system' | 'light' | 'dark'

function setTheme(value: ThemePreference) {
  colorMode.preference = value
}

const themeOptions = computed(() =>
  (
    [
      { value: 'system', label: t('user.themeSystem'), icon: 'i-lucide-monitor' },
      { value: 'light', label: t('user.themeLight'), icon: 'i-lucide-sun' },
      { value: 'dark', label: t('user.themeDark'), icon: 'i-lucide-moon' },
    ] as const
  ).map((o) => ({
    label: o.label,
    icon: o.icon,
    type: 'checkbox' as const,
    checked: colorMode.preference === o.value,
    onUpdateChecked: (checked: boolean) => checked && setTheme(o.value),
    onSelect: (e: Event) => e.preventDefault(),
  })),
)

const items = computed<DropdownMenuItem[][]>(() => [
  [{ type: 'label', label: props.name, description: props.email, avatar: { alt: props.name } }],
  [{ label: t('user.account'), icon: 'i-lucide-user', to: '/account' }],
  [{ type: 'label', label: t('user.theme') }, ...themeOptions.value],
  [
    {
      label: t('user.logout'),
      icon: 'i-lucide-log-out',
      onSelect: () => emit('logout'),
    },
  ],
])
</script>

<template>
  <UDropdownMenu
    :items="items"
    :content="{ align: 'center', collisionPadding: 12 }"
    :ui="{ content: collapsed ? 'w-56' : 'w-(--reka-dropdown-menu-trigger-width) min-w-56' }"
  >
    <UButton
      :avatar="{ alt: name }"
      :label="collapsed ? undefined : name"
      :aria-label="collapsed ? `${t('user.menu')}: ${name}` : undefined"
      :trailing-icon="collapsed ? undefined : 'i-lucide-chevrons-up-down'"
      color="neutral"
      variant="ghost"
      block
      :square="collapsed"
      class="data-[state=open]:bg-elevated"
      :ui="{ trailingIcon: 'text-muted' }"
      data-testid="user-menu"
    />
  </UDropdownMenu>
</template>
