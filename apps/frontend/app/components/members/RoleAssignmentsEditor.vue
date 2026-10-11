<script setup lang="ts">
import type { Role } from '~~/types/api'

/**
 * Asignaciones (rol, alcance) de un miembro: cada fila, un rol y dónde aplica (todo el ISP o
 * un nodo, `RoleAssignment.scope`). Con `single` solo una fila (invitación).
 */
type AssignmentRow = { role_id: string; scope: string }

const rows = defineModel<AssignmentRow[]>({ required: true })
const props = withDefaults(
  defineProps<{
    roles: Role[]
    single?: boolean
    error?: string
  }>(),
  { single: false, error: undefined },
)

const { t } = useI18n()
const { sites } = useSites()

const roleItems = computed(() =>
  props.roles.map((r) => ({
    label: r.requires_mfa ? `${r.name} · ${t('members.roles.mfa')}` : r.name,
    value: r.id,
  })),
)
const scopeItems = computed(() => [
  { label: t('members.roles.scopeTenant'), value: 'tenant' },
  ...(sites.value ?? []).map((s) => ({
    label: t('members.roles.scopeSite', { name: s.name }),
    value: `site:${s.id}`,
  })),
])

function add() {
  rows.value = [...rows.value, { role_id: props.roles[0]?.id ?? '', scope: 'tenant' }]
}
function remove(i: number) {
  rows.value = rows.value.filter((_, j) => j !== i)
}
</script>

<template>
  <fieldset class="flex flex-col gap-3" data-testid="role-editor">
    <legend class="text-highlighted mb-1 text-sm font-medium">
      {{ single ? t('members.roles.role') : t('members.roles.title') }}
    </legend>
    <div
      v-for="(row, i) in rows"
      :key="i"
      class="grid grid-cols-1 items-end gap-2 sm:grid-cols-[1fr_1fr_auto]"
      data-testid="role-row"
    >
      <UFormField :label="t('members.roles.role')" :ui="{ label: single ? 'sr-only' : '' }">
        <USelect
          v-model="row.role_id"
          :items="roleItems"
          :placeholder="t('members.roles.choose')"
          class="w-full"
          data-testid="role-select"
        />
      </UFormField>
      <UFormField :label="t('members.roles.scope')">
        <USelect
          v-model="row.scope"
          :items="scopeItems"
          class="w-full"
          data-testid="scope-select"
        />
      </UFormField>
      <UButton
        v-if="!single"
        color="neutral"
        variant="ghost"
        icon="i-lucide-trash-2"
        :aria-label="t('members.roles.removeRow')"
        :disabled="rows.length <= 1"
        @click="remove(i)"
      />
    </div>
    <div v-if="!single">
      <UButton
        size="sm"
        color="neutral"
        variant="outline"
        icon="i-lucide-plus"
        :label="t('members.roles.addRow')"
        data-testid="role-add"
        @click="add"
      />
    </div>
    <p v-if="error" class="text-error text-sm" role="alert">{{ error }}</p>
  </fieldset>
</template>
