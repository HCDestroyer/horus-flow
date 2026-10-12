<script setup lang="ts">
import type { DropdownMenuItem } from '@nuxt/ui'
import type { Role } from '~~/types/api'

/** Tarjeta de un rol del ISP: nombre, clave, descripción, permisos y asignaciones. */
const props = defineProps<{
  role: Role
  permissionCount: number
  actions: DropdownMenuItem[][]
}>()

const { t } = useI18n()
</script>

<template>
  <li
    class="bg-default ring-default flex items-start justify-between gap-3 rounded-md p-4 ring-1"
    :data-role="props.role.key"
    data-testid="role-card"
  >
    <div class="flex min-w-0 flex-col gap-1">
      <div class="flex flex-wrap items-center gap-2">
        <span class="text-highlighted font-semibold">{{ role.name }}</span>
        <UBadge
          v-if="role.is_system"
          color="neutral"
          variant="subtle"
          size="sm"
          icon="i-lucide-lock"
          :label="t('roles.system')"
          data-testid="role-system-badge"
        />
        <UBadge v-else color="primary" variant="subtle" size="sm" :label="t('roles.custom')" />
        <UBadge
          v-if="role.requires_mfa"
          color="neutral"
          variant="outline"
          size="sm"
          icon="i-lucide-shield-check"
          :label="t('roles.mfa')"
        />
      </div>
      <code class="text-muted text-xs break-all">{{ role.key }}</code>
      <p v-if="role.description" class="text-default text-sm">{{ role.description }}</p>
      <p class="text-muted text-xs" data-testid="role-counts">
        {{ t('roles.permissionCount', permissionCount) }} ·
        {{ t('roles.assignments', role.assignments_count ?? 0) }}
      </p>
    </div>
    <UDropdownMenu :items="actions" :content="{ align: 'end' }">
      <UButton
        color="neutral"
        variant="ghost"
        icon="i-lucide-ellipsis"
        :aria-label="t('roles.actions.menu', { name: role.name })"
        data-testid="role-actions"
      />
    </UDropdownMenu>
  </li>
</template>
