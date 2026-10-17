<script setup lang="ts">
import type { Router } from '~~/types/api'

/**
 * Alta o edición de un router MikroTik (`POST /routers`, `PATCH /routers/{id}` con
 * `If-Match`). Reglas del contrato: un principal por nodo (`409 ROUTER_PRIMARY_EXISTS`, con
 * la opción de registrarlo como secundario), solo RouterOS 7.x y aviso si es < 7.12 (D15: se
 * registra, pero no se podrá generar el script). Tras el alta, el siguiente paso guiado es el
 * script de conexión WireGuard (MikroTik) o la configuración VRP en modo directo (Huawei, sin
 * túnel: docs/vendors/huawei.md). El fabricante solo se elige en el alta.
 */
const props = withDefaults(
  defineProps<{
    siteId?: string | null
    router?: Router | null
    /** El nodo ya tiene router principal (el nuevo se propone como secundario). */
    siteHasPrimary?: boolean
  }>(),
  { siteId: null, router: null, siteHasPrimary: false },
)
const emit = defineEmits<{ saved: [router: Router]; conflict: [router: Router] }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { $api } = useNuxtApp()
const route = useRoute()
const toast = useToast()
const slug = computed(() => String(route.params.slug))

type AdminState = Router['admin_state']
type Vendor = Router['vendor']
const form = reactive({
  name: '',
  displayName: '',
  isPrimary: true,
  vendor: 'mikrotik' as Vendor,
  model: '',
  version: '',
  managementAddress: '',
  adminState: 'active' as AdminState,
})
const huawei = computed(() => form.vendor === 'huawei')
const vendorItems = computed(() =>
  (['mikrotik', 'huawei'] as const).map((v) => ({ label: t(`devices.vendor.${v}`), value: v })),
)
const errors = ref<Record<string, string>>({})
const error = shallowRef<unknown>(null)
const primaryExists = ref(false)
const conflict = ref(false)
const saving = ref(false)
const base = ref<Router | null>(null)
/** Router recién creado: se muestran los siguientes pasos. */
const created = ref<Router | null>(null)
const editing = computed(() => !!props.router)

function fill(r: Router | null) {
  base.value = r
  Object.assign(form, {
    name: r?.name ?? '',
    displayName: r?.display_name ?? '',
    isPrimary: r ? r.is_primary !== false : !props.siteHasPrimary,
    vendor: r?.vendor ?? 'mikrotik',
    model: r?.model ?? '',
    version: r?.routeros_version ?? '',
    managementAddress: r?.management_address ?? '',
    adminState: r?.admin_state ?? 'active',
  })
}

watch(open, (o) => {
  if (!o) return
  fill(props.router)
  errors.value = {}
  error.value = null
  primaryExists.value = false
  conflict.value = false
  created.value = null
})

const versionCheck = computed(() =>
  huawei.value ? ('ok' as const) : checkRouterOsVersion(form.version),
)
const createdDirect = computed(() => created.value?.connection_mode === 'direct')

function validate() {
  const e: Record<string, string> = {}
  if (!form.name.trim()) e.name = t('devices.router.nameRequired')
  else if (/\s/.test(form.name.trim())) e.name = t('devices.router.nameSpaces')
  if (versionCheck.value === 'invalid') e.routeros_version = t('devices.router.versionInvalid')
  errors.value = e
  return Object.keys(e).length === 0
}

