<script setup lang="ts">
import type { DropdownMenuItem, TableColumn } from '@nuxt/ui'
import type { InvitationIssued, Member, PasswordResetIssued, RoleAssignment } from '~~/types/api'

/**
 * Usuarios y roles del ISP (I1): quién tiene acceso y con qué rol.
 * - Lista con búsqueda (nombre o correo, solo en memoria: no entra en la URL) y filtro de estado
 *   (en la URL). Paginación por cursor.
 * - "Invitar" (`users.manage`): correo + rol y alcance. Si la cuenta ya existía queda añadida;
 *   si no, se muestra el enlace de un solo uso para compartirlo o copiarlo (y si hay correo
 *   configurado, también sale por correo).
 * - "Generar enlace" para un invitado (invalida los anteriores), "Quitar" con confirmación y
 *   "Cambiar roles" (`roles.assign`, `If-Match` + re-autenticación reciente: si el backend la
 *   pide, se confirma la identidad en un diálogo y el cambio se reintenta solo).
 * - "Editar" (nombre y correo, `PATCH /members/{id}`) y "Restablecer contraseña" (miembros
 *   activos: enlace de un solo uso, 30 min, con el mismo panel que las invitaciones), ambas
 *   con re-autenticación. Una cuenta que también pertenece a otro ISP o tiene roles de
 *   plataforma solo la edita la consola de plataforma (409 USER_SHARED).
 * Ocultar por permiso, deshabilitar por estado (frontend.md §12).
 */
type Row = { role_id: string; scope: string }

const { t } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const toast = useToast()
const can = useCan()
const { me } = useAuth()
const { withReauth } = useReauth()
const { membership } = useTenant()
const slug = computed(() => String(route.params.slug))
useSectionGuard()(findSection('tenant', 'admin/users'), slug.value)

const canManage = computed(() => can('users.manage'))
const canAssign = computed(() => can('roles.assign'))
const canSeeRoles = computed(() => can('roles.read'))
const rolesHref = computed(() => `/t/${slug.value}/admin/roles`)
const tenantName = computed(() => membership.value?.tenant_name ?? '')

const { data: rolesData } = useTenantQuery(
  'roles',
  () => unwrap($api.GET('/roles')).then((r) => r.data),
  { immediate: can('roles.read') },
)
const roles = computed(() => rolesData.value ?? [])
const { siteName } = useSites()

const STATUSES = ['all', 'active', 'invited', 'revoked'] as const
const status = useQueryParam('status', 'all', STATUSES)
const searchInput = ref('')
const search = ref('')

// --- Lista -----------------------------------------------------------------------------------
const rows = ref<Member[]>([])
const cursor = ref<string | null>(null)
const loading = ref(false)
const loadingMore = ref(false)
const loaded = ref(false)
const error = shallowRef<unknown>(null)

