<script setup lang="ts">
import type { CatalogPermission, Role } from '~~/types/api'

/**
 * Matriz de permisos: filas = permisos del catálogo agrupados por área, columnas = roles.
 * En escritorio, tabla con scroll horizontal propio; en móvil y tableta, un rol cada vez
 * (selector) con sus áreas plegables y cuántos permisos de cada una tiene.
 */
const props = defineProps<{
  roles: Role[]
  permissions: CatalogPermission[]
}>()

const { t } = useI18n()
const groups = computed(() => groupPermissions(props.permissions))
const areaLabel = usePermissionAreaLabel()

// --- Móvil: un rol cada vez ------------------------------------------------------------------
const selectedId = ref<string>('')
watch(
  () => props.roles,
  (roles) => {
    if (!roles.some((r) => r.id === selectedId.value)) selectedId.value = roles[0]?.id ?? ''
  },
  { immediate: true },
)
const selected = computed(() => props.roles.find((r) => r.id === selectedId.value))
const roleItems = computed(() =>
  props.roles.map((r) => ({
    label: r.is_system ? `${r.name} · ${t('roles.system')}` : r.name,
    value: r.id,
  })),
)
const accordion = computed(() =>
  groups.value.map((g) => {
    const granted = selected.value
      ? g.permissions.filter((p) => roleHasPermission(selected.value!, p.key)).length
      : 0
    return {
      label: areaLabel(g.id),
      value: g.id,
      granted,
      total: g.permissions.length,
      permissions: g.permissions,
    }
  }),
)
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="permission-matrix">
    <!-- Escritorio -->
    <div
      class="ring-default hidden overflow-x-auto rounded-md ring-1 lg:block"
      tabindex="0"
      role="region"
      :aria-label="t('roles.matrix.title')"
      data-testid="matrix-table"
    >
      <table class="w-full border-collapse text-sm">
        <caption class="sr-only">
          {{
            t('roles.matrix.caption')
          }}
        </caption>
        <thead>
          <tr class="bg-elevated/60">
            <th
              scope="col"
              class="bg-elevated sticky start-0 z-10 min-w-64 px-3 py-2 text-start text-xs font-medium"
            >
              {{ t('roles.matrix.permission') }}
            </th>
            <th
              v-for="role in roles"
              :key="role.id"
              scope="col"
              class="min-w-24 px-2 py-2 text-center align-bottom text-xs font-medium"
              :data-role-col="role.key"
            >
              <span class="text-highlighted block">{{ role.name }}</span>
              <span v-if="role.is_system" class="text-muted block font-normal">
                {{ t('roles.system') }}
              </span>
            </th>
          </tr>
        </thead>
        <tbody v-for="group in groups" :key="group.id" :data-area="group.id">
          <tr class="border-default border-t">
            <th
              scope="colgroup"
              :colspan="roles.length + 1"
              class="bg-muted text-highlighted sticky start-0 px-3 py-1.5 text-start text-xs font-semibold"
            >
              {{ areaLabel(group.id) }}
            </th>
          </tr>
          <tr
            v-for="perm in group.permissions"
            :key="perm.key"
            class="border-default hover:bg-elevated/40 border-t"
            :data-permission="perm.key"
          >
            <th scope="row" class="bg-default sticky start-0 px-3 py-1.5 text-start font-normal">
              <span class="text-default block">{{ perm.description }}</span>
              <code class="text-muted text-xs">{{ perm.key }}</code>
            </th>
            <td
              v-for="role in roles"
              :key="role.id"
              class="px-2 py-1.5 text-center"
              :data-role="role.key"
            >
              <template v-if="roleHasPermission(role, perm.key)">
                <UIcon name="i-lucide-check" class="text-primary size-4" aria-hidden="true" />
                <span class="sr-only">{{ t('common.yes') }}</span>
              </template>
              <template v-else>
                <span class="bg-accented inline-block size-1.5 rounded-full" aria-hidden="true" />
                <span class="sr-only">{{ t('common.no') }}</span>
              </template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Móvil y tableta -->
    <div class="flex flex-col gap-3 lg:hidden" data-testid="matrix-mobile">
      <UFormField :label="t('roles.matrix.pickRole')">
        <USelect
          v-model="selectedId"
          :items="roleItems"
          class="w-full"
          data-testid="matrix-role-select"
        />
      </UFormField>
      <p v-if="selected?.description" class="text-muted text-sm">{{ selected.description }}</p>
      <UAccordion
        v-if="selected"
        type="multiple"
        :items="accordion"
        :ui="{ item: 'border-default border-b last:border-b-0' }"
        class="bg-default ring-default rounded-md px-3 ring-1"
      >
        <template #trailing="{ item }">
          <span class="text-muted ms-auto me-2 text-xs tabular">
            {{ t('roles.matrix.areaCount', { n: item.granted, total: item.total }) }}
          </span>
        </template>
        <template #body="{ item }">
          <ul class="flex flex-col gap-2 pb-2">
            <li
              v-for="perm in item.permissions"
              :key="perm.key"
              class="flex items-start gap-2 text-sm"
              :data-permission="perm.key"
            >
              <UIcon
                :name="roleHasPermission(selected, perm.key) ? 'i-lucide-check' : 'i-lucide-minus'"
                class="mt-0.5 size-4 shrink-0"
                :class="roleHasPermission(selected, perm.key) ? 'text-primary' : 'text-dimmed'"
                aria-hidden="true"
              />
              <span class="sr-only">
                {{ roleHasPermission(selected, perm.key) ? t('common.yes') : t('common.no') }}:
              </span>
              <span class="flex min-w-0 flex-col">
                <span
                  :class="roleHasPermission(selected, perm.key) ? 'text-default' : 'text-muted'"
                >
                  {{ perm.description }}
                </span>
                <code class="text-muted text-xs break-all">{{ perm.key }}</code>
              </span>
            </li>
          </ul>
        </template>
      </UAccordion>
    </div>
  </div>
</template>
