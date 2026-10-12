<script setup lang="ts">
import type { Site } from '~~/types/api'

/**
 * Alta o edición de un nodo (`POST /sites`, `PATCH /sites/{id}` con `If-Match`). Solo se
 * envía lo que cambió (merge-patch). Ante `412` (otra sesión lo cambió) se carga la versión
 * actual en el formulario y se avisa para revisar y guardar de nuevo.
 */
const props = withDefaults(defineProps<{ site?: Site | null }>(), { site: null })
const emit = defineEmits<{ saved: [site: Site]; conflict: [site: Site] }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { $api } = useNuxtApp()
const { me } = useAuth()
const toast = useToast()

type Kind = NonNullable<Site['kind']>
const form = reactive({
  name: '',
  code: '',
  kind: 'node' as Kind,
  address: '',
  timezone: '',
  latitude: '',
  longitude: '',
})
const errors = ref<Record<string, string>>({})
const error = shallowRef<unknown>(null)
const conflict = ref(false)
const saving = ref(false)
/** Versión sobre la que se guarda (la del 412 si hubo conflicto). */
const base = ref<Site | null>(null)
const editing = computed(() => !!props.site)

function fill(site: Site | null) {
  base.value = site
  Object.assign(form, {
    name: site?.name ?? '',
    code: site?.code ?? '',
    kind: site?.kind ?? 'node',
    address: site?.address ?? '',
    timezone: site ? (site.timezone ?? '') : (me.value?.timezone ?? defaultZone()),
    latitude: site?.latitude != null ? String(site.latitude) : '',
    longitude: site?.longitude != null ? String(site.longitude) : '',
  })
}

function defaultZone() {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone ?? ''
  } catch {
    return ''
  }
}

watch(open, (o) => {
  if (!o) return
  fill(props.site)
  errors.value = {}
  error.value = null
  conflict.value = false
})

function validZone(zone: string) {
  try {
    new Intl.DateTimeFormat('es', { timeZone: zone })
    return true
  } catch {
    return false
  }
}

function values() {
  const e: Record<string, string> = {}
  const name = form.name.trim()
  if (!name) e.name = t('devices.site.nameRequired')
  const latitude = parseCoordinate(form.latitude, 90)
  const longitude = parseCoordinate(form.longitude, 180)
  if (latitude === undefined) e.latitude = t('devices.site.latitudeInvalid')
  if (longitude === undefined) e.longitude = t('devices.site.longitudeInvalid')
  const timezone = nullable(form.timezone)
  if (timezone && !validZone(timezone)) e.timezone = t('devices.site.timezoneInvalid')
  errors.value = e
  if (Object.keys(e).length) return null
  return {
    name,
    code: nullable(form.code),
    kind: form.kind,
    address: nullable(form.address),
    timezone,
    latitude: latitude ?? null,
    longitude: longitude ?? null,
  }
}

async function save() {
  const next = values()
  if (!next) return
  saving.value = true
  error.value = null
  try {
    let saved: Site
    if (base.value) {
      const patch = mergePatch<typeof next>(base.value, next)
      if (!Object.keys(patch).length) {
        open.value = false
        return
      }
      saved = await unwrap(
        $api.PATCH('/sites/{site_id}', {
          params: {
            path: { site_id: base.value.id },
            header: { 'If-Match': ifMatch(base.value.version) },
          },
          // `SiteInput` exige `name` también en el PATCH (devices.yaml): se envía siempre.
          body: { ...patch, name: next.name },
        }),
      )
      toast.add({ title: t('devices.site.updated'), icon: 'i-lucide-check', color: 'success' })
    } else {
      saved = await unwrap(
        $api.POST('/sites', {
          params: { header: { 'Idempotency-Key': idempotencyKey() } },
          body: next,
        }),
      )
      toast.add({
        title: t('devices.site.created', { name: saved.name }),
        icon: 'i-lucide-check',
        color: 'success',
      })
    }
    open.value = false
    emit('saved', saved)
  } catch (e) {
    if (e instanceof ApiError && e.status === 412 && e.problem.current) {
      const current = e.problem.current as unknown as Site
      fill(current)
      conflict.value = true
      emit('conflict', current)
    } else if (e instanceof ApiError && e.code === 'ALREADY_EXISTS') {
      errors.value = { code: t('devices.site.codeTaken') }
    } else if (e instanceof ApiError && e.code === 'VALIDATION_FAILED') {
      errors.value = fieldErrors(e.problem)
      if (errors.value._) error.value = e
    } else {
      error.value = e
    }
  } finally {
    saving.value = false
  }
}

