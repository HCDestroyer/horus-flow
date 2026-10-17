<script setup lang="ts">
import type { CapacitySettings, CapacitySettingsValues } from '~~/types/api'

/**
 * Ajustes de capacidad del ISP (`GET/PUT/DELETE /capacity/settings`): umbrales de
 * planificación y urgente con su antelación, saturación sostenida, pérdida y latencia de las
 * sondas, país para clasificar el tráfico local (por defecto el del ISP), ASN locales (IXP y
 * cachés on-net) y destinos de las sondas de respaldo desde Horus («ruta Horus → destino»).
 * Los porcentajes se editan como % y viajan como proporción (0,8).
 */
const emit = defineEmits<{ saved: [CapacitySettings] }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()

const current = ref<CapacitySettings | null>(null)
const loadError = ref<unknown>(null)
const errors = ref<Record<string, string>>({})
const saving = ref(false)
const form = reactive({
  country: '',
  planning: 80,
  urgent: 90,
  leadDays: 90,
  satWarn: 70,
  satCrit: 85,
  sustained: 15,
  lossWarn: 1,
  lossCrit: 5,
  latWarn: 80,
  latCrit: 200,
  asns: '',
  targets: [] as { label: string; address: string }[],
})

function fill(s: CapacitySettingsValues) {
  Object.assign(form, {
    country: s.country ?? '',
    planning: Math.round(s.planning_threshold * 100),
    urgent: Math.round(s.urgent_threshold * 100),
    leadDays: s.lead_days,
    satWarn: Math.round(s.saturation_warning * 100),
    satCrit: Math.round(s.saturation_critical * 100),
    sustained: s.sustained_minutes,
    lossWarn: s.loss_warning_pct,
    lossCrit: s.loss_critical_pct,
    latWarn: s.latency_warning_ms,
    latCrit: s.latency_critical_ms,
    asns: s.local_asns.join(', '),
    targets: s.probe_targets.map((p) => ({ ...p })),
  })
}

async function load() {
  loadError.value = null
  try {
    current.value = await unwrap($api.GET('/capacity/settings'))
    fill(current.value)
  } catch (e) {
    loadError.value = e
  }
}
watch(open, (o) => {
  if (!o) return
  errors.value = {}
  load()
})

const asnParse = computed(() => parseAsnList(form.asns))

