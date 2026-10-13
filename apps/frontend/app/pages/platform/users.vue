<script setup lang="ts">
import type { DropdownMenuItem, TableColumn } from '@nuxt/ui'
import type {
  PasswordResetIssued,
  PlatformRoleKey,
  PlatformUser,
  PlatformUserStatus,
} from '~~/types/api'

/**
 * Usuarios de la instalación (consola de plataforma, `platform.users.read`): estado de la
 * cuenta, verificación en dos pasos, roles de plataforma y ISP de los que es miembro.
 * Búsqueda (nombre o correo, solo en memoria: no entra en la URL) y paginación por cursor.
 *
 * Con `platform.users.manage` (re-autenticación reciente, useReauth):
 * - "Bloquear" / "Activar": bloquear revoca sus sesiones al momento; nunca la propia cuenta.
 * - "Roles de plataforma": platform_admin / platform_operator / platform_auditor.
 * - "Editar" (nombre y correo), "Restablecer contraseña" (enlace de un solo uso, 30 min, con
 *   el mismo panel que las invitaciones) y "Eliminar cuenta" (revoca sesiones y membresías,
 *   conserva la auditoría; nunca la propia cuenta).
 * Siempre debe quedar un superadministrador activo (409 LAST_PLATFORM_ADMIN).
 */
const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { me } = useAuth()
const auth = useAuth()
const platformCan = usePlatformCan()
const { withReauth } = useReauth()
useSectionGuard()(findSection('platform', '/platform/users'))

const canManage = computed(() => platformCan('platform.users.manage'))
const isSelf = (u: PlatformUser) => u.id === me.value?.id

// --- Lista -----------------------------------------------------------------------------------
const rows = ref<PlatformUser[]>([])
const cursor = ref<string | null>(null)
const loading = ref(false)
const loadingMore = ref(false)
const loaded = ref(false)
const error = shallowRef<unknown>(null)
const searchInput = ref('')
const search = ref('')

function fetchPage(next: string | null) {
  return unwrap(
    $api.GET('/platform/users', {
      params: {
        query: { q: search.value.trim() || undefined, cursor: next ?? undefined, limit: 50 },
      },
    }),
  )
}

let seq = 0
async function load() {
  const mine = ++seq
  loading.value = true
  error.value = null
  try {
    const page = await fetchPage(null)
    if (mine !== seq) return
    rows.value = page.data
    cursor.value = page.page.next_cursor
  } catch (e) {
    if (mine !== seq) return
    error.value = e
    rows.value = []
  } finally {
    if (mine === seq) {
      loading.value = false
      loaded.value = true
    }
  }
}

async function loadMore() {
  if (!cursor.value) return
  loadingMore.value = true
  try {
    const page = await fetchPage(cursor.value)
    rows.value = [...rows.value, ...page.data]
    cursor.value = page.page.next_cursor
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  } finally {
    loadingMore.value = false
  }
}

function submitSearch() {
  search.value = searchInput.value
  load()
}
function clearSearch() {
  searchInput.value = ''
  search.value = ''
  load()
}
onMounted(load)

/** Sustituye una fila con la respuesta del backend (sin recargar toda la lista). */
function replace(user: PlatformUser) {
  rows.value = rows.value.map((u) => (u.id === user.id ? user : u))
}

const STATUS_BADGE: Record<
  PlatformUserStatus,
  { color: 'success' | 'warning' | 'error' | 'neutral'; icon: string }
> = {
  active: { color: 'success', icon: 'i-lucide-circle-check' },
  disabled: { color: 'error', icon: 'i-lucide-ban' },
  locked: { color: 'warning', icon: 'i-lucide-lock' },
  pending: { color: 'neutral', icon: 'i-lucide-hourglass' },
}

const PLATFORM_ROLES: PlatformRoleKey[] = [
  'platform_admin',
  'platform_operator',
  'platform_auditor',
]

const roleLabel = (key: string) =>
  (PLATFORM_ROLES as string[]).includes(key) ? t(`platformUsers.roles.${key}.name`) : key

