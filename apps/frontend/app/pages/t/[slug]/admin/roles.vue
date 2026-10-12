<script setup lang="ts">
import type { DropdownMenuItem } from '@nuxt/ui'
import type { Role } from '~~/types/api'

/**
 * Roles y permisos del ISP (I1, `roles.read`).
 * - Roles de sistema (solo lectura, insignia "Sistema") y roles propios del ISP con su clave,
 *   descripción, permisos y cuántas asignaciones tienen.
 * - Matriz de permisos por área (prefijo de la clave) con las descripciones del catálogo
 *   (`GET /permissions`); en móvil, un rol cada vez con sus áreas plegables.
 * - Con `roles.manage`: "Nuevo rol" (o duplicar uno), editar (If-Match) y eliminar los propios
 *   (409 ROLE_IN_USE si alguien lo tiene asignado).
 * Ocultar por permiso, deshabilitar por estado (frontend.md §12).
 */
const { t } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const toast = useToast()
const can = useCan()
const { membership } = useTenant()
const slug = computed(() => String(route.params.slug))
useSectionGuard()(findSection('tenant', 'admin/roles'), slug.value)

const canManage = computed(() => can('roles.manage'))
const usersHref = computed(() => `/t/${slug.value}/admin/users`)

const {
  data: rolesData,
  error: rolesError,
  status: rolesStatus,
  refresh: refreshRoles,
} = useTenantQuery('roles', () => unwrap($api.GET('/roles')).then((r) => r.data))
const {
  data: catalog,
  error: catalogError,
  status: catalogStatus,
  refresh: refreshCatalog,
} = useTenantQuery('permissions', () => unwrap($api.GET('/permissions')))

// La lista de roles se comparte con Usuarios y roles: al volver, las asignaciones pueden
// haber cambiado.
onMounted(() => {
  if (rolesData.value) refreshRoles()
})

const roles = computed(() => sortRoles(rolesData.value ?? []))
const systemRoles = computed(() => roles.value.filter((r) => r.is_system))
const customRoles = computed(() => roles.value.filter((r) => !r.is_system))
/** Permisos de ISP del catálogo (los de plataforma no se asignan en roles del ISP). */
const permissions = computed(() =>
  (catalog.value?.permissions ?? []).filter((p) => p.family === 'tenant'),
)
const error = computed(() => rolesError.value ?? catalogError.value)
const loading = computed(
  () =>
    (rolesStatus.value === 'pending' && !rolesData.value) ||
    (catalogStatus.value === 'pending' && !catalog.value),
)
function retry() {
  refreshRoles()
  refreshCatalog()
}

const grantable = (key: string) => hasTenantWidePermission(membership.value, key)

const VIEWS = ['roles', 'matrix'] as const
const view = useQueryParam('view', 'roles', VIEWS)
const tabs = computed(() => [
  { label: t('roles.tabs.roles'), value: 'roles', icon: 'i-lucide-list' },
  { label: t('roles.tabs.matrix'), value: 'matrix', icon: 'i-lucide-grid-3x3' },
])

function permissionCount(role: Role) {
  return rolePermissionKeys(role, permissions.value).length
}

// --- Detalle (solo lectura) ------------------------------------------------------------------
const viewing = ref<Role | null>(null)
const detailOpen = computed({
  get: () => !!viewing.value,
  set: (v) => {
    if (!v) viewing.value = null
  },
})
const areaLabel = usePermissionAreaLabel()
const detailGroups = computed(() => {
  const r = viewing.value
  if (!r) return []
  return groupPermissions(permissions.value.filter((p) => roleHasPermission(r, p.key)))
})

// --- Alta y edición --------------------------------------------------------------------------
const editorOpen = ref(false)
const editingRole = ref<Role | null>(null)
const template = ref<Role | null>(null)

function openCreate(from: Role | null = null) {
  editingRole.value = null
  template.value = from
  editorOpen.value = true
}
function openEdit(role: Role) {
  editingRole.value = role
  template.value = null
  editorOpen.value = true
}
function onSaved() {
  refreshRoles()
}

function actionsFor(role: Role): DropdownMenuItem[][] {
  const main: DropdownMenuItem[] = [
    {
      label: t('roles.actions.view'),
      icon: 'i-lucide-eye',
      onSelect: () => (viewing.value = role),
    },
  ]
  if (canManage.value && !role.is_system) {
    main.push({
      label: t('roles.actions.edit'),
      icon: 'i-lucide-pencil',
      onSelect: () => openEdit(role),
    })
  }
  if (canManage.value) {
    main.push({
      label: t('roles.actions.duplicate'),
      icon: 'i-lucide-copy',
      onSelect: () => openCreate(role),
    })
  }
  const danger: DropdownMenuItem[] = []
  if (canManage.value && !role.is_system) {
    danger.push({
      label: t('roles.actions.delete'),
      icon: 'i-lucide-trash-2',
      color: 'error',
      onSelect: () => openDelete(role),
    })
  }
  return [main, danger].filter((g) => g.length)
}

