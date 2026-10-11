<script setup lang="ts">
import type { TenantCreated } from '~~/types/api'

/**
 * Alta de un ISP desde la consola de plataforma (`POST /platform/tenants`, `x-reauth`).
 * - Nombre; identificador (slug) derivado del nombre y editable; país (GT por defecto) y zona
 *   horaria (America/Guatemala); correo del primer administrador, por defecto el propio: así
 *   el superadministrador queda como administrador del ISP y puede invitar al resto.
 * - `Idempotency-Key` por envío (el reintento tras confirmar la identidad repite la misma).
 * - Errores: 409 → el identificador ya existe; 422 → en cada campo; `REAUTH_REQUIRED` →
 *   diálogo de confirmar identidad y reintento automático.
 * - Al crear: `outcome=added` → "Entrar al ISP" / "Invitar usuarios"; `outcome=invited` →
 *   el enlace de un solo uso para compartirlo o copiarlo (`InvitationLinkPanel`).
 */
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ created: [tenant: TenantCreated] }>()

const { t, locale } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { me } = useAuth()
const { withReauth } = useReauth()

const COUNTRIES = [
  'AR',
  'BO',
  'BR',
  'CL',
  'CO',
  'CR',
  'CU',
  'DO',
  'EC',
  'ES',
  'GT',
  'HN',
  'MX',
  'NI',
  'PA',
  'PE',
  'PR',
  'PY',
  'SV',
  'US',
  'UY',
  'VE',
]
/** Zonas habituales en Latinoamérica, primero; después, todas las IANA del navegador. */
const COMMON_ZONES = [
  'America/Guatemala',
  'America/El_Salvador',
  'America/Tegucigalpa',
  'America/Managua',
  'America/Costa_Rica',
  'America/Panama',
  'America/Mexico_City',
  'America/Bogota',
  'America/Lima',
  'America/Guayaquil',
  'America/Caracas',
  'America/La_Paz',
  'America/Santiago',
  'America/Argentina/Buenos_Aires',
  'America/Montevideo',
  'America/Asuncion',
  'America/Santo_Domingo',
  'America/Puerto_Rico',
  'America/Havana',
  'America/Sao_Paulo',
  'Europe/Madrid',
  'UTC',
]
const EMAIL_RE = /^[^@\s]+@[^@\s]+\.[^@\s]+$/

const countryItems = computed(() => {
  let names: Intl.DisplayNames | null = null
  try {
    names = new Intl.DisplayNames([locale.value], { type: 'region' })
  } catch {
    // Navegador sin Intl.DisplayNames: solo el código.
  }
  return COUNTRIES.map((code) => ({ value: code, label: names?.of(code) ?? code })).sort((a, b) =>
    a.label.localeCompare(b.label, locale.value),
  )
})

function allZones() {
  try {
    const intl = Intl as unknown as { supportedValuesOf?: (key: string) => string[] }
    return intl.supportedValuesOf?.('timeZone') ?? []
  } catch {
    return []
  }
}
const zoneItems = ref([...new Set([...COMMON_ZONES, ...allZones()])])
function addZone(zone: string) {
  const value = zone.trim()
  if (!value) return
  if (!zoneItems.value.includes(value)) zoneItems.value = [value, ...zoneItems.value]
  form.timezone = value
}

const empty = () => ({
  name: '',
  slug: '',
  country: 'GT',
  timezone: 'America/Guatemala',
  email: me.value?.email ?? '',
})
const form = reactive(empty())
const slugEdited = ref(false)
const errors = ref<Record<string, string>>({})
const saving = ref(false)
const created = ref<TenantCreated | null>(null)

watch(open, (value) => {
  if (!value) return
  Object.assign(form, empty())
  slugEdited.value = false
  errors.value = {}
  created.value = null
})
watch(
  () => form.name,
  (name) => {
    if (!slugEdited.value) form.slug = slugify(name)
  },
)
/** Al editar el identificador deja de seguir al nombre (si se vacía, vuelve a seguirlo). */
function onSlugInput(value: string | number | null | undefined) {
  slugEdited.value = !!value
}

const ownEmail = computed(
  () => !!me.value && form.email.trim().toLowerCase() === me.value.email.toLowerCase(),
)

function validate() {
  const out: Record<string, string> = {}
  if (!form.name.trim()) out.name = t('platform.create.nameRequired')
  if (!TENANT_SLUG_RE.test(form.slug)) out.slug = t('platform.create.slugInvalid')
  if (!form.country) out.country = t('platform.create.countryRequired')
  if (!form.timezone.trim()) out.timezone = t('platform.create.timezoneRequired')
  if (!EMAIL_RE.test(form.email.trim())) out.initial_admin_email = t('platform.create.emailInvalid')
  return out
}

