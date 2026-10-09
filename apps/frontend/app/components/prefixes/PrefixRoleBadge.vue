<script setup lang="ts">
import type { ClientPrefixRole } from '~~/types/api'

/** Rol de un prefijo: Clientes / Infraestructura / Excluido (icono + texto, sin color de estado). */
defineProps<{ role: ClientPrefixRole }>()
const { t } = useI18n()
const ICON: Record<ClientPrefixRole, string> = {
  customers: 'i-lucide-users',
  infrastructure: 'i-lucide-server',
  excluded: 'i-lucide-ban',
}
</script>

<template>
  <span class="text-default inline-flex items-center gap-1 whitespace-nowrap">
    <UIcon :name="ICON[role]" class="text-muted size-4 shrink-0" aria-hidden="true" />
    {{ t(`prefixes.role.${role}`) }}
  </span>
</template>