function actionsFor(u: PlatformUser): DropdownMenuItem[][] {
  if (!canManage.value) return []
  const main: DropdownMenuItem[] = [
    {
      label: t('platformUsers.actions.edit'),
      icon: 'i-lucide-pencil',
      onSelect: () => openEdit(u),
    },
    {
      label: t('platformUsers.actions.roles'),
      icon: 'i-lucide-shield-user',
      onSelect: () => openRoles(u),
    },
    {
      label: t('platformUsers.actions.resetPassword'),
      icon: 'i-lucide-key-round',
      onSelect: () => openReset(u),
    },
  ]
  const status: DropdownMenuItem[] = []
  if (u.status === 'active' || u.status === 'pending') {
    // Nunca bloquearse a uno mismo.
    if (!isSelf(u)) {
      status.push({
        label: t('platformUsers.actions.block'),
        icon: 'i-lucide-ban',
        color: 'error',
        onSelect: () => openStatus(u, 'disabled'),
      })
    }
  } else {
    status.push({
      label: t('platformUsers.actions.activate'),
      icon: 'i-lucide-circle-check',
      onSelect: () => openStatus(u, 'active'),
    })
  }
  // Eliminar: nunca la propia cuenta (422 CANNOT_DELETE_SELF); se deja ver, deshabilitado.
  const danger: DropdownMenuItem[] = [
    {
      label: t('platformUsers.actions.delete'),
      icon: 'i-lucide-user-x',
      color: 'error',
      disabled: isSelf(u),
      onSelect: () => openDelete(u),
    },
  ]
  return [main, status, danger].filter((g) => g.length)
}

const columns: TableColumn<PlatformUser>[] = [
  { id: 'user', header: () => t('platformUsers.col.user') },
  { id: 'status', header: () => t('platformUsers.col.status') },
  { id: 'mfa', header: () => t('platformUsers.col.mfa') },
  { id: 'platform', header: () => t('platformUsers.col.platformRoles') },
  { id: 'memberships', header: () => t('platformUsers.col.memberships') },
  {
    id: 'actions',
    header: () => t('platformUsers.col.actions'),
    meta: { class: { th: 'sr-only' } },
  },
]

function errorMessage(e: unknown) {
  if (e instanceof ApiError && e.code === 'LAST_PLATFORM_ADMIN') return t('platformUsers.lastAdmin')
  if (isReauthRequired(e)) return t('platformUsers.reauthCancelled')
  if (e instanceof ApiError && e.status === 409) return e.problem.title ?? t('errors.generic.title')
  return t(describeError(e).messageKey)
}

// --- Bloquear / activar ----------------------------------------------------------------------
const statusTarget = ref<{ user: PlatformUser; to: 'active' | 'disabled' } | null>(null)
const statusError = ref<string | null>(null)
const statusBusy = ref(false)
const statusOpen = computed({
  get: () => !!statusTarget.value,
  set: (v) => {
    if (!v) statusTarget.value = null
  },
})
function openStatus(user: PlatformUser, to: 'active' | 'disabled') {
  statusTarget.value = { user, to }
  statusError.value = null
}
async function applyStatus() {
  const target = statusTarget.value
  if (!target) return
  statusBusy.value = true
  statusError.value = null
  try {
    const user = await withReauth(
      () =>
        unwrap(
          $api.PATCH('/platform/users/{user_id}', {
            params: { path: { user_id: target.user.id } },
            body: { status: target.to },
          }),
        ),
      t('platformUsers.reauthBody'),
    )
    replace(user)
    statusTarget.value = null
    toast.add({
      title:
        target.to === 'disabled'
          ? t('platformUsers.status.blocked', { name: user.display_name })
          : t('platformUsers.status.activated', { name: user.display_name }),
      color: 'success',
      icon: 'i-lucide-check',
    })
  } catch (e) {
    statusError.value = errorMessage(e)
    if (e instanceof ApiError && e.status === 404) load()
  } finally {
    statusBusy.value = false
  }
}