async function save() {
  errors.value = {}
  if (asnParse.value.invalid.length) {
    errors.value = {
      local_asns: t('capacity.settings.asnInvalid', { list: asnParse.value.invalid.join(', ') }),
    }
    return
  }
  const bad = form.targets.findIndex((p) => !isIPv4(p.address) || !p.label.trim())
  if (bad >= 0) {
    errors.value = { [`probe_targets[${bad}].address`]: t('capacity.settings.targetInvalid') }
    return
  }
  saving.value = true
  try {
    const saved = await unwrap(
      $api.PUT('/capacity/settings', {
        params: {
          header: current.value?.customized ? { 'If-Match': ifMatch(current.value.version) } : {},
        },
        body: {
          country: form.country.trim() ? form.country.trim().toUpperCase() : null,
          planning_threshold: form.planning / 100,
          urgent_threshold: form.urgent / 100,
          lead_days: Number(form.leadDays),
          saturation_warning: form.satWarn / 100,
          saturation_critical: form.satCrit / 100,
          sustained_minutes: Number(form.sustained),
          loss_warning_pct: Number(form.lossWarn),
          loss_critical_pct: Number(form.lossCrit),
          latency_warning_ms: Number(form.latWarn),
          latency_critical_ms: Number(form.latCrit),
          local_asns: asnParse.value.asns,
          probe_targets: form.targets.map((p) => ({
            label: p.label.trim(),
            address: p.address.trim(),
          })),
        },
      }),
    )
    open.value = false
    toast.add({ title: t('capacity.settings.saved'), color: 'success', icon: 'i-lucide-check' })
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
  saving.value = true
  try {
    const saved = await unwrap($api.DELETE('/capacity/settings'))
    open.value = false
    toast.add({ title: t('capacity.settings.resetDone'), color: 'success', icon: 'i-lucide-check' })
    emit('saved', saved)
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
    :title="t('capacity.settings.title')"
    :description="t('capacity.settings.description')"
    :ui="{ content: 'sm:max-w-xl' }"
  >
    <template #body>
      <ErrorState v-if="loadError" :error="loadError" @retry="load()" />
      <form
        v-else
        id="capacity-settings-form"
        class="flex flex-col gap-5"
        data-testid="capacity-settings"
        @submit.prevent="save"
      >
        <fieldset class="flex flex-col gap-3">
          <legend class="text-highlighted mb-1 text-sm font-semibold">
            {{ t('capacity.settings.forecast') }}
          </legend>
          <div class="grid gap-3 sm:grid-cols-3">
            <UFormField :label="t('capacity.settings.planning')" :error="errors.planning_threshold">
              <UInputNumber
                v-model="form.planning"
                :min="30"
                :max="100"
                class="w-full"
                data-testid="settings-planning"
              />
            </UFormField>
            <UFormField :label="t('capacity.settings.urgent')" :error="errors.urgent_threshold">
              <UInputNumber v-model="form.urgent" :min="30" :max="100" class="w-full" />
            </UFormField>
            <UFormField :label="t('capacity.settings.leadDays')" :error="errors.lead_days">
              <UInputNumber v-model="form.leadDays" :min="7" :max="365" class="w-full" />
            </UFormField>
          </div>
        </fieldset>
        <fieldset class="flex flex-col gap-3">
          <legend class="text-highlighted mb-1 text-sm font-semibold">
            {{ t('capacity.settings.saturation') }}
          </legend>
          <div class="grid gap-3 sm:grid-cols-3">
            <UFormField :label="t('capacity.settings.satWarn')" :error="errors.saturation_warning">
              <UInputNumber v-model="form.satWarn" :min="30" :max="100" class="w-full" />
            </UFormField>
            <UFormField :label="t('capacity.settings.satCrit')" :error="errors.saturation_critical">
              <UInputNumber v-model="form.satCrit" :min="30" :max="100" class="w-full" />
            </UFormField>
            <UFormField :label="t('capacity.settings.sustained')" :error="errors.sustained_minutes">
              <UInputNumber v-model="form.sustained" :min="5" :max="240" class="w-full" />
            </UFormField>
          </div>
        </fieldset>
        <fieldset class="flex flex-col gap-3">
          <legend class="text-highlighted mb-1 text-sm font-semibold">
            {{ t('capacity.settings.quality') }}
          </legend>
          <div class="grid gap-3 sm:grid-cols-2">
            <UFormField :label="t('capacity.settings.lossWarn')" :error="errors.loss_warning_pct">
              <UInputNumber
                v-model="form.lossWarn"
                :min="0.1"
                :max="100"
                :step="0.1"
                class="w-full"
              />
            </UFormField>
            <UFormField :label="t('capacity.settings.lossCrit')" :error="errors.loss_critical_pct">
              <UInputNumber
                v-model="form.lossCrit"
                :min="0.1"
                :max="100"
                :step="0.1"
                class="w-full"
              />
            </UFormField>
            <UFormField :label="t('capacity.settings.latWarn')" :error="errors.latency_warning_ms">
              <UInputNumber v-model="form.latWarn" :min="1" :max="10000" class="w-full" />
            </UFormField>
            <UFormField :label="t('capacity.settings.latCrit')" :error="errors.latency_critical_ms">
              <UInputNumber v-model="form.latCrit" :min="1" :max="10000" class="w-full" />
            </UFormField>
          </div>
        </fieldset>
        <fieldset class="flex flex-col gap-3">
          <legend class="text-highlighted mb-1 text-sm font-semibold">
            {{ t('capacity.settings.traffic') }}
          </legend>
          <UFormField
            :label="t('capacity.settings.country')"
            :help="t('capacity.settings.countryHelp', { country: current?.tenant_country ?? '—' })"
            :error="errors.country"
          >
            <UInput
              v-model="form.country"
              class="w-24"
              maxlength="2"
              :ui="{ base: 'uppercase font-mono' }"
              data-testid="settings-country"
            />
          </UFormField>
          <UFormField
            :label="t('capacity.settings.asns')"
            :help="t('capacity.settings.asnsHelp')"
            :error="errors.local_asns"
          >
            <UTextarea
              v-model="form.asns"
              :rows="2"
              class="w-full"
              :ui="{ base: 'font-mono' }"
              data-testid="settings-asns"
            />
          </UFormField>
        </fieldset>
        <fieldset class="flex flex-col gap-3">
          <legend class="text-highlighted mb-1 text-sm font-semibold">
            {{ t('capacity.settings.targets') }}
          </legend>
          <p class="text-muted text-sm">{{ t('capacity.settings.targetsHelp') }}</p>
          <div v-for="(p, i) in form.targets" :key="i" class="flex flex-wrap items-start gap-2">
            <UFormField
              :label="t('capacity.settings.targetLabel')"
              class="min-w-40 flex-1"
              :error="errors[`probe_targets[${i}].label`]"
            >
              <UInput v-model="p.label" class="w-full" maxlength="60" />
            </UFormField>
            <UFormField
              :label="t('capacity.settings.targetAddress')"
              class="min-w-40 flex-1"
              :error="errors[`probe_targets[${i}].address`]"
            >
              <UInput
                v-model="p.address"
                class="w-full"
                :ui="{ base: 'font-mono' }"
                inputmode="decimal"
              />
            </UFormField>
            <UButton
              class="mt-6"
              color="neutral"
              variant="ghost"
              icon="i-lucide-trash-2"
              :aria-label="t('capacity.settings.removeTarget', { n: i + 1 })"
              @click="form.targets.splice(i, 1)"
            />
          </div>
          <div>
            <UButton
              v-if="form.targets.length < 10"
              color="neutral"
              variant="outline"
              icon="i-lucide-plus"
              :label="t('capacity.settings.addTarget')"
              @click="form.targets.push({ label: '', address: '' })"
            />
          </div>
        </fieldset>
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
          v-if="current?.customized"
          color="neutral"
          variant="ghost"
          icon="i-lucide-rotate-ccw"
          :label="t('capacity.settings.reset')"
          :disabled="saving"
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
            form="capacity-settings-form"
            icon="i-lucide-check"
            :label="t('common.save')"
            :loading="saving"
            data-testid="settings-save"
          />
        </div>
      </div>
    </template>
  </USlideover>
</template>
