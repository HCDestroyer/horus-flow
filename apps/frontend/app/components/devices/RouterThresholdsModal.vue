<script setup lang="ts">
import type { RouterThresholds, RouterThresholdValues } from '~~/types/api'

/**
 * Umbrales de los routers del ISP (GET/PUT/DELETE /routers/thresholds): CPU, memoria y
 * temperatura, aviso y crítico. Sin ajustes propios rigen los de la instalación. Horus no
 * escribe nada en los routers: solo cambia cuándo marca un router en aviso o crítico (y
 * cuándo avisan los canales de notificación). Editar exige `snmp.manage`.
 */
const open = defineModel<boolean>('open', { default: false })
const { t } = useI18n()
const { $api } = useNuxtApp()
const can = useCan()
const toast = useToast()
const manage = computed(() => can('snmp.manage'))

const current = ref<RouterThresholds | null>(null)
const loadError = ref<unknown>(null)
const loading = ref(false)
const saving = ref(false)
const errors = ref<Record<string, string>>({})
const form = reactive<RouterThresholdValues>({
  cpu_warning: 80,
  cpu_critical: 95,
  memory_warning: 85,
  memory_critical: 95,
  temperature_warning: 70,
  temperature_critical: 85,
})

const METRICS = [
  { key: 'cpu', unit: '%', min: 1, max: 100 },
  { key: 'memory', unit: '%', min: 1, max: 100 },
  { key: 'temperature', unit: '°C', min: 20, max: 150 },
] as const

function fill(v: RouterThresholds) {
  current.value = v
  for (const m of METRICS) {
    form[`${m.key}_warning`] = v[`${m.key}_warning`]
    form[`${m.key}_critical`] = v[`${m.key}_critical`]
  }
}

async function load() {
  loading.value = true
  loadError.value = null
  errors.value = {}
  try {
    fill(await unwrap($api.GET('/routers/thresholds')))
  } catch (e) {
    loadError.value = e
  } finally {
    loading.value = false
  }
}
watch(open, (o) => o && load())

async function run(fn: () => Promise<RouterThresholds>, done: string) {
  saving.value = true
  errors.value = {}
  try {
    fill(await fn())
    toast.add({ title: done, color: 'success', icon: 'i-lucide-check' })
    open.value = false
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

function save() {
  const c = current.value
  return run(
    () =>
      unwrap(
        $api.PUT('/routers/thresholds', {
          params: { header: c?.customized ? { 'If-Match': ifMatch(c.version) } : {} },
          body: { ...form },
        }),
      ),
    t('routerThresholds.saved'),
  )
}
function reset() {
  return run(() => unwrap($api.DELETE('/routers/thresholds')), t('routerThresholds.resetDone'))
}
</script>

<template>
  <UModal
    v-model:open="open"
    :title="t('routerThresholds.title')"
    :description="t('routerThresholds.description')"
  >
    <template #body>
      <LoadingState v-if="loading && !current" :rows="3" />
      <ErrorState v-else-if="loadError" :error="loadError" @retry="load" />
      <form
        v-else
        id="router-thresholds-form"
        class="flex flex-col gap-4"
        data-testid="router-thresholds"
        @submit.prevent="save"
      >
        <UBadge
          class="self-start"
          :color="current?.customized ? 'primary' : 'neutral'"
          variant="subtle"
          :label="
            current?.customized ? t('routerThresholds.custom') : t('routerThresholds.defaults')
          "
          data-testid="router-thresholds-source"
        />
        <fieldset v-for="m in METRICS" :key="m.key" class="flex flex-col gap-2">
          <legend class="text-highlighted mb-1 text-sm font-medium">
            {{ t(`routerThresholds.metric.${m.key}`) }}
            <span class="text-dimmed text-xs font-normal">
              ·
              {{
                t('routerThresholds.installation', {
                  w: current?.defaults[`${m.key}_warning`] ?? '—',
                  c: current?.defaults[`${m.key}_critical`] ?? '—',
                  u: m.unit,
                })
              }}
            </span>
          </legend>
          <div class="grid grid-cols-2 gap-3">
            <UFormField
              :label="t('routerThresholds.warning', { u: m.unit })"
              :error="errors[`${m.key}_warning`]"
            >
              <UInputNumber
                v-model="form[`${m.key}_warning`]"
                :min="m.min"
                :max="m.max"
                :disabled="!manage"
                class="w-full"
                :data-testid="`threshold-${m.key}-warning`"
              />
            </UFormField>
            <UFormField
              :label="t('routerThresholds.critical', { u: m.unit })"
              :error="errors[`${m.key}_critical`]"
            >
              <UInputNumber
                v-model="form[`${m.key}_critical`]"
                :min="m.min"
                :max="m.max"
                :disabled="!manage"
                class="w-full"
                :data-testid="`threshold-${m.key}-critical`"
              />
            </UFormField>
          </div>
        </fieldset>
        <p class="text-muted text-xs">
          {{ t('routerThresholds.sustained', { n: current?.sustained_cycles ?? 3 }) }}
        </p>
        <UAlert v-if="errors._" color="error" variant="subtle" :title="errors._" />
      </form>
    </template>
    <template #footer>
      <div class="flex w-full flex-wrap justify-between gap-2">
        <UButton
          v-if="manage && current?.customized"
          color="neutral"
          variant="ghost"
          icon="i-lucide-rotate-ccw"
          :label="t('routerThresholds.reset')"
          :loading="saving"
          data-testid="router-thresholds-reset"
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
            v-if="manage"
            type="submit"
            form="router-thresholds-form"
            :loading="saving"
            :label="t('routerThresholds.save')"
            data-testid="router-thresholds-save"
          />
        </div>
      </div>
    </template>
  </UModal>
</template>