// --- Editar (nombre y correo) ----------------------------------------------------------------
const EMAIL_RE = /^[^@\s]+@[^@\s]+\.[^@\s]+$/
const editTarget = ref<PlatformUser | null>(null)
const editForm = reactive({ name: '', email: '' })
const editErrors = ref<Record<string, string>>({})
const editBusy = ref(false)
const editOpen = computed({
  get: () => !!editTarget.value,
  set: (v) => {
    if (!v) editTarget.value = null
  },
})
function openEdit(user: PlatformUser) {
  editTarget.value = user
  editForm.name = user.display_name
  editForm.email = user.email
  editErrors.value = {}
}
async function saveEdit() {
  const user = editTarget.value
  if (!user) return
  editErrors.value = {}
  const name = editForm.name.trim()
  const email = editForm.email.trim().toLowerCase()
  if (!name) {
    editErrors.value = { name: t('platformUsers.edit.nameRequired') }
    return
  }
  if (!EMAIL_RE.test(email)) {
    editErrors.value = { email: t('platformUsers.edit.emailInvalid') }
    return
  }
  const body: { display_name?: string; email?: string } = {}
  if (name !== user.display_name) body.display_name = name
  if (email !== user.email.toLowerCase()) body.email = email
  if (!Object.keys(body).length) {
    editTarget.value = null
    return
  }
  editBusy.value = true
  try {
    const updated = await withReauth(
      () =>
        unwrap(
          $api.PATCH('/platform/users/{user_id}', {
            params: { path: { user_id: user.id } },
            body,
          }),
        ),
      t('platformUsers.reauthBody'),
    )
    replace(updated)
    editTarget.value = null
    toast.add({
      title: t('platformUsers.edit.saved', { name: updated.display_name }),
      color: 'success',
      icon: 'i-lucide-check',
    })
    if (isSelf(updated)) await auth.reloadMe()
  } catch (e) {
    if (e instanceof ApiError && e.code === 'ALREADY_EXISTS') {
      editErrors.value = { email: t('platformUsers.edit.exists') }
    } else if (e instanceof ApiError && e.status === 422 && e.problem.errors?.length) {
      editErrors.value = Object.fromEntries(
        e.problem.errors.map((x) => [x.field === 'display_name' ? 'name' : x.field, x.message]),
      )
    } else editErrors.value = { _: errorMessage(e) }
    if (e instanceof ApiError && e.status === 404) load()
  } finally {
    editBusy.value = false
  }
}

// --- Restablecer contraseña --------------------------------------------------------------------
const resetTarget = ref<PlatformUser | null>(null)
const resetBusy = ref(false)
const resetError = ref<string | null>(null)
/** Enlace recién emitido: se muestra una sola vez. */
const resetIssued = ref<PasswordResetIssued | null>(null)
const resetOpen = computed({
  get: () => !!resetTarget.value,
  set: (v) => {
    if (!v) {
      resetTarget.value = null
      resetIssued.value = null
    }
  },
})
function openReset(user: PlatformUser) {
  resetTarget.value = user
  resetIssued.value = null
  resetError.value = null
}
async function issueReset() {
  const user = resetTarget.value
  if (!user) return
  resetBusy.value = true
  resetError.value = null
  try {
    resetIssued.value = await withReauth(
      () =>
        unwrap(
          $api.POST('/platform/users/{user_id}/password-reset', {
            params: { path: { user_id: user.id } },
          }),
        ),
      t('platformUsers.reauthBody'),
    )
  } catch (e) {
    resetError.value = errorMessage(e)
    if (e instanceof ApiError && e.status === 404) load()
  } finally {
    resetBusy.value = false
  }
}

// --- Eliminar cuenta -------------------------------------------------------------------------
const deleteTarget = ref<PlatformUser | null>(null)
const deleteBusy = ref(false)
const deleteError = ref<string | null>(null)
const deleteOpen = computed({
  get: () => !!deleteTarget.value,
  set: (v) => {
    if (!v) deleteTarget.value = null
  },
})
function openDelete(user: PlatformUser) {
  if (isSelf(user)) return
  deleteTarget.value = user
  deleteError.value = null
}
async function applyDelete() {
  const user = deleteTarget.value
  if (!user) return
  deleteBusy.value = true
  deleteError.value = null
  try {
    await withReauth(
      () =>
        unwrap(
          $api.DELETE('/platform/users/{user_id}', { params: { path: { user_id: user.id } } }),
        ),
      t('platformUsers.reauthBody'),
    )
    rows.value = rows.value.filter((u) => u.id !== user.id)
    deleteTarget.value = null
    toast.add({
      title: t('platformUsers.delete.done', { name: user.display_name }),
      color: 'success',
      icon: 'i-lucide-check',
    })
  } catch (e) {
    if (e instanceof ApiError && e.code === 'CANNOT_DELETE_SELF') {
      deleteError.value = t('platformUsers.delete.self')
    } else deleteError.value = errorMessage(e)
    if (e instanceof ApiError && e.status === 404) {
      deleteTarget.value = null
      load()
    }
  } finally {
    deleteBusy.value = false
  }
}