function fetchPage(next: string | null) {
  return unwrap(
    $api.GET('/members', {
      params: {
        query: {
          q: search.value.trim() || undefined,
          status: status.value === 'all' ? undefined : status.value,
          cursor: next ?? undefined,
          limit: 50,
        },
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
function clearAll() {
  searchInput.value = ''
  search.value = ''
  if (status.value === 'all') load()
  else status.value = 'all'
}
onMounted(load)
watch(status, load)
const filtered = computed(() => !!search.value || status.value !== 'all')

const STATUS_BADGE = {
  active: { color: 'success', icon: 'i-lucide-circle-check' },
  invited: { color: 'warning', icon: 'i-lucide-mail' },
  revoked: { color: 'neutral', icon: 'i-lucide-ban' },
} as const

function roleName(a: RoleAssignment) {
  return roles.value.find((r) => r.id === a.role_id)?.name ?? a.role_key ?? a.role_id
}
function scopeLabel(scope: string) {
  if (scope === 'tenant') return t('members.roles.scopeTenant')
  if (scope.startsWith('site:')) {
    return t('members.roles.scopeSite', { name: siteName(scope.slice(5)) })
  }
  return t('members.roles.scopeGroup')
}
const isSelf = (m: Member) => m.user_id === me.value?.id

function actionsFor(m: Member): DropdownMenuItem[][] {
  const main: DropdownMenuItem[] = []
  if (canManage.value && m.status !== 'revoked') {
    main.push({
      label: t('members.actions.edit'),
      icon: 'i-lucide-pencil',
      onSelect: () => openEdit(m),
    })
  }
  if (canManage.value && m.status === 'active') {
    main.push({
      label: t('members.actions.resetPassword'),
      icon: 'i-lucide-key-round',
      onSelect: () => openReset(m),
    })
  }
  if (canManage.value && m.status === 'invited') {
    main.push({
      label: t('members.actions.reissue'),
      icon: 'i-lucide-link',
      onSelect: () => reissue(m),
    })
  }
  if (canAssign.value && m.status !== 'revoked') {
    main.push({
      label: t('members.actions.roles'),
      icon: 'i-lucide-user-cog',
      onSelect: () => openRoles(m),
    })
  }
  const danger: DropdownMenuItem[] = []
  if (canManage.value && !isSelf(m)) {
    danger.push({
      label: t('members.actions.remove'),
      icon: 'i-lucide-user-minus',
      color: 'error',
      onSelect: () => (removing.value = m),
    })
  }
  return [main, danger].filter((g) => g.length)
}

const columns: TableColumn<Member>[] = [
  { id: 'member', header: () => t('members.col.member') },
  { id: 'status', header: () => t('members.col.status') },
  { id: 'roles', header: () => t('members.col.roles') },
  { id: 'mfa', header: () => t('members.col.mfa') },
  { id: 'actions', header: () => t('members.col.actions'), meta: { class: { th: 'sr-only' } } },
]

const statusOptions = computed(() =>
  STATUSES.map((s) => ({ label: t(`members.filters.${s}`), value: s })),
)

// --- Invitar y enlaces -----------------------------------------------------------------------
const inviteOpen = ref(false)
const invite = reactive({ email: '', rows: [] as Row[] })
const inviteErrors = ref<Record<string, string>>({})
const inviting = ref(false)
/** Enlace recién emitido (invitación o "Generar enlace"). */
const issued = ref<{ result: InvitationIssued; email: string } | null>(null)

function defaultRole() {
  return (roles.value.find((r) => r.key === 'noc') ?? roles.value[0])?.id ?? ''
}
function openInvite() {
  invite.email = ''
  invite.rows = [{ role_id: defaultRole(), scope: 'tenant' }]
  inviteErrors.value = {}
  issued.value = null
  inviteOpen.value = true
}
watch(roles, () => {
  if (inviteOpen.value && invite.rows[0] && !invite.rows[0].role_id) {
    invite.rows[0].role_id = defaultRole()
  }
})

function fieldErrors(e: unknown, fallbackField = '_') {
  if (e instanceof ApiError && e.problem.errors?.length) {
    return Object.fromEntries(
      e.problem.errors.map((x) => [x.field.split(/[.[]/)[0] ?? fallbackField, x.message]),
    )
  }
  return { _: t(describeError(e).messageKey) }
}

async function submitInvite() {
  inviteErrors.value = {}
  const email = invite.email.trim()
  if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) {
    inviteErrors.value = { email: t('members.invite.emailInvalid') }
    return
  }
  if (!invite.rows.every((r) => r.role_id)) {
    inviteErrors.value = { role_assignments: t('members.invite.roleRequired') }
    return
  }
  inviting.value = true
  try {
    const result = await unwrap(
      $api.POST('/members', {
        params: { header: { 'Idempotency-Key': idempotencyKey() } },
        body: { email, role_assignments: invite.rows.map((r) => ({ ...r })) },
      }),
    )
    if (result.outcome === 'added') {
      inviteOpen.value = false
      toast.add({
        title: t('members.invite.added', { email }),
        color: 'success',
        icon: 'i-lucide-user-check',
      })
    } else {
      issued.value = { result, email }
    }
    load()
  } catch (e) {
    inviteErrors.value = fieldErrors(e)
  } finally {
    inviting.value = false
  }
}

async function reissue(m: Member) {
  try {
    const result = await unwrap(
      $api.POST('/members/{user_id}/invitation', { params: { path: { user_id: m.user_id } } }),
    )
    issued.value = { result, email: m.email }
    inviteErrors.value = {}
    inviteOpen.value = true
  } catch (e) {
    const conflict = e instanceof ApiError && e.status === 409
    toast.add({
      title: conflict ? t('members.reissueConflict') : t(describeError(e).messageKey),
      color: 'error',
    })
    if (conflict) load()
  }
}

const inviteTitle = computed(() =>
  issued.value ? t('members.link.title', { email: issued.value.email }) : t('members.invite.title'),
)
const inviteDescription = computed(() =>
  issued.value
    ? t('members.link.description', { tenant: tenantName.value })
    : t('members.invite.description', { tenant: tenantName.value }),
)

// --- Quitar ----------------------------------------------------------------------------------
const removing = ref<Member | null>(null)
const removeBusy = ref(false)
const removeOpen = computed({
  get: () => !!removing.value,
  set: (v) => {
    if (!v) removing.value = null
  },
})
async function remove() {
  const m = removing.value
  if (!m) return
  removeBusy.value = true
  try {
    await unwrap($api.DELETE('/members/{user_id}', { params: { path: { user_id: m.user_id } } }))
    toast.add({
      title: t('members.remove.done', { name: m.display_name }),
      color: 'success',
      icon: 'i-lucide-check',
    })
  } catch (e) {
    toast.add({
      title:
        e instanceof ApiError && e.status === 409
          ? (e.problem.title ?? t(describeError(e).messageKey))
          : t(describeError(e).messageKey),
      color: 'error',
    })
  } finally {
    removeBusy.value = false
    removing.value = null
    load()
  }
}

// --- Editar (nombre y correo) ----------------------------------------------------------------
const EMAIL_RE = /^[^@\s]+@[^@\s]+\.[^@\s]+$/
const editTarget = ref<Member | null>(null)
const editForm = reactive({ name: '', email: '' })
const editFieldErrors = ref<Record<string, string>>({})
const editBusy = ref(false)
const editOpen = computed({
  get: () => !!editTarget.value,
  set: (v) => {
    if (!v) editTarget.value = null
  },
})
function openEdit(m: Member) {
  editTarget.value = m
  editForm.name = m.display_name
  editForm.email = m.email
  editFieldErrors.value = {}
}

/** Mensaje para los 409 de editar / restablecer (cuenta compartida, correo en uso…). */
function conflictMessage(e: unknown, fallback: string) {
  if (e instanceof ApiError && e.status === 409) {
    if (e.code === 'USER_SHARED') return t('members.edit.shared')
    if (e.code === 'ALREADY_EXISTS') return t('members.edit.exists')
    return e.problem.detail ?? e.problem.title ?? fallback
  }
  return fallback
}

async function saveEdit() {
  const m = editTarget.value
  if (!m) return
  editFieldErrors.value = {}
  const name = editForm.name.trim()
  const email = editForm.email.trim().toLowerCase()
  if (!name) {
    editFieldErrors.value = { name: t('members.edit.nameRequired') }
    return
  }
  if (!EMAIL_RE.test(email)) {
    editFieldErrors.value = { email: t('members.edit.emailInvalid') }
    return
  }
  const body: { display_name?: string; email?: string } = {}
  if (name !== m.display_name) body.display_name = name
  if (email !== m.email.toLowerCase()) body.email = email
  if (!Object.keys(body).length) {
    editTarget.value = null
    return
  }
  editBusy.value = true
  try {
    const updated = await withReauth(
      () =>
        unwrap(
          $api.PATCH('/members/{user_id}', { params: { path: { user_id: m.user_id } }, body }),
        ),
      t('members.edit.reauthBody'),
    )
    rows.value = rows.value.map((r) => (r.user_id === updated.user_id ? updated : r))
    editTarget.value = null
    toast.add({
      title: t('members.edit.saved', { name: updated.display_name }),
      color: 'success',
      icon: 'i-lucide-check',
    })
  } catch (e) {
    if (isReauthRequired(e)) editFieldErrors.value = { _: t('members.edit.reauthCancelled') }
    else if (e instanceof ApiError && e.status === 409) {
      const message = conflictMessage(e, t('errors.generic.title'))
      editFieldErrors.value = e.code === 'ALREADY_EXISTS' ? { email: message } : { _: message }
    } else if (e instanceof ApiError && e.status === 422) {
      const { display_name: name, ...rest } = fieldErrors(e)
      editFieldErrors.value = name ? { ...rest, name } : rest
    } else editFieldErrors.value = { _: t(describeError(e).messageKey) }
    if (e instanceof ApiError && e.status === 404) load()
  } finally {
    editBusy.value = false
  }
}

// --- Restablecer contraseña --------------------------------------------------------------------
const resetTarget = ref<Member | null>(null)
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
function openReset(m: Member) {
  resetTarget.value = m
  resetIssued.value = null
  resetError.value = null
}
async function issueReset() {
  const m = resetTarget.value
  if (!m) return
  resetBusy.value = true
  resetError.value = null
  try {
    resetIssued.value = await withReauth(
      () =>
        unwrap(
          $api.POST('/members/{user_id}/password-reset', {
            params: { path: { user_id: m.user_id } },
          }),
        ),
      t('members.reset.reauthBody'),
    )
  } catch (e) {
    if (isReauthRequired(e)) resetError.value = t('members.reset.reauthCancelled')
    else resetError.value = conflictMessage(e, t(describeError(e).messageKey))
    if (e instanceof ApiError && (e.status === 404 || e.status === 409)) load()
  } finally {
    resetBusy.value = false
  }
}

// --- Cambiar roles ---------------------------------------------------------------------------
const editing = ref<Member | null>(null)
const editRows = ref<Row[]>([])
const editVersion = ref(0)
const editError = ref<string | null>(null)
const editNotice = ref<string | null>(null)
const reauthNeeded = ref(false)
const saving = ref(false)
const rolesOpen = computed({
  get: () => !!editing.value,
  set: (v) => {
    if (!v) editing.value = null
  },
})
function openRoles(m: Member) {
  editing.value = m
  editRows.value = m.role_assignments.map((a) => ({ role_id: a.role_id, scope: a.scope }))
  editVersion.value = m.version
  editError.value = null
  editNotice.value = null
  reauthNeeded.value = false
}
async function saveRoles() {
  const m = editing.value
  if (!m) return
  editError.value = null
  editNotice.value = null
  reauthNeeded.value = false
  if (!editRows.value.length || !editRows.value.every((r) => r.role_id)) {
    editError.value = t('members.invite.roleRequired')
    return
  }
  saving.value = true
  try {
    const body = editRows.value.map((r) => ({ ...r }))
    await withReauth(
      () =>
        unwrap(
          $api.PUT('/members/{user_id}/role-assignments', {
            params: {
              path: { user_id: m.user_id },
              header: { 'If-Match': ifMatch(editVersion.value) },
            },
            body,
          }),
        ),
      t('members.roles.reauthBody'),
    )
    editing.value = null
    toast.add({
      title: t('members.roles.saved', { name: m.display_name }),
      color: 'success',
      icon: 'i-lucide-check',
    })
    load()
  } catch (e) {
    if (e instanceof ApiError && e.status === 412 && e.problem.current) {
      // Otro administrador lo cambió: se cargan los roles vigentes para revisarlos.
      const current = e.problem.current as unknown as Member
      editVersion.value = current.version
      editRows.value = current.role_assignments.map((a) => ({
        role_id: a.role_id,
        scope: a.scope,
      }))
      editNotice.value = t('members.roles.conflict')
    } else if (isReauthRequired(e)) {
      // Canceló la confirmación de identidad: se ofrece repetirla.
      reauthNeeded.value = true
    } else if (e instanceof ApiError && e.status === 422) {
      editError.value = Object.values(fieldErrors(e))[0] ?? t('members.invite.roleRequired')
    } else {
      editError.value = t(describeError(e).messageKey)
    }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <AppPage :title="t('nav.items.users')" panel-id="members">
    <template #toolbar-left>
      <p class="text-muted text-sm">{{ t('members.intro') }}</p>
    </template>
    <template v-if="canManage || canSeeRoles" #toolbar-right>
      <UButton
        v-if="canSeeRoles"
        :to="rolesHref"
        color="neutral"
        variant="outline"
        icon="i-lucide-key-round"
        :label="t('members.manageRoles')"
        data-testid="manage-roles"
      />
      <UButton
        v-if="canManage"
        icon="i-lucide-user-plus"
        :label="t('members.invite.open')"
        data-testid="invite-open"
        @click="openInvite"
      />
    </template>

    <form
      class="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-end"
      role="search"
      @submit.prevent="submitSearch"
    >
      <UFormField :label="t('members.search')" class="min-w-0 sm:w-80">
        <UInput
          v-model="searchInput"
          icon="i-lucide-search"
          :placeholder="t('members.searchPlaceholder')"
          autocomplete="off"
          class="w-full"
          data-testid="member-search"
        />
      </UFormField>
      <UFormField :label="t('members.col.status')">
        <USelect
          v-model="status"
          :items="statusOptions"
          class="w-full sm:w-44"
          data-testid="member-status"
        />
      </UFormField>
      <UButton
        type="submit"
        icon="i-lucide-search"
        color="neutral"
        variant="outline"
        :label="t('members.searchSubmit')"
        class="self-start sm:self-end"
      />
    </form>

    <LoadingState v-if="!loaded || (loading && !rows.length && !error)" :rows="5" />
    <ErrorState v-else-if="error" :error="error" @retry="load" />
    <UCard v-else-if="!rows.length" class="py-6" data-testid="members-empty">
      <EmptyState
        v-if="filtered"
        icon="i-lucide-search-x"
        :title="t('members.emptyFiltered')"
        :actions="[
          {
            label: t('common.clearFilters'),
            icon: 'i-lucide-filter-x',
            color: 'neutral',
            variant: 'outline',
            onClick: clearAll,
          },
        ]"
      />
      <EmptyState
        v-else
        icon="i-lucide-users"
        :title="t('members.empty')"
        :description="canManage ? t('members.emptyHint') : undefined"
      />
    </UCard>

    <template v-else>
      <p class="text-muted text-sm" aria-live="polite" data-testid="members-count">
        {{ t('members.showing', { n: formatNumber(rows.length) }) }}
      </p>

      <!-- Escritorio: tabla. -->
      <UCard class="hidden lg:block" :ui="{ body: 'p-0 sm:p-0' }">
        <UTable
          :data="rows"
          :columns="columns"
          :loading="loading"
          class="w-full"
          :ui="{ td: 'align-top py-2.5', th: 'text-xs whitespace-nowrap' }"
          data-testid="members-table"
        >
          <template #member-cell="{ row }">
            <div class="flex min-w-0 flex-col" :data-member="row.original.email">
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
              :label="t(`members.status.${row.original.status}`)"
              data-testid="member-status-badge"
            />
          </template>
          <template #roles-cell="{ row }">
            <ul class="flex flex-col gap-0.5">
              <li v-for="a in row.original.role_assignments" :key="a.role_id + a.scope">
                <span>{{ roleName(a) }}</span>
                <span class="text-muted text-xs"> · {{ scopeLabel(a.scope) }}</span>
              </li>
            </ul>
          </template>
          <template #mfa-cell="{ row }">
            <span
              class="flex items-center gap-1"
              :class="row.original.mfa_enabled ? 'text-default' : 'text-muted'"
            >
              <UIcon
                :name="row.original.mfa_enabled ? 'i-lucide-shield-check' : 'i-lucide-shield-off'"
                class="size-4"
                aria-hidden="true"
              />
              {{ row.original.mfa_enabled ? t('members.mfaOn') : t('members.mfaOff') }}
            </span>
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
                  :aria-label="t('members.actions.menu', { name: row.original.display_name })"
                  data-testid="member-actions"
                />
              </UDropdownMenu>
            </div>
          </template>
        </UTable>
      </UCard>

      <!-- Móvil y tableta: lista. -->
      <ul class="flex flex-col gap-2 lg:hidden" data-testid="members-list">
        <li
          v-for="m in rows"
          :key="m.user_id"
          class="bg-default ring-default flex items-start justify-between gap-2 rounded-md p-3 ring-1"
        >
          <div class="flex min-w-0 flex-col gap-1">
            <span class="text-highlighted truncate font-medium">{{ m.display_name }}</span>
            <span class="text-muted truncate text-xs">{{ m.email }}</span>
            <span class="flex flex-wrap items-center gap-2 text-sm">
              <UBadge
                :color="STATUS_BADGE[m.status].color"
                :icon="STATUS_BADGE[m.status].icon"
                variant="subtle"
                size="sm"
                :label="t(`members.status.${m.status}`)"
              />
              <span v-for="a in m.role_assignments" :key="a.role_id + a.scope" class="text-muted">
                {{ roleName(a) }}
              </span>
            </span>
          </div>
          <UDropdownMenu
            v-if="actionsFor(m).length"
            :items="actionsFor(m)"
            :content="{ align: 'end' }"
          >
            <UButton
              color="neutral"
              variant="ghost"
              icon="i-lucide-ellipsis"
              :aria-label="t('members.actions.menu', { name: m.display_name })"
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

    <!-- Invitar / enlace emitido -->
    <UModal v-model:open="inviteOpen" :title="inviteTitle" :description="inviteDescription">
      <template #body>
        <InvitationLinkPanel
          v-if="issued"
          :issued="issued.result"
          :email="issued.email"
          :tenant-name="tenantName"
        />
        <form
          v-else
          id="invite-form"
          class="flex flex-col gap-4"
          novalidate
          @submit.prevent="submitInvite"
        >
          <UFormField :label="t('members.invite.email')" :error="inviteErrors.email" required>
            <UInput
              v-model="invite.email"
              type="email"
              autocomplete="off"
              inputmode="email"
              spellcheck="false"
              autocapitalize="none"
              class="w-full"
              :placeholder="t('members.invite.emailPlaceholder')"
              data-testid="invite-email"
            />
          </UFormField>
          <RoleAssignmentsEditor
            v-if="roles.length"
            v-model="invite.rows"
            :roles="roles"
            single
            :error="inviteErrors.role_assignments"
          />
          <p v-else class="text-muted text-sm">{{ t('members.invite.noRoles') }}</p>
          <p class="text-muted text-xs">{{ t('members.invite.hint') }}</p>
          <UAlert v-if="inviteErrors._" color="error" variant="subtle" :title="inviteErrors._" />
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <template v-if="issued">
            <UButton
              color="neutral"
              variant="outline"
              :label="t('common.close')"
              data-testid="invitation-close"
              @click="inviteOpen = false"
            />
          </template>
          <template v-else>
            <UButton
              color="neutral"
              variant="ghost"
              :label="t('common.cancel')"
              @click="inviteOpen = false"
            />
            <UButton
              type="submit"
              form="invite-form"
              icon="i-lucide-send"
              :loading="inviting"
              :disabled="!roles.length"
              :label="t('members.invite.submit')"
              data-testid="invite-submit"
            />
          </template>
        </div>
      </template>
    </UModal>

    <!-- Editar nombre y correo -->
    <UModal
      v-model:open="editOpen"
      :title="t('members.edit.title', { name: editTarget?.display_name ?? '' })"
      :description="t('members.edit.description')"
    >
      <template #body>
        <form
          id="member-edit-form"
          class="flex flex-col gap-4"
          novalidate
          data-testid="member-edit-form"
          @submit.prevent="saveEdit"
        >
          <UFormField :label="t('members.edit.name')" :error="editFieldErrors.name" required>
            <UInput
              v-model="editForm.name"
              autocomplete="off"
              maxlength="120"
              class="w-full"
              data-testid="member-edit-name"
            />
          </UFormField>
          <UFormField :label="t('members.edit.email')" :error="editFieldErrors.email" required>
            <UInput
              v-model="editForm.email"
              type="email"
              autocomplete="off"
              inputmode="email"
              spellcheck="false"
              autocapitalize="none"
              class="w-full"
              data-testid="member-edit-email"
            />
          </UFormField>
          <UAlert
            v-if="editFieldErrors._"
            color="error"
            variant="subtle"
            icon="i-lucide-circle-alert"
            :title="editFieldErrors._"
            data-testid="member-edit-error"
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
            form="member-edit-form"
            :loading="editBusy"
            :label="t('common.save')"
            data-testid="member-edit-save"
          />
        </div>
      </template>
    </UModal>

    <!-- Restablecer contraseña -->
    <UModal
      v-model:open="resetOpen"
      :title="
        resetIssued
          ? t('members.reset.linkTitle', { name: resetTarget?.display_name ?? '' })
          : t('members.reset.title', { name: resetTarget?.display_name ?? '' })
      "
      :description="
        resetIssued
          ? t('members.reset.linkDescription', { email: resetTarget?.email ?? '' })
          : t('members.reset.body', {
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
          data-testid="member-reset-error"
        />
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <template v-if="resetIssued">
            <UButton
              color="neutral"
              variant="outline"
              :label="t('common.close')"
              data-testid="member-reset-close"
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
              :label="t('members.reset.submit')"
              data-testid="member-reset-confirm"
              @click="issueReset"
            />
          </template>
        </div>
      </template>
    </UModal>

    <!-- Quitar -->
    <UModal
      v-model:open="removeOpen"
      :title="t('members.remove.title', { name: removing?.display_name ?? '' })"
      :description="t('members.remove.body', { tenant: tenantName })"
    >
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            :label="t('common.cancel')"
            autofocus
            @click="removing = null"
          />
          <UButton
            color="error"
            icon="i-lucide-user-minus"
            :loading="removeBusy"
            :label="t('members.remove.submit')"
            data-testid="remove-confirm"
            @click="remove"
          />
        </div>
      </template>
    </UModal>

    <!-- Cambiar roles -->
    <USlideover
      v-model:open="rolesOpen"
      :title="t('members.roles.editTitle', { name: editing?.display_name ?? '' })"
      :description="t('members.roles.editDescription')"
    >
      <template #body>
        <form id="roles-form" class="flex flex-col gap-4" @submit.prevent="saveRoles">
          <UAlert
            v-if="editNotice"
            color="warning"
            variant="subtle"
            icon="i-lucide-refresh-cw"
            :title="editNotice"
            data-testid="roles-conflict"
          />
          <RoleAssignmentsEditor
            v-model="editRows"
            :roles="roles"
            :error="editError ?? undefined"
          />
          <p v-if="canSeeRoles" class="text-muted text-sm">
            {{ t('members.roles.customHint') }}
            <ULink :to="rolesHref" class="text-primary font-medium" data-testid="roles-manage-link">
              {{ t('members.manageRoles') }}
            </ULink>
          </p>
          <UAlert
            v-if="reauthNeeded"
            color="warning"
            variant="subtle"
            icon="i-lucide-lock-keyhole"
            :title="t('members.roles.reauthTitle')"
            :description="t('members.roles.reauthCancelled')"
            :actions="[
              {
                label: t('members.roles.reauthAction'),
                icon: 'i-lucide-lock-keyhole',
                color: 'warning',
                variant: 'outline',
                onClick: () => saveRoles(),
              },
            ]"
            data-testid="roles-reauth"
          />
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="ghost"
            :label="t('common.cancel')"
            @click="editing = null"
          />
          <UButton
            type="submit"
            form="roles-form"
            :loading="saving"
            :label="t('common.save')"
            data-testid="roles-save"
          />
        </div>
      </template>
    </USlideover>
  </AppPage>
</template>