const kindItems = computed(() =>
  (['node', 'region', 'datacenter', 'other'] as const).map((k) => ({
    label: t(`devices.site.kind.${k}`),
    value: k,
  })),
)
</script>

<template>
  <UModal
    v-model:open="open"
    :title="editing ? t('devices.site.editTitle') : t('devices.site.createTitle')"
    :description="editing ? t('devices.site.editDescription') : t('devices.site.createDescription')"
    data-testid="site-form-modal"
  >
    <template #body>
      <form id="site-form" class="flex flex-col gap-4" novalidate @submit.prevent="save">
        <UAlert
          v-if="conflict"
          color="warning"
          variant="subtle"
          icon="i-lucide-git-compare"
          :title="t('devices.conflictTitle')"
          :description="t('devices.site.conflictBody')"
          data-testid="site-conflict"
        />
        <UFormField :label="t('devices.site.name')" :error="errors.name" required>
          <UInput
            v-model="form.name"
            class="w-full"
            maxlength="120"
            autocomplete="off"
            :placeholder="t('devices.site.namePlaceholder')"
            autofocus
            data-testid="site-name"
          />
        </UFormField>
        <div class="grid gap-4 sm:grid-cols-2">
          <UFormField
            :label="t('devices.site.code')"
            :hint="t('common.optional')"
            :help="t('devices.site.codeHelp')"
            :error="errors.code"
          >
            <UInput
              v-model="form.code"
              class="w-full"
              maxlength="32"
              autocomplete="off"
              :ui="{ base: 'font-mono uppercase' }"
              data-testid="site-code"
            />
          </UFormField>
          <UFormField :label="t('devices.site.kindLabel')" :error="errors.kind">
            <USelect v-model="form.kind" :items="kindItems" class="w-full" />
          </UFormField>
        </div>
        <UFormField
          :label="t('devices.site.address')"
          :hint="t('common.optional')"
          :error="errors.address"
        >
          <UInput
            v-model="form.address"
            class="w-full"
            maxlength="300"
            autocomplete="off"
            data-testid="site-address"
          />
        </UFormField>
        <UFormField
          :label="t('devices.site.timezone')"
          :help="t('devices.site.timezoneHelp')"
          :error="errors.timezone"
        >
          <UInput
            v-model="form.timezone"
            class="w-full"
            autocomplete="off"
            placeholder="America/Bogota"
            data-testid="site-timezone"
          />
        </UFormField>
        <div class="grid gap-4 sm:grid-cols-2">
          <UFormField
            :label="t('devices.site.latitude')"
            :hint="t('common.optional')"
            :error="errors.latitude"
          >
            <UInput
              v-model="form.latitude"
              class="w-full"
              inputmode="decimal"
              autocomplete="off"
              placeholder="4.7110"
            />
          </UFormField>
          <UFormField
            :label="t('devices.site.longitude')"
            :hint="t('common.optional')"
            :error="errors.longitude"
          >
            <UInput
              v-model="form.longitude"
              class="w-full"
              inputmode="decimal"
              autocomplete="off"
              placeholder="-74.0721"
            />
          </UFormField>
        </div>
        <ErrorState v-if="error" :error="error" @retry="save" />
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
          form="site-form"
          :loading="saving"
          :icon="editing ? 'i-lucide-save' : 'i-lucide-plus'"
          :label="editing ? t('common.save') : t('devices.site.createSubmit')"
          data-testid="site-submit"
        />
      </div>
    </template>
  </UModal>
</template>
