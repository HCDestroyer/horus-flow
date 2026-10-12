<script setup lang="ts">
import type { CatalogPermission, Role } from '~~/types/api'

/**
 * Alta y edición de un rol propio del ISP (`roles.manage`).
 * - Clave derivada del nombre (editable solo al crear; `^[a-z][a-z0-9_]{1,62}$`).
 * - Permisos agrupados por área con "Seleccionar área"; "Copiar de un rol existente" los
 *   rellena a partir de otro rol.
 * - Anti-escalada: solo se pueden conceder permisos que el usuario tiene en todo el ISP; los
 *   demás salen desactivados con el motivo (el backend respondería 403 PERMISSION_DENIED).
 * - Edición con `If-Match`: si otra persona cambió el rol (412) se carga la versión vigente.
 * - `REAUTH_REQUIRED`: se confirma la identidad (useReauth) y se reintenta.
 */
const open = defineModel<boolean>('open', { required: true })
const props = withDefaults(
  defineProps<{
    /** Rol propio a editar; `null` para crear uno nuevo. */
    role: Role | null
    /** Al crear: rol del que copiar los permisos de partida ("Duplicar"). */
    template?: Role | null
    roles: Role[]
    permissions: CatalogPermission[]
    /** ¿Puede el usuario conceder este permiso? (lo tiene en todo el ISP). */
    grantable: (key: string) => boolean
  }>(),
  { template: null },
)
const emit = defineEmits<{ saved: [role: Role]; changed: [] }>()

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { withReauth } = useReauth()
const areaLabel = usePermissionAreaLabel()

const editing = computed(() => !!props.role)
const groups = computed(() => groupPermissions(props.permissions))

const form = reactive({
  name: '',
  key: '',
  description: '',
  permissions: [] as string[],
  copyFrom: '' as string,
})
const version = ref(0)
const keyTouched = ref(false)
const errors = ref<Record<string, string>>({})
const notice = ref<string | null>(null)
const omitted = ref(0)
const saving = ref(false)

function grantableKeys(role: Role) {
  return rolePermissionKeys(role, props.permissions)
}

function reset() {
  const r = props.role
  errors.value = {}
  notice.value = null
  omitted.value = 0
  keyTouched.value = !!r
  form.copyFrom = ''
  if (r) {
    form.name = r.name
    form.key = r.key
    form.description = r.description ?? ''
    form.permissions = rolePermissionKeys(r, props.permissions)
    version.value = r.version ?? 1
  } else {
    form.name = ''
    form.key = ''
    form.description = ''
    form.permissions = []
    version.value = 0
    if (props.template) {
      form.name = t('roles.form.copyName', { name: props.template.name })
      copyFrom(props.template.id)
    }
  }
}

watch(open, (v) => v && reset(), { immediate: true })

watch(
  () => form.name,
  (name) => {
    if (!editing.value && !keyTouched.value) form.key = deriveRoleKey(name)
  },
)

function onKeyInput() {
  keyTouched.value = true
}

const selected = computed(() => new Set(form.permissions))
/** Un permiso que el rol ya tiene se puede quitar aunque no se pueda conceder. */
const canToggle = (key: string) => props.grantable(key) || selected.value.has(key)

function toggle(key: string, on: boolean | 'indeterminate') {
  const set = new Set(form.permissions)
  if (on === true) set.add(key)
  else set.delete(key)
  form.permissions = props.permissions.map((p) => p.key).filter((k) => set.has(k))
}

function groupState(keys: string[]) {
  const grantable = keys.filter((k) => props.grantable(k))
  const all = grantable.length > 0 && grantable.every((k) => selected.value.has(k))
  return { grantable, all }
}

function toggleGroup(keys: string[]) {
  const { grantable, all } = groupState(keys)
  const set = new Set(form.permissions)
  for (const k of grantable) {
    if (all) set.delete(k)
    else set.add(k)
  }
  form.permissions = props.permissions.map((p) => p.key).filter((k) => set.has(k))
}

const copyItems = computed(() =>
  sortRoles(props.roles).map((r) => ({
    label: r.is_system ? `${r.name} · ${t('roles.system')}` : r.name,
    value: r.id,
  })),
)

function copyFrom(roleId: string) {
  const source = props.roles.find((r) => r.id === roleId)
  form.copyFrom = roleId
  if (!source) return
  const keys = grantableKeys(source)
  const allowed = keys.filter((k) => props.grantable(k))
  omitted.value = keys.length - allowed.length
  form.permissions = allowed
}

