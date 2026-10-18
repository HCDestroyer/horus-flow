<script setup lang="ts">
import type { Router } from '~~/types/api'

/**
 * Alta de un switch de monitoreo (`POST /routers` con `device_type=switch`): nodo, nombre y
 * sistema del switch (RouterOS en CRS, SwOS o Huawei serie S). SwOS solo tiene SNMP v1/v2c:
 * aviso claro de menor seguridad y de que debe estar en la red de gestión. El switch queda
 * siempre en modo directo y nunca es router principal. Al crearlo se abre su ficha con el
 * asistente de conexión (configuración para pegar y verificación SNMP en vivo).
 */
const props = withDefaults(defineProps<{ siteId?: string | null }>(), { siteId: null })
const emit = defineEmits<{ created: [router: Router] }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { sites } = useSites()

const form = reactive({
  site: props.siteId ?? '',
  name: '',
  platform: 'routeros' as SwitchPlatform,
})
const errors = ref<Record<string, string>>({})
const error = shallowRef<unknown>(null)
const saving = ref(false)

watch(open, (o) => {
  if (!o) return
  Object.assign(form, {
    site: props.siteId ?? sites.value?.[0]?.id ?? '',
    name: '',
    platform: 'routeros',
  })
  errors.value = {}
  error.value = null
})
watch(sites, (list) => {
  if (!form.site && list?.length) form.site = list[0]!.id
})

const siteItems = computed(() => (sites.value ?? []).map((s) => ({ label: s.name, value: s.id })))
const platformItems = computed(() =>
  SWITCH_PLATFORMS.map((p) => ({
    value: p,
    label: t(`switches.platform.${p}`),
    description: t(`switches.platform.${p}Help`),
  })),
)

async function save() {
  errors.value = {}
  if (!form.site) errors.value.site_id = t('switches.form.site')
  if (!form.name.trim()) errors.value.name = t('switches.form.name')
  if (Object.keys(errors.value).length) return
  saving.value = true
  error.value = null
  try {
    const created = await unwrap(
      $api.POST('/routers', {
        body: {
          site_id: form.site,
          name: form.name.trim(),
          device_type: 'switch',
          switch_platform: form.platform,
          is_primary: false,
        },
      }),
    )
    toast.add({ title: t('switches.form.created'), icon: 'i-lucide-check', color: 'success' })
    open.value = false
    emit('created', created)
  } catch (e) {
    if (e instanceof ApiError && e.code === 'VALIDATION_FAILED') {
      errors.value = fieldErrors(e.problem)
      return
    }
    if (e instanceof ApiError && e.code === 'ALREADY_EXISTS') {
      errors.value = { name: t('switches.form.nameTaken') }
      return
    }
    error.value = e
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <UModal
    v-model:open="open"
    :title="t('switches.form.title')"
    :description="t('switches.form.description')"
    data-testid="switch-form-modal"
  >
    <template #body>
      <form id="switch-form" class="flex flex-col gap-4" novalidate @submit.prevent="save">
        <UFormField :label="t('switches.form.site')" :error="errors.site_id" required>
          <USelect
            v-model="form.site"
            :items="siteItems"
            class="w-full"
            data-testid="switch-site"
          />
        </UFormField>
        <UFormField :label="t('switches.form.name')" :error="errors.name" required>
          <UInput
            v-model="form.name"
            class="w-full"
            maxlength="120"
            autocomplete="off"
            spellcheck="false"
            :ui="{ base: 'font-mono' }"
            :placeholder="t('switches.form.namePlaceholder')"
            autofocus
            data-testid="switch-name"
          />
        </UFormField>
        <UFormField :label="t('switches.form.platform')" :error="errors.switch_platform">
          <URadioGroup
            v-model="form.platform"
            :items="platformItems"
            variant="card"
            data-testid="switch-platform"
          />
        </UFormField>
        <UAlert
          v-if="isV2cOnly(form.platform)"
          color="warning"
          variant="subtle"
          icon="i-lucide-shield-alert"
          :title="t('switches.form.swosWarningTitle')"
          :description="t('switches.form.swosWarningBody')"
          data-testid="switch-swos-warning"
        />
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
          form="switch-form"
          icon="i-lucide-plus"
          :loading="saving"
          :label="t('switches.form.submit')"
          data-testid="switch-submit"
        />
      </div>
    </template>
  </UModal>
</template>
