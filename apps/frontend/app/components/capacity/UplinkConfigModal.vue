<script setup lang="ts">
import type { CapacityUplink, UplinkConfig, UplinkKind } from '~~/types/api'

/**
 * Configuración de un enlace de subida (`PUT /capacity/uplinks/{id}`): nombre visible,
 * proveedor, tipo, capacidad contratada (puede ser menor que la velocidad del puerto; vacía
 * = velocidad del puerto) y si forma parte de la salida internacional agregada. Quitar la
 * configuración vuelve a «solo detección». Horus no escribe nada en el router.
 */
const props = defineProps<{ uplink: CapacityUplink | null }>()
const emit = defineEmits<{ saved: [UplinkConfig | null] }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()

const form = reactive({
  label: '',
  provider: '',
  kind: 'international_transit' as UplinkKind,
  capacity: undefined as number | undefined,
  unit: 'Mbit/s' as CapacityUnit,
  egress: true,
  egressTouched: false,
  /** Fecha (local, aaaa-mm-dd) desde la que rige un cambio de capacidad; vacía = ahora. */
  validFrom: '',
})
const errors = ref<Record<string, string>>({})
const saving = ref(false)

watch(open, (o) => {
  if (!o || !props.uplink) return
  errors.value = {}
  const u = props.uplink
  const c = capacityToForm(u.contracted_bps)
  Object.assign(form, {
    label: u.configured ? u.label : u.interface_name,
    provider: u.provider ?? '',
    kind: u.kind ?? 'international_transit',
    capacity: c.value,
    unit: c.unit,
    egress: u.configured
      ? u.international_egress
      : (u.kind ?? 'international_transit') === 'international_transit',
    egressTouched: u.configured,
    validFrom: '',
  })
})
const today = () => new Date().toLocaleDateString('sv-SE')
const minValidFrom = () => new Date(Date.now() - 89 * 86_400_000).toLocaleDateString('sv-SE')
/** ¿Cambia la capacidad contratada? Solo entonces se pregunta desde cuándo rige. */
const capacityChanged = computed(() => {
  const u = props.uplink
  if (!u?.configured) return false
  return capacityFromForm(form.capacity, form.unit) !== (u.contracted_bps ?? null)
})
watch(
  () => form.kind,
  (k) => {
    if (!form.egressTouched) form.egress = k === 'international_transit'
  },
)

const kindItems = computed(() =>
  UPLINK_KINDS.map((k) => ({ label: t(`capacity.kind.${k}`), value: k })),
)
const unitItems = Object.keys(CAPACITY_UNITS).map((u) => ({ label: u, value: u }))
const portSpeed = computed(() =>
  props.uplink?.speed_bps
    ? joinUnit(formatBps(props.uplink.speed_bps))
    : t('capacity.config.unknownSpeed'),
)

async function save() {
  const u = props.uplink
  if (!u) return
  errors.value = {}
  if (!form.label.trim()) {
    errors.value = { label: t('capacity.config.labelRequired') }
    return
  }
  const contracted = capacityFromForm(form.capacity, form.unit)
  if (form.capacity != null && contracted == null) {
    errors.value = { contracted_bps: t('capacity.config.capacityInvalid') }
    return
  }
  saving.value = true
  try {
    const saved = await unwrap(
      $api.PUT('/capacity/uplinks/{interface_id}', {
        params: {
          path: { interface_id: u.interface_id },
          header: u.configured ? { 'If-Match': ifMatch(u.version) } : {},
        },
        body: {
          router_id: u.router_id,
          label: form.label.trim(),
          provider: form.provider.trim() || null,
          kind: form.kind,
          contracted_bps: contracted,
          international_egress: form.egress,
          capacity_valid_from:
            capacityChanged.value && form.validFrom && form.validFrom !== today()
              ? new Date(`${form.validFrom}T00:00:00`).toISOString()
              : null,
        },
      }),
    )
    open.value = false
    toast.add({ title: t('capacity.config.saved'), color: 'success', icon: 'i-lucide-check' })
    emit('saved', saved)
  } catch (e) {
    if (e instanceof ApiError && e.problem.errors?.length) {
      errors.value = Object.fromEntries(e.problem.errors.map((x) => [x.field, x.message]))
    } else {
      errors.value = { _: t(describeError(e).messageKey) }
    }
  } finally {
    saving.value = false
  }
}