async function save() {
  if (!validate()) return
  saving.value = true
  error.value = null
  primaryExists.value = false
  const fields = {
    name: form.name.trim(),
    display_name: nullable(form.displayName),
    model: nullable(form.model),
    ...(huawei.value
      ? { management_address: nullable(form.managementAddress) }
      : { routeros_version: nullable(form.version) }),
  }
  try {
    if (base.value) {
      const patch = mergePatch(base.value, { ...fields, admin_state: form.adminState })
      if (!Object.keys(patch).length) {
        open.value = false
        return
      }
      const saved = await unwrap(
        $api.PATCH('/routers/{router_id}', {
          params: {
            path: { router_id: base.value.id },
            header: { 'If-Match': ifMatch(base.value.version) },
          },
          body: patch,
        }),
      )
      toast.add({ title: t('devices.router.updated'), icon: 'i-lucide-check', color: 'success' })
      open.value = false
      emit('saved', saved)
    } else {
      const saved = await unwrap(
        $api.POST('/routers', {
          params: { header: { 'Idempotency-Key': idempotencyKey() } },
          body: {
            ...fields,
            site_id: props.siteId!,
            is_primary: form.isPrimary,
            vendor: form.vendor,
          },
        }),
      )
      created.value = saved
      emit('saved', saved)
    }
  } catch (e) {
    if (e instanceof ApiError && e.status === 412 && e.problem.current) {
      const current = e.problem.current as unknown as Router
      fill(current)
      conflict.value = true
      emit('conflict', current)
    } else if (e instanceof ApiError && e.code === 'ROUTER_PRIMARY_EXISTS') {
      primaryExists.value = true
    } else if (e instanceof ApiError && e.code === 'ALREADY_EXISTS') {
      errors.value = { name: t('devices.router.nameTaken') }
    } else if (e instanceof ApiError && e.code === 'VALIDATION_FAILED') {
      const fe = fieldErrors(e.problem)
      if (fe.routeros_version) fe.routeros_version = t('devices.router.versionInvalid')
      if (fe.management_address) {
        fe.management_address = t('devices.router.managementAddressInvalid')
      }
      errors.value = fe
      if (fe._ || fe.site_id) error.value = e
    } else {
      error.value = e
    }
  } finally {
    saving.value = false
  }
}

function saveAsSecondary() {
  form.isPrimary = false
  save()
}

function continueToScript() {
  const id = created.value?.id
  open.value = false
  if (id) navigateTo(`/t/${slug.value}/routers/${id}/connection`)
}

const adminItems = computed(() =>
  (['active', 'maintenance', 'decommissioned'] as const).map((s) => ({
    label: t(`devices.router.admin.${s}`),
    value: s,
  })),
)
const title = computed(() =>
  created.value
    ? t('devices.router.createdTitle', { name: created.value.name })
    : editing.value
      ? t('devices.router.editTitle')
      : t('devices.router.createTitle'),
)
</script>