// --- Eliminar --------------------------------------------------------------------------------
const deleting = ref<Role | null>(null)
const deleteError = ref<string | null>(null)
const deleteInUse = ref(false)
const deleteBusy = ref(false)
const deleteOpen = computed({
  get: () => !!deleting.value,
  set: (v) => {
    if (!v) deleting.value = null
  },
})
function openDelete(role: Role) {
  deleting.value = role
  deleteError.value = null
  deleteInUse.value = false
}
async function remove() {
  const role = deleting.value
  if (!role) return
  deleteBusy.value = true
  deleteError.value = null
  deleteInUse.value = false
  try {
    await unwrap(
      $api.DELETE('/roles/{role_id}', {
        params: { path: { role_id: role.id }, header: { 'If-Match': ifMatch(role.version ?? 1) } },
      }),
    )
    deleting.value = null
    toast.add({
      title: t('roles.delete.done', { name: role.name }),
      color: 'success',
      icon: 'i-lucide-check',
    })
    refreshRoles()
  } catch (e) {
    if (e instanceof ApiError && e.code === 'ROLE_IN_USE') {
      deleteInUse.value = true
      await refreshRoles()
      const fresh = rolesData.value?.find((r) => r.id === role.id)
      if (fresh) deleting.value = fresh
      deleteError.value = t('roles.delete.inUse', fresh?.assignments_count ?? 1)
    } else if (e instanceof ApiError && e.status === 412) {
      await refreshRoles()
      const fresh = rolesData.value?.find((r) => r.id === role.id)
      if (fresh) deleting.value = fresh
      deleteError.value = t('roles.delete.conflict')
    } else if (e instanceof ApiError && e.code === 'ROLE_READ_ONLY') {
      deleteError.value = t('roles.readOnly')
    } else if (e instanceof ApiError && e.status === 404) {
      deleting.value = null
      refreshRoles()
    } else {
      deleteError.value = t(describeError(e).messageKey)
    }
  } finally {
    deleteBusy.value = false
  }
}
</script>