const hasBlocked = computed(() => props.permissions.some((p) => !props.grantable(p.key)))

function validate() {
  const e: Record<string, string> = {}
  const name = form.name.trim()
  if (!name) e.name = t('roles.form.nameRequired')
  else if (name.length > 120) e.name = t('roles.form.nameTooLong')
  if (!editing.value && !isValidRoleKey(form.key)) e.key = t('roles.form.keyInvalid')
  if (form.description.length > 500) e.description = t('roles.form.descriptionTooLong')
  if (!form.permissions.length) e.permissions = t('roles.form.permissionsRequired')
  errors.value = e
  return !Object.keys(e).length
}

function fieldErrors(e: ApiError) {
  return Object.fromEntries(
    (e.problem.errors ?? []).map((x) => [x.field.split(/[.[]/)[0] || '_', x.message]),
  )
}

async function save() {
  notice.value = null
  if (!validate()) return
  saving.value = true
  const description = form.description.trim() || null
  try {
    const role = await withReauth(() => {
      if (props.role) {
        return unwrap(
          $api.PATCH('/roles/{role_id}', {
            params: {
              path: { role_id: props.role.id },
              header: { 'If-Match': ifMatch(version.value) },
            },
            body: { name: form.name.trim(), description, permissions: [...form.permissions] },
          }),
        )
      }
      return unwrap(
        $api.POST('/roles', {
          body: {
            key: form.key,
            name: form.name.trim(),
            description,
            permissions: [...form.permissions],
          },
        }),
      )
    }, t('roles.form.reauthBody'))
    toast.add({
      title: editing.value
        ? t('roles.form.updated', { name: role.name })
        : t('roles.form.created', { name: role.name }),
      color: 'success',
      icon: 'i-lucide-check',
    })
    open.value = false
    emit('saved', role)
  } catch (e) {
    handleError(e)
  } finally {
    saving.value = false
  }
}

function handleError(e: unknown) {
  if (e instanceof ApiError && e.status === 412 && e.problem.current) {
    // Otra persona cambió el rol: se carga la versión vigente para revisarla.
    const current = e.problem.current as unknown as Role
    version.value = current.version ?? version.value
    form.name = current.name
    form.description = current.description ?? ''
    form.permissions = rolePermissionKeys(current, props.permissions)
    notice.value = t('roles.form.conflict')
    errors.value = {}
    emit('changed')
  } else if (isReauthRequired(e)) {
    errors.value = { _: t('roles.form.reauthCancelled') }
  } else if (e instanceof ApiError && e.code === 'PERMISSION_DENIED') {
    errors.value = { _: t('roles.form.escalation') }
  } else if (e instanceof ApiError && e.code === 'ROLE_READ_ONLY') {
    errors.value = { _: t('roles.readOnly') }
  } else if (e instanceof ApiError && e.status === 404) {
    errors.value = { _: t('roles.form.gone') }
    emit('changed')
  } else if (e instanceof ApiError && (e.status === 409 || e.status === 422)) {
    const fields = fieldErrors(e)
    errors.value = Object.keys(fields).length
      ? fields
      : { _: e.status === 409 ? t('roles.form.keyTaken') : t(describeError(e).messageKey) }
  } else {
    errors.value = { _: t(describeError(e).messageKey) }
  }
}

const title = computed(() =>
  props.role ? t('roles.form.editTitle', { name: props.role.name }) : t('roles.form.createTitle'),
)
</script>

<template>
  <USlideover
    v-model:open="open"
    :title="title"
    :description="editing ? t('roles.form.editDescription') : t('roles.form.createDescription')"
    :ui="{ content: 'sm:max-w-xl' }"
  >
    <template #body>
      <form
        id="role-form"
        class="flex flex-col gap-5"
        novalidate
        data-testid="role-form"
        @submit.prevent="save"
      >
        <UAlert
          v-if="notice"
          color="warning"
          variant="subtle"
          icon="i-lucide-refresh-cw"
          :title="notice"
          data-testid="role-conflict"
        />
        <UFormField :label="t('roles.form.name')" :error="errors.name" required>
          <UInput
            v-model="form.name"
            maxlength="120"
            autocomplete="off"
            class="w-full"
            data-testid="role-name"
          />
        </UFormField>
        <UFormField
          :label="t('roles.form.key')"
          :hint="editing ? t('roles.form.keyFixed') : undefined"
          :help="editing ? undefined : t('roles.form.keyHelp')"
          :error="errors.key"
          :required="!editing"
        >
          <UInput
            v-model="form.key"
            :disabled="editing"
            maxlength="63"
            autocomplete="off"
            spellcheck="false"
            autocapitalize="none"
            class="w-full"
            :ui="{ base: 'font-mono' }"
            data-testid="role-key"
            @input="onKeyInput"
          />
        </UFormField>
        <UFormField
          :label="t('roles.form.description')"
          :error="errors.description"
          :hint="t('roles.form.optional')"
        >
          <UTextarea
            v-model="form.description"
            :rows="2"
            maxlength="500"
            autoresize
            class="w-full"
            data-testid="role-description"
          />
        </UFormField>
        <UFormField
          v-if="!editing"
          :label="t('roles.form.copyFrom')"
          :help="t('roles.form.copyHelp')"
        >
          <USelect
            :model-value="form.copyFrom || undefined"
            :items="copyItems"
            :placeholder="t('roles.form.copyPlaceholder')"
            class="w-full"
            data-testid="role-copy"
            @update:model-value="(v: string) => copyFrom(v)"
          />
        </UFormField>
        <UAlert
          v-if="omitted"
          color="neutral"
          variant="subtle"
          icon="i-lucide-info"
          :title="t('roles.form.omitted', omitted)"
          data-testid="role-omitted"
        />

        <fieldset class="flex flex-col gap-3">
          <legend class="text-highlighted mb-1 text-sm font-medium">
            {{ t('roles.form.permissions') }}
            <span class="text-muted font-normal">
              · {{ t('roles.form.selectedCount', form.permissions.length) }}
            </span>
          </legend>
          <UAlert
            v-if="hasBlocked"
            color="info"
            variant="subtle"
            icon="i-lucide-shield-alert"
            :title="t('roles.form.escalationTitle')"
            :description="t('roles.form.escalationHelp')"
            data-testid="role-escalation-help"
          />
          <p v-if="errors.permissions" class="text-error text-sm" role="alert">
            {{ errors.permissions }}
          </p>
          <section
            v-for="group in groups"
            :key="group.id"
            class="ring-default flex flex-col gap-2 rounded-md p-3 ring-1"
            :data-area="group.id"
          >
            <div class="flex flex-wrap items-center justify-between gap-2">
              <h3 class="text-highlighted text-sm font-semibold">{{ areaLabel(group.id) }}</h3>
              <UButton
                size="xs"
                color="neutral"
                variant="outline"
                :disabled="!groupState(group.permissions.map((p) => p.key)).grantable.length"
                :label="
                  groupState(group.permissions.map((p) => p.key)).all
                    ? t('roles.form.clearGroup')
                    : t('roles.form.selectGroup')
                "
                :aria-label="
                  groupState(group.permissions.map((p) => p.key)).all
                    ? t('roles.form.clearGroupAria', { area: areaLabel(group.id) })
                    : t('roles.form.selectGroupAria', { area: areaLabel(group.id) })
                "
                data-testid="role-group-toggle"
                @click="toggleGroup(group.permissions.map((p) => p.key))"
              />
            </div>
            <UCheckbox
              v-for="perm in group.permissions"
              :key="perm.key"
              :model-value="selected.has(perm.key)"
              :disabled="!canToggle(perm.key)"
              :label="perm.description"
              :description="
                grantable(perm.key) ? perm.key : `${perm.key} · ${t('roles.form.notGrantable')}`
              "
              :data-permission="perm.key"
              @update:model-value="(v) => toggle(perm.key, v)"
            />
          </section>
        </fieldset>
        <UAlert
          v-if="errors._"
          color="error"
          variant="subtle"
          icon="i-lucide-circle-alert"
          :title="errors._"
          data-testid="role-error"
        />
      </form>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton
          color="neutral"
          variant="ghost"
          :label="t('common.cancel')"
          @click="open = false"
        />
        <UButton
          type="submit"
          form="role-form"
          :loading="saving"
          :icon="editing ? 'i-lucide-check' : 'i-lucide-plus'"
          :label="editing ? t('common.save') : t('roles.form.submitCreate')"
          data-testid="role-save"
        />
      </div>
    </template>
  </USlideover>
</template>