async function submit() {
  errors.value = validate()
  if (Object.keys(errors.value).length) return
  saving.value = true
  // La misma clave en el reintento tras confirmar la identidad: es el mismo alta.
  const key = idempotencyKey()
  const body = {
    name: form.name.trim(),
    slug: form.slug,
    country: form.country,
    timezone: form.timezone.trim(),
    initial_admin_email: form.email.trim(),
  }
  try {
    const result = await withReauth(
      () =>
        unwrap(
          $api.POST('/platform/tenants', {
            params: { header: { 'Idempotency-Key': key } },
            body,
          }),
        ),
      t('platform.create.reauth'),
    )
    created.value = result
    emit('created', result)
    if (result.initial_admin_invitation.outcome === 'added') {
      toast.add({
        title: t('platform.create.added', { name: result.name }),
        color: 'success',
        icon: 'i-lucide-check',
      })
    }
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) {
      errors.value = { slug: t('platform.create.slugTaken') }
    } else if (e instanceof ApiError && e.status === 422 && e.problem.errors?.length) {
      errors.value = Object.fromEntries(
        e.problem.errors.map((x) => [x.field.split(/[.[]/)[0] ?? '_', x.message]),
      )
    } else if (isReauthRequired(e)) {
      errors.value = { _: t('platform.create.reauthCancelled') }
    } else {
      const info = describeError(e)
      errors.value = { _: t(info.messageKey, info.params ?? {}) }
    }
  } finally {
    saving.value = false
  }
}

const invited = computed(() =>
  created.value?.initial_admin_invitation.outcome === 'invited' ? created.value : null,
)
const title = computed(() => {
  if (!created.value) return t('platform.create.title')
  return invited.value
    ? t('platform.create.invitedTitle', { name: created.value.name })
    : t('platform.create.addedTitle', { name: created.value.name })
})
const description = computed(() => {
  if (!created.value) return t('platform.create.description')
  return invited.value
    ? t('platform.create.invitedDescription', { email: form.email.trim() })
    : t('platform.create.addedDescription')
})

async function go(path: string) {
  open.value = false
  await navigateTo(path)
}
</script>

<template>
  <UModal v-model:open="open" :title="title" :description="description">
    <template #body>
      <InvitationLinkPanel
        v-if="invited"
        :issued="invited.initial_admin_invitation"
        :email="form.email.trim()"
        :tenant-name="invited.name"
      />
      <div v-else-if="created" class="flex flex-col gap-3" data-testid="tenant-created">
        <UAlert
          color="success"
          variant="subtle"
          icon="i-lucide-circle-check"
          :title="t('platform.create.addedNext')"
        />
      </div>
      <form
        v-else
        id="tenant-form"
        class="flex flex-col gap-4"
        novalidate
        data-testid="tenant-form"
        @submit.prevent="submit"
      >
        <UFormField :label="t('platform.create.name')" :error="errors.name" required>
          <UInput
            v-model="form.name"
            maxlength="120"
            autocomplete="off"
            autofocus
            class="w-full"
            :placeholder="t('platform.create.namePlaceholder')"
            data-testid="tenant-name"
          />
        </UFormField>
        <UFormField
          :label="t('platform.create.slug')"
          :hint="t('platform.create.slugHint', { slug: form.slug || '…' })"
          :error="errors.slug"
          required
        >
          <UInput
            v-model="form.slug"
            maxlength="63"
            autocomplete="off"
            spellcheck="false"
            autocapitalize="none"
            class="w-full"
            :ui="{ base: 'font-mono' }"
            data-testid="tenant-slug"
            @update:model-value="onSlugInput"
          />
        </UFormField>
        <div class="grid gap-4 sm:grid-cols-2">
          <UFormField :label="t('platform.create.country')" :error="errors.country" required>
            <USelect
              v-model="form.country"
              :items="countryItems"
              class="w-full"
              data-testid="tenant-country"
            />
          </UFormField>
          <UFormField :label="t('platform.create.timezone')" :error="errors.timezone" required>
            <USelectMenu
              v-model="form.timezone"
              :items="zoneItems"
              create-item
              virtualize
              class="w-full"
              :search-input="{ placeholder: t('platform.create.timezoneSearch') }"
              data-testid="tenant-timezone"
              @create="addZone"
            />
          </UFormField>
        </div>
        <UFormField
          :label="t('platform.create.email')"
          :error="errors.initial_admin_email"
          :help="ownEmail ? t('platform.create.emailOwnHint') : t('platform.create.emailOtherHint')"
          required
        >
          <UInput
            v-model="form.email"
            type="email"
            inputmode="email"
            autocomplete="off"
            spellcheck="false"
            autocapitalize="none"
            class="w-full"
            data-testid="tenant-email"
          />
        </UFormField>
        <UAlert
          v-if="errors._"
          color="error"
          variant="subtle"
          icon="i-lucide-circle-alert"
          :title="errors._"
          data-testid="tenant-error"
        />
      </form>
    </template>
    <template #footer>
      <div class="flex w-full flex-wrap justify-end gap-2">
        <template v-if="invited">
          <UButton
            color="neutral"
            variant="outline"
            :label="t('common.close')"
            data-testid="tenant-close"
            @click="open = false"
          />
        </template>
        <template v-else-if="created">
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-user-plus"
            :label="t('platform.create.inviteUsers')"
            data-testid="tenant-invite-users"
            @click="go(`/t/${created.slug}/admin/users`)"
          />
          <UButton
            icon="i-lucide-log-in"
            :label="t('platform.create.enter')"
            data-testid="tenant-enter"
            @click="go(`/t/${created.slug}`)"
          />
        </template>
        <template v-else>
          <UButton
            color="neutral"
            variant="ghost"
            :label="t('common.cancel')"
            @click="open = false"
          />
          <UButton
            type="submit"
            form="tenant-form"
            icon="i-lucide-plus"
            :loading="saving"
            :label="t('platform.create.submit')"
            data-testid="tenant-submit"
          />
        </template>
      </div>
    </template>
  </UModal>
</template>