// --- Roles de plataforma ---------------------------------------------------------------------
const rolesTarget = ref<PlatformUser | null>(null)
const rolesDraft = ref<PlatformRoleKey[]>([])
const rolesError = ref<string | null>(null)
const rolesBusy = ref(false)
const rolesOpen = computed({
  get: () => !!rolesTarget.value,
  set: (v) => {
    if (!v) rolesTarget.value = null
  },
})
function openRoles(user: PlatformUser) {
  rolesTarget.value = user
  rolesDraft.value = PLATFORM_ROLES.filter((r) => user.platform_roles.includes(r))
  rolesError.value = null
}
function toggleRole(role: PlatformRoleKey, on: boolean | 'indeterminate') {
  const set = new Set(rolesDraft.value)
  if (on === true) set.add(role)
  else set.delete(role)
  rolesDraft.value = PLATFORM_ROLES.filter((r) => set.has(r))
}
const removingOwnAdmin = computed(
  () =>
    !!rolesTarget.value &&
    isSelf(rolesTarget.value) &&
    rolesTarget.value.platform_roles.includes('platform_admin') &&
    !rolesDraft.value.includes('platform_admin'),
)
async function saveRoles() {
  const user = rolesTarget.value
  if (!user) return
  rolesBusy.value = true
  rolesError.value = null
  try {
    const updated = await withReauth(
      () =>
        unwrap(
          $api.PUT('/platform/users/{user_id}/platform-roles', {
            params: { path: { user_id: user.id } },
            body: { platform_roles: [...rolesDraft.value] },
          }),
        ),
      t('platformUsers.reauthBody'),
    )
    replace(updated)
    rolesTarget.value = null
    toast.add({
      title: t('platformUsers.rolesSaved', { name: updated.display_name }),
      color: 'success',
      icon: 'i-lucide-check',
    })
    // Si eran los propios roles, los permisos de la consola cambian.
    if (isSelf(updated)) await auth.reloadMe()
  } catch (e) {
    rolesError.value = errorMessage(e)
  } finally {
    rolesBusy.value = false
  }
}
</script>