async function reset() {
  const u = props.uplink
  if (!u?.configured) return
  saving.value = true
  try {
    await unwrap(
      $api.DELETE('/capacity/uplinks/{interface_id}', {
        params: { path: { interface_id: u.interface_id } },
      }),
    )
    open.value = false
    toast.add({ title: t('capacity.config.removed'), color: 'success', icon: 'i-lucide-check' })
    emit('saved', null)
  } catch (e) {
    errors.value = { _: t(describeError(e).messageKey) }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <USlideover
    v-model:open="open"
    :title="t('capacity.config.title', { name: uplink?.interface_name ?? '' })"
    :description="t('capacity.config.description', { router: uplink?.router_name ?? '' })"
    :ui="{ content: 'sm:max-w-lg' }"
  >
    <template #body>
      <form
        id="uplink-config-form"
        class="flex flex-col gap-4"
        data-testid="uplink-config"
        @submit.prevent="save"
      >
        <UFormField
          :label="t('capacity.config.label')"
          :help="t('capacity.config.labelHelp')"
          :error="errors.label"
          required
        >
          <UInput v-model="form.label" class="w-full" maxlength="120" data-testid="uplink-label" />
        </UFormField>
        <UFormField :label="t('capacity.config.provider')" :error="errors.provider">
          <UInput
            v-model="form.provider"
            class="w-full"
            maxlength="120"
            data-testid="uplink-provider"
          />
        </UFormField>
        <UFormField :label="t('capacity.config.kind')" :error="errors.kind">
          <USelect
            v-model="form.kind"
            :items="kindItems"
            class="w-full"
            data-testid="uplink-kind"
          />
        </UFormField>
        <UFormField
          :label="t('capacity.config.capacity')"
          :help="t('capacity.config.capacityHelp', { speed: portSpeed })"
          :error="errors.contracted_bps"
        >
          <div class="flex gap-2">
            <UInputNumber
              v-model="form.capacity"
              :min="1"
              :step="1"
              class="min-w-0 flex-1"
              :placeholder="t('capacity.config.capacityPlaceholder')"
              data-testid="uplink-capacity"
            />
            <USelect
              v-model="form.unit"
              :items="unitItems"
              class="w-32"
              :aria-label="t('capacity.config.unit')"
            />
          </div>
        </UFormField>
        <UFormField
          v-if="capacityChanged"
          :label="t('capacity.config.validFrom')"
          :help="t('capacity.config.validFromHelp')"
          :error="errors.capacity_valid_from"
        >
          <UInput
            v-model="form.validFrom"
            type="date"
            :min="minValidFrom()"
            :max="today()"
            class="w-full"
            data-testid="uplink-valid-from"
          />
        </UFormField>
        <UFormField :help="t('capacity.config.egressHelp')">
          <USwitch
            v-model="form.egress"
            :label="t('capacity.config.egress')"
            data-testid="uplink-egress"
            @update:model-value="form.egressTouched = true"
          />
        </UFormField>
        <UAlert
          v-if="errors._"
          color="error"
          variant="subtle"
          icon="i-lucide-circle-x"
          :title="errors._"
        />
      </form>
    </template>
    <template #footer>
      <div class="flex w-full flex-wrap justify-between gap-2">
        <UButton
          v-if="uplink?.configured"
          color="neutral"
          variant="ghost"
          icon="i-lucide-rotate-ccw"
          :label="t('capacity.config.reset')"
          :disabled="saving"
          data-testid="uplink-reset"
          @click="reset"
        />
        <span v-else />
        <div class="flex gap-2">
          <UButton
            color="neutral"
            variant="outline"
            :label="t('common.cancel')"
            @click="open = false"
          />
          <UButton
            type="submit"
            form="uplink-config-form"
            icon="i-lucide-check"
            :label="t('common.save')"
            :loading="saving"
            data-testid="uplink-save"
          />
        </div>
      </div>
    </template>
  </USlideover>
</template>