<template>
  <UModal
    v-model:open="open"
    :title="title"
    :description="
      created
        ? createdDirect
          ? t('devices.router.createdDescriptionDirect')
          : t('devices.router.createdDescription')
        : editing
          ? t('devices.router.editDescription')
          : t('devices.router.createDescription')
    "
    data-testid="router-form-modal"
  >
    <template #body>
      <div v-if="created" class="flex flex-col gap-4" data-testid="router-created">
        <UAlert
          v-if="created.routeros_version_supported === false"
          color="warning"
          variant="subtle"
          icon="i-lucide-triangle-alert"
          :title="t('devices.router.unsupportedTitle')"
          :description="t('devices.router.unsupportedBody')"
          data-testid="router-unsupported"
        />
        <SetupSteps current="script" />
      </div>
      <form v-else id="router-form" class="flex flex-col gap-4" novalidate @submit.prevent="save">
        <UAlert
          v-if="conflict"
          color="warning"
          variant="subtle"
          icon="i-lucide-git-compare"
          :title="t('devices.conflictTitle')"
          :description="t('devices.router.conflictBody')"
          data-testid="router-conflict"
        />
        <UAlert
          v-if="primaryExists"
          color="warning"
          variant="subtle"
          icon="i-lucide-crown"
          :title="t('devices.router.primaryExistsTitle')"
          :description="t('devices.router.primaryExistsBody')"
          data-testid="router-primary-exists"
        >
          <template #actions>
            <UButton
              size="sm"
              :label="t('devices.router.saveAsSecondary')"
              data-testid="router-save-secondary"
              @click="saveAsSecondary"
            />
          </template>
        </UAlert>
        <UFormField
          :label="t('devices.router.name')"
          :help="t('devices.router.nameHelp')"
          :error="errors.name"
          required
        >
          <UInput
            v-model="form.name"
            class="w-full"
            maxlength="120"
            autocomplete="off"
            spellcheck="false"
            :ui="{ base: 'font-mono' }"
            placeholder="rt-centro"
            autofocus
            data-testid="router-name"
          />
        </UFormField>
        <UFormField
          v-if="!editing"
          :label="t('devices.vendor.label')"
          :help="t('devices.vendor.help')"
        >
          <USelect
            v-model="form.vendor"
            :items="vendorItems"
            class="w-full"
            data-testid="router-vendor"
          />
        </UFormField>
        <UAlert
          v-if="huawei"
          color="warning"
          variant="subtle"
          icon="i-lucide-shield-alert"
          :title="t('onboarding.direct.warningTitle')"
          :description="t('devices.vendor.directHint')"
          data-testid="router-direct-warning"
        />
        <UFormField
          :label="t('devices.router.displayName')"
          :hint="t('common.optional')"
          :error="errors.display_name"
        >
          <UInput
            v-model="form.displayName"
            class="w-full"
            maxlength="120"
            autocomplete="off"
            data-testid="router-display-name"
          />
        </UFormField>
        <div class="grid gap-4 sm:grid-cols-2">
          <UFormField
            :label="t('devices.router.model')"
            :hint="t('common.optional')"
            :error="errors.model"
          >
            <UInput
              v-model="form.model"
              class="w-full"
              autocomplete="off"
              :placeholder="huawei ? t('devices.router.huaweiModelPlaceholder') : 'CCR2116-12G-4S+'"
              data-testid="router-model"
            />
          </UFormField>
          <UFormField
            v-if="huawei"
            :label="t('devices.router.managementAddress')"
            :hint="t('common.optional')"
            :help="t('devices.router.managementAddressHelp')"
            :error="errors.management_address"
          >
            <UInput
              v-model="form.managementAddress"
              class="w-full"
              autocomplete="off"
              spellcheck="false"
              placeholder="198.51.100.20"
              :ui="{ base: 'font-mono' }"
              data-testid="router-management-address"
            />
          </UFormField>
          <UFormField
            v-else
            :label="t('devices.router.version')"
            :help="versionCheck === 'unsupported' ? undefined : t('devices.router.versionHelp')"
            :error="errors.routeros_version"
          >
            <UInput
              v-model="form.version"
              class="w-full"
              autocomplete="off"
              inputmode="decimal"
              placeholder="7.16.2"
              :ui="{ base: 'font-mono' }"
              data-testid="router-version"
            />
          </UFormField>
        </div>
        <UAlert
          v-if="versionCheck === 'unsupported'"
          color="warning"
          variant="subtle"
          icon="i-lucide-triangle-alert"
          :title="t('devices.router.unsupportedTitle')"
          :description="t('devices.router.unsupportedBody')"
          data-testid="router-version-warning"
        />
        <UFormField
          v-if="!editing"
          :label="t('devices.router.primary')"
          :help="
            siteHasPrimary ? t('devices.router.primaryTaken') : t('devices.router.primaryHelp')
          "
        >
          <USwitch
            v-model="form.isPrimary"
            :label="form.isPrimary ? t('devices.router.primaryYes') : t('devices.router.primaryNo')"
            data-testid="router-primary"
          />
        </UFormField>
        <UFormField v-else :label="t('devices.router.adminState')" :error="errors.admin_state">
          <USelect
            v-model="form.adminState"
            :items="adminItems"
            class="w-full"
            data-testid="router-admin-state"
          />
        </UFormField>
        <ErrorState v-if="error" :error="error" @retry="save" />
      </form>
    </template>
    <template #footer>
      <div v-if="created" class="flex w-full flex-wrap justify-end gap-2">
        <UButton
          color="neutral"
          variant="ghost"
          :label="t('devices.router.later')"
          data-testid="router-later"
          @click="open = false"
        />
        <UButton
          icon="i-lucide-file-code-2"
          trailing-icon="i-lucide-arrow-right"
          :label="
            createdDirect
              ? t('devices.router.continueToConfig')
              : t('devices.router.continueToScript')
          "
          data-testid="router-continue"
          @click="continueToScript"
        />
      </div>
      <div v-else class="flex w-full justify-end gap-2">
        <UButton
          color="neutral"
          variant="ghost"
          :label="t('common.cancel')"
          @click="open = false"
        />
        <UButton
          type="submit"
          form="router-form"
          :loading="saving"
          :icon="editing ? 'i-lucide-save' : 'i-lucide-plus'"
          :label="editing ? t('common.save') : t('devices.router.createSubmit')"
          data-testid="router-submit"
        />
      </div>
    </template>
  </UModal>
</template>