<template>
  <AppPage :title="t('nav.items.platformUsers')" panel-id="platform-users">
    <template #toolbar-left>
      <p class="text-muted text-sm">{{ t('platformUsers.intro') }}</p>
    </template>

    <form
      class="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-end"
      role="search"
      @submit.prevent="submitSearch"
    >
      <UFormField :label="t('platformUsers.search')" class="min-w-0 sm:w-80">
        <UInput
          v-model="searchInput"
          icon="i-lucide-search"
          :placeholder="t('platformUsers.searchPlaceholder')"
          autocomplete="off"
          class="w-full"
          data-testid="platform-user-search"
        />
      </UFormField>
      <UButton
        type="submit"
        icon="i-lucide-search"
        color="neutral"
        variant="outline"
        :label="t('platformUsers.searchSubmit')"
        class="self-start sm:self-end"
      />
    </form>

    <LoadingState v-if="!loaded || (loading && !rows.length && !error)" :rows="5" />
    <ErrorState v-else-if="error" :error="error" @retry="load" />
    <UCard v-else-if="!rows.length" class="py-6" data-testid="platform-users-empty">
      <EmptyState
        icon="i-lucide-search-x"
        :title="t('platformUsers.empty')"
        :actions="
          search
            ? [
                {
                  label: t('common.clearFilters'),
                  icon: 'i-lucide-filter-x',
                  color: 'neutral',
                  variant: 'outline',
                  onClick: clearSearch,
                },
              ]
            : []
        "
      />
    </UCard>

    <template v-else>
      <p class="text-muted text-sm" aria-live="polite" data-testid="platform-users-count">
        {{ t('platformUsers.showing', rows.length) }}
      </p>

      <!-- Escritorio: tabla. -->
      <UCard class="hidden lg:block" :ui="{ body: 'p-0 sm:p-0' }">
        <UTable
          :data="rows"
          :columns="columns"
          :loading="loading"
          class="w-full"
          :ui="{ td: 'align-top py-2.5', th: 'text-xs whitespace-nowrap' }"
          data-testid="platform-users-table"
        >
          <template #user-cell="{ row }">
            <div class="flex min-w-0 flex-col" :data-user="row.original.email">
              <span class="text-highlighted font-medium">
                {{ row.original.display_name }}
                <span v-if="isSelf(row.original)" class="text-muted text-xs font-normal">
                  ({{ t('members.you') }})
                </span>
              </span>
              <span class="text-muted text-xs">{{ row.original.email }}</span>
            </div>
          </template>
          <template #status-cell="{ row }">
            <UBadge
              :color="STATUS_BADGE[row.original.status].color"
              :icon="STATUS_BADGE[row.original.status].icon"
              variant="subtle"
              :label="t(`platformUsers.statusLabel.${row.original.status}`)"
              data-testid="platform-user-status"
            />
          </template>
          <template #mfa-cell="{ row }">
            <UBadge
              :color="row.original.mfa_enabled ? 'success' : 'neutral'"
              :icon="row.original.mfa_enabled ? 'i-lucide-shield-check' : 'i-lucide-shield-off'"
              variant="outline"
              :label="row.original.mfa_enabled ? t('members.mfaOn') : t('members.mfaOff')"
            />
          </template>
          <template #platform-cell="{ row }">
            <span v-if="!row.original.platform_roles.length" class="text-muted">—</span>
            <ul v-else class="flex flex-wrap gap-1" data-testid="platform-user-roles">
              <li v-for="r in row.original.platform_roles" :key="r">
                <UBadge color="primary" variant="subtle" :label="roleLabel(r)" />
              </li>
            </ul>
          </template>
          <template #memberships-cell="{ row }">
            <span v-if="!row.original.memberships.length" class="text-muted">—</span>
            <ul v-else class="flex flex-col gap-0.5">
              <li v-for="m in row.original.memberships" :key="m.tenant_id">
                <span class="text-default">{{ m.tenant_slug }}</span>
                <span class="text-muted text-xs"> · {{ m.role_keys.join(', ') }}</span>
              </li>
            </ul>
          </template>
          <template #actions-cell="{ row }">
            <div class="flex justify-end">
              <UDropdownMenu
                v-if="actionsFor(row.original).length"
                :items="actionsFor(row.original)"
                :content="{ align: 'end' }"
              >
                <UButton
                  color="neutral"
                  variant="ghost"
                  icon="i-lucide-ellipsis"
                  :aria-label="t('platformUsers.actions.menu', { name: row.original.display_name })"
                  data-testid="platform-user-actions"
                />
              </UDropdownMenu>
            </div>
          </template>
        </UTable>
      </UCard>

      <!-- Móvil y tableta: lista. -->
      <ul class="flex flex-col gap-2 lg:hidden" data-testid="platform-users-list">
        <li
          v-for="u in rows"
          :key="u.id"
          class="bg-default ring-default flex items-start justify-between gap-2 rounded-md p-3 ring-1"
        >
          <div class="flex min-w-0 flex-col gap-1">
            <span class="text-highlighted truncate font-medium">
              {{ u.display_name }}
              <span v-if="isSelf(u)" class="text-muted text-xs font-normal">
                ({{ t('members.you') }})
              </span>
            </span>
            <span class="text-muted truncate text-xs">{{ u.email }}</span>
            <span class="flex flex-wrap items-center gap-1.5 text-sm">
              <UBadge
                :color="STATUS_BADGE[u.status].color"
                :icon="STATUS_BADGE[u.status].icon"
                variant="subtle"
                size="sm"
                :label="t(`platformUsers.statusLabel.${u.status}`)"
              />
              <UBadge
                :color="u.mfa_enabled ? 'success' : 'neutral'"
                variant="outline"
                size="sm"
                :label="u.mfa_enabled ? t('platformUsers.mfaShort') : t('platformUsers.noMfaShort')"
              />
              <UBadge
                v-for="r in u.platform_roles"
                :key="r"
                color="primary"
                variant="subtle"
                size="sm"
                :label="roleLabel(r)"
              />
            </span>
            <span v-if="u.memberships.length" class="text-muted text-xs">
              {{
                u.memberships.map((m) => `${m.tenant_slug} (${m.role_keys.join(', ')})`).join(' · ')
              }}
            </span>
          </div>
          <UDropdownMenu
            v-if="actionsFor(u).length"
            :items="actionsFor(u)"
            :content="{ align: 'end' }"
          >
            <UButton
              color="neutral"
              variant="ghost"
              icon="i-lucide-ellipsis"
              :aria-label="t('platformUsers.actions.menu', { name: u.display_name })"
            />
          </UDropdownMenu>
        </li>
      </ul>

      <div v-if="cursor" class="flex justify-center">
        <UButton
          color="neutral"
          variant="outline"
          icon="i-lucide-chevrons-down"
          :loading="loadingMore"
          :label="t('common.loadMore')"
          @click="loadMore"
        />
      </div>
    </template>

    <!-- Bloquear / activar -->
    <UModal
      v-model:open="statusOpen"
      :title="
        statusTarget?.to === 'disabled'
          ? t('platformUsers.block.title', { name: statusTarget?.user.display_name ?? '' })
          : t('platformUsers.activate.title', { name: statusTarget?.user.display_name ?? '' })
      "
      :description="
        statusTarget?.to === 'disabled'
          ? t('platformUsers.block.body')
          : t('platformUsers.activate.body')
      "
    >
      <template #body>
        <UAlert
          v-if="statusError"
          color="error"
          variant="subtle"
          icon="i-lucide-circle-alert"
          :title="statusError"
          data-testid="platform-status-error"
        />
        <p v-else class="text-muted text-sm">{{ statusTarget?.user.email }}</p>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            :label="t('common.cancel')"
            autofocus
            @click="statusTarget = null"
          />
          <UButton
            :color="statusTarget?.to === 'disabled' ? 'error' : 'primary'"
            :icon="statusTarget?.to === 'disabled' ? 'i-lucide-ban' : 'i-lucide-circle-check'"
            :loading="statusBusy"
            :label="
              statusTarget?.to === 'disabled'
                ? t('platformUsers.block.submit')
                : t('platformUsers.activate.submit')
            "
            data-testid="platform-status-confirm"
            @click="applyStatus"
          />
        </div>
      </template>
    </UModal>

    <!-- Editar nombre y correo -->
    <UModal
      v-model:open="editOpen"
      :title="t('platformUsers.edit.title', { name: editTarget?.display_name ?? '' })"
      :description="t('platformUsers.edit.description')"
    >
      <template #body>
        <form
          id="platform-user-edit-form"
          class="flex flex-col gap-4"
          novalidate
          data-testid="platform-user-edit-form"
          @submit.prevent="saveEdit"
        >
          <UFormField :label="t('platformUsers.edit.name')" :error="editErrors.name" required>
            <UInput
              v-model="editForm.name"
              autocomplete="off"
              maxlength="120"
              class="w-full"
              data-testid="platform-user-edit-name"
            />
          </UFormField>
          <UFormField :label="t('platformUsers.edit.email')" :error="editErrors.email" required>
            <UInput
              v-model="editForm.email"
              type="email"
              autocomplete="off"
              inputmode="email"
              spellcheck="false"
              autocapitalize="none"
              class="w-full"
              data-testid="platform-user-edit-email"
            />
          </UFormField>
          <UAlert
            v-if="editErrors._"
            color="error"
            variant="subtle"
            icon="i-lucide-circle-alert"
            :title="editErrors._"
            data-testid="platform-user-edit-error"
          />
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="ghost"
            :label="t('common.cancel')"
            @click="editTarget = null"
          />
          <UButton
            type="submit"
            form="platform-user-edit-form"
            :loading="editBusy"
            :label="t('common.save')"
            data-testid="platform-user-edit-save"
          />
        </div>
      </template>
    </UModal>

    <!-- Restablecer contraseña -->
    <UModal
      v-model:open="resetOpen"
      :title="
        resetIssued
          ? t('platformUsers.reset.linkTitle', { name: resetTarget?.display_name ?? '' })
          : t('platformUsers.reset.title', { name: resetTarget?.display_name ?? '' })
      "
      :description="
        resetIssued
          ? t('platformUsers.reset.linkDescription', { email: resetTarget?.email ?? '' })
          : t('platformUsers.reset.body', {
              name: resetTarget?.display_name ?? '',
              email: resetTarget?.email ?? '',
            })
      "
    >
      <template v-if="resetIssued || resetError" #body>
        <PasswordResetLinkPanel
          v-if="resetIssued"
          :issued="resetIssued"
          :email="resetTarget?.email ?? ''"
        />
        <UAlert
          v-else-if="resetError"
          color="error"
          variant="subtle"
          icon="i-lucide-circle-alert"
          :title="resetError"
          data-testid="platform-reset-error"
        />
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <template v-if="resetIssued">
            <UButton
              color="neutral"
              variant="outline"
              :label="t('common.close')"
              data-testid="platform-reset-close"
              @click="resetOpen = false"
            />
          </template>
          <template v-else>
            <UButton
              color="neutral"
              variant="outline"
              :label="t('common.cancel')"
              autofocus
              @click="resetOpen = false"
            />
            <UButton
              icon="i-lucide-key-round"
              :loading="resetBusy"
              :label="t('platformUsers.reset.submit')"
              data-testid="platform-reset-confirm"
              @click="issueReset"
            />
          </template>
        </div>
      </template>
    </UModal>

    <!-- Eliminar cuenta -->
    <UModal
      v-model:open="deleteOpen"
      :title="t('platformUsers.delete.title', { name: deleteTarget?.display_name ?? '' })"
      :description="t('platformUsers.delete.body')"
    >
      <template #body>
        <UAlert
          v-if="deleteError"
          color="error"
          variant="subtle"
          icon="i-lucide-circle-alert"
          :title="deleteError"
          data-testid="platform-delete-error"
        />
        <p v-else class="text-muted text-sm">{{ deleteTarget?.email }}</p>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            :label="t('common.cancel')"
            autofocus
            @click="deleteTarget = null"
          />
          <UButton
            color="error"
            icon="i-lucide-user-x"
            :loading="deleteBusy"
            :label="t('platformUsers.delete.submit')"
            data-testid="platform-delete-confirm"
            @click="applyDelete"
          />
        </div>
      </template>
    </UModal>

    <!-- Roles de plataforma -->
    <USlideover
      v-model:open="rolesOpen"
      :title="t('platformUsers.rolesEditor.title', { name: rolesTarget?.display_name ?? '' })"
      :description="t('platformUsers.rolesEditor.description')"
    >
      <template #body>
        <form
          id="platform-roles-form"
          class="flex flex-col gap-4"
          data-testid="platform-roles-form"
          @submit.prevent="saveRoles"
        >
          <UAlert
            color="info"
            variant="subtle"
            icon="i-lucide-smartphone"
            :title="t('platformUsers.rolesEditor.totpTitle')"
            :description="
              rolesTarget && !rolesTarget.mfa_enabled
                ? t('platformUsers.rolesEditor.totpMissing')
                : t('platformUsers.rolesEditor.totpHelp')
            "
            data-testid="platform-roles-totp"
          />
          <fieldset class="flex flex-col gap-3">
            <legend class="text-highlighted mb-1 text-sm font-medium">
              {{ t('platformUsers.rolesEditor.legend') }}
            </legend>
            <UCheckbox
              v-for="role in PLATFORM_ROLES"
              :key="role"
              :model-value="rolesDraft.includes(role)"
              :label="t(`platformUsers.roles.${role}.name`)"
              :description="t(`platformUsers.roles.${role}.description`)"
              :data-platform-role="role"
              @update:model-value="(v) => toggleRole(role, v)"
            />
          </fieldset>
          <p v-if="!rolesDraft.length" class="text-muted text-sm">
            {{ t('platformUsers.rolesEditor.none') }}
          </p>
          <UAlert
            v-if="removingOwnAdmin"
            color="warning"
            variant="subtle"
            icon="i-lucide-triangle-alert"
            :title="t('platformUsers.rolesEditor.ownAdmin')"
          />
          <UAlert
            v-if="rolesError"
            color="error"
            variant="subtle"
            icon="i-lucide-circle-alert"
            :title="rolesError"
            data-testid="platform-roles-error"
          />
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="ghost"
            :label="t('common.cancel')"
            @click="rolesTarget = null"
          />
          <UButton
            type="submit"
            form="platform-roles-form"
            :loading="rolesBusy"
            :label="t('common.save')"
            data-testid="platform-roles-save"
          />
        </div>
      </template>
    </USlideover>
  </AppPage>
</template>