<template>
  <AppPage :title="t('nav.items.roles')" panel-id="roles">
    <template #toolbar-left>
      <p class="text-muted text-sm">{{ t('roles.intro') }}</p>
    </template>
    <template v-if="canManage" #toolbar-right>
      <UButton
        icon="i-lucide-plus"
        :label="t('roles.create')"
        data-testid="role-create-open"
        @click="openCreate()"
      />
    </template>

    <UTabs v-model="view" :items="tabs" :content="false" class="w-full" variant="link" />

    <LoadingState v-if="loading" :rows="5" />
    <ErrorState v-else-if="error" :error="error" @retry="retry" />

    <template v-else-if="view === 'roles'">
      <section class="flex flex-col gap-3" :aria-labelledby="'custom-roles-title'">
        <div class="flex flex-wrap items-baseline justify-between gap-2">
          <h2 id="custom-roles-title" class="text-highlighted text-base font-semibold">
            {{ t('roles.customTitle') }}
          </h2>
          <p class="text-muted text-sm">{{ t('roles.customHint') }}</p>
        </div>
        <UCard v-if="!customRoles.length" class="py-4" data-testid="custom-roles-empty">
          <EmptyState
            icon="i-lucide-key-round"
            :title="t('roles.customEmpty')"
            :description="canManage ? t('roles.customEmptyHint') : undefined"
            :actions="
              canManage
                ? [{ label: t('roles.create'), icon: 'i-lucide-plus', onClick: () => openCreate() }]
                : []
            "
          />
        </UCard>
        <ul v-else class="grid gap-3 lg:grid-cols-2" data-testid="custom-roles">
          <RoleCard
            v-for="role in customRoles"
            :key="role.id"
            :role="role"
            :permission-count="permissionCount(role)"
            :actions="actionsFor(role)"
          />
        </ul>
      </section>

      <section class="flex flex-col gap-3" aria-labelledby="system-roles-title">
        <div class="flex flex-wrap items-baseline justify-between gap-2">
          <h2 id="system-roles-title" class="text-highlighted text-base font-semibold">
            {{ t('roles.systemTitle') }}
          </h2>
          <p class="text-muted text-sm">{{ t('roles.systemHint') }}</p>
        </div>
        <ul class="grid gap-3 lg:grid-cols-2" data-testid="system-roles">
          <RoleCard
            v-for="role in systemRoles"
            :key="role.id"
            :role="role"
            :permission-count="permissionCount(role)"
            :actions="actionsFor(role)"
          />
        </ul>
      </section>
    </template>

    <RolePermissionMatrix v-else :roles="roles" :permissions="permissions" />

    <!-- Detalle de un rol (solo lectura) -->
    <USlideover
      v-model:open="detailOpen"
      :title="viewing?.name ?? ''"
      :description="viewing?.description || t('roles.noDescription')"
    >
      <template #body>
        <div
          v-if="viewing"
          class="focus-visible:ring-primary flex flex-col gap-4 rounded-sm outline-none focus-visible:ring-2"
          tabindex="0"
          role="group"
          :aria-label="t('roles.detailLabel', { name: viewing.name })"
          data-testid="role-detail"
        >
          <div class="flex flex-wrap items-center gap-2 text-sm">
            <UBadge
              v-if="viewing.is_system"
              color="neutral"
              variant="subtle"
              icon="i-lucide-lock"
              :label="t('roles.system')"
            />
            <code class="text-muted">{{ viewing.key }}</code>
            <span class="text-muted">
              · {{ t('roles.assignments', viewing.assignments_count ?? 0) }}
            </span>
          </div>
          <UAlert
            v-if="viewing.is_system"
            color="neutral"
            variant="subtle"
            icon="i-lucide-lock"
            :title="t('roles.readOnly')"
            :description="canManage ? t('roles.readOnlyHint') : undefined"
            data-testid="role-read-only"
          />
          <section v-for="group in detailGroups" :key="group.id" class="flex flex-col gap-1.5">
            <h3 class="text-highlighted text-sm font-semibold">{{ areaLabel(group.id) }}</h3>
            <ul class="flex flex-col gap-1.5">
              <li
                v-for="perm in group.permissions"
                :key="perm.key"
                class="flex items-start gap-2 text-sm"
              >
                <UIcon
                  name="i-lucide-check"
                  class="text-primary mt-0.5 size-4 shrink-0"
                  aria-hidden="true"
                />
                <span class="flex min-w-0 flex-col">
                  <span>{{ perm.description }}</span>
                  <code class="text-muted text-xs break-all">{{ perm.key }}</code>
                </span>
              </li>
            </ul>
          </section>
        </div>
      </template>
      <template v-if="viewing && canManage" #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-copy"
            :label="t('roles.actions.duplicate')"
            @click="(openCreate(viewing!), (viewing = null))"
          />
          <UButton
            v-if="!viewing.is_system"
            icon="i-lucide-pencil"
            :label="t('roles.actions.edit')"
            @click="(openEdit(viewing!), (viewing = null))"
          />
        </div>
      </template>
    </USlideover>

    <RoleEditor
      v-if="canManage"
      v-model:open="editorOpen"
      :role="editingRole"
      :template="template"
      :roles="roles"
      :permissions="permissions"
      :grantable="grantable"
      @saved="onSaved"
      @changed="refreshRoles()"
    />

    <!-- Eliminar -->
    <UModal
      v-model:open="deleteOpen"
      :title="t('roles.delete.title', { name: deleting?.name ?? '' })"
      :description="t('roles.delete.body')"
    >
      <template #body>
        <div class="flex flex-col gap-3" data-testid="role-delete">
          <p class="text-sm" data-testid="role-delete-count">
            {{
              deleting?.assignments_count
                ? t('roles.delete.assigned', deleting.assignments_count)
                : t('roles.delete.unassigned')
            }}
          </p>
          <UAlert
            v-if="deleteError"
            color="error"
            variant="subtle"
            icon="i-lucide-circle-alert"
            :title="deleteError"
            :actions="
              deleteInUse
                ? [
                    {
                      label: t('roles.delete.goToUsers'),
                      icon: 'i-lucide-users',
                      color: 'error',
                      variant: 'outline',
                      to: usersHref,
                    },
                  ]
                : []
            "
            data-testid="role-delete-error"
          />
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            :label="t('common.cancel')"
            autofocus
            @click="deleting = null"
          />
          <UButton
            color="error"
            icon="i-lucide-trash-2"
            :loading="deleteBusy"
            :label="t('roles.delete.submit')"
            data-testid="role-delete-confirm"
            @click="remove"
          />
        </div>
      </template>
    </UModal>
  </AppPage>
</template>
