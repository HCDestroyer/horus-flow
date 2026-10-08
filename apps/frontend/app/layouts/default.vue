<script setup lang="ts">
import type { CommandPaletteGroup, CommandPaletteItem, NavigationMenuItem } from '@nuxt/ui'

/**
 * Shell principal (frontend.md §5.1): barra lateral con el ISP arriba, navegación agrupada
 * (máx. dos niveles, HIG sidebars), menú de usuario al pie y búsqueda ⌘K.
 */
const { t } = useI18n()
const auth = useAuth()
const { membership } = useTenant()
const navigation = useNavigation()

const open = ref(false)

const navItems = computed<NavigationMenuItem[][]>(() =>
  navigation.value.map((group) => [
    ...(group.labelKey ? [{ type: 'label' as const, label: t(group.labelKey) }] : []),
    ...group.sections.map((s) => ({
      label: t(s.labelKey),
      icon: s.icon,
      to: s.href,
      // "Resumen" solo activo en su ruta exacta; el resto, también en sus subrutas.
      exact: s.id === 'overview',
      onSelect: () => {
        open.value = false
      },
    })),
  ]),
)

const searchGroups = computed<CommandPaletteGroup<CommandPaletteItem>[]>(() => [
  {
    id: 'sections',
    label: t('nav.sections'),
    items: navigation.value.flatMap((group) =>
      group.sections.map((s) => ({
        id: s.id,
        label: t(s.labelKey),
        suffix: group.labelKey ? t(group.labelKey) : undefined,
        icon: s.icon,
        to: s.href,
      })),
    ),
  },
])

const tenantName = computed(() => membership.value?.tenant_name ?? t('app.name'))
</script>

<template>
  <div class="contents">
    <a
      href="#main-content"
      class="bg-default text-highlighted ring-primary sr-only z-50 rounded-md px-3 py-2 ring-2 focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
    >
      {{ t('app.skipToContent') }}
    </a>

    <UDashboardGroup unit="rem" storage="local" storage-key="horus-shell">
      <UDashboardSidebar
        id="app-sidebar"
        v-model:open="open"
        collapsible
        resizable
        :default-size="16"
        :min-size="13"
        :max-size="22"
        :collapsed-size="4"
        class="bg-muted"
        :ui="{ footer: 'border-t border-default', header: 'h-(--ui-header-height)' }"
      >
        <template #header="{ collapsed }">
          <TenantHeader :name="tenantName" :collapsed="collapsed" />
        </template>

        <template #default="{ collapsed }">
          <UDashboardSearchButton
            :collapsed="collapsed"
            :label="t('nav.search')"
            class="bg-transparent ring-default"
          />
          <UNavigationMenu
            :collapsed="collapsed"
            :items="navItems"
            orientation="vertical"
            tooltip
            popover
            :aria-label="t('nav.mainNavigation')"
            data-testid="main-nav"
            :ui="{ label: 'text-muted' }"
          />
        </template>

        <template #footer="{ collapsed }">
          <UserMenu
            v-if="auth.me.value"
            :name="auth.me.value.display_name"
            :email="auth.me.value.email"
            :collapsed="collapsed"
            @logout="auth.logout()"
          />
        </template>
      </UDashboardSidebar>

      <UDashboardSearch
        :groups="searchGroups"
        :placeholder="t('nav.searchPlaceholder')"
        :color-mode="false"
      />

      <slot />
    </UDashboardGroup>
  </div>
</template>
