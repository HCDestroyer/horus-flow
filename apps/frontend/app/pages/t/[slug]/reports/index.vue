<script setup lang="ts">
import type { ReportDefinition, ReportFormat, ReportRun } from '~~/types/api'

/**
 * Reportes del ISP (I3, rol reporting): reporte semanal de seguridad en PDF y CSV. Lista de
 * ejecuciones (programadas y a petición) con estado, periodo en la zona del ISP, tamaño y
 * descargas autenticadas (el archivo contiene IPs de clientes: se pide con el token y se guarda
 * con un enlace local, nunca con una URL compartible); «Generar ahora» con rango opcional y la
 * programación semanal. Se refresca cada 5 s mientras haya alguno generándose y al llegar el
 * evento `horus.reporting.report.*` por el topic `me`.
 */
const { t, d } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const toast = useToast()
const can = useCan()
const slug = computed(() => String(route.params.slug))
useSectionGuard()(findSection('tenant', 'reports'), slug.value)
const canExport = computed(() => can('reports.export'))
const canSchedule = computed(() => can('settings.manage'))

// --- Ejecuciones -------------------------------------------------------------------------
const extra = ref<ReportRun[]>([])
const cursor = ref<string | null>(null)
const { data, error, status, refresh } = useTenantQuery('reports', async () => {
  const res = await unwrap($api.GET('/reports', { params: { query: { limit: 20 } } }))
  cursor.value = res.page.next_cursor ?? null
  extra.value = []
  return res
})
const runs = computed(() => [...(data.value?.data ?? []), ...extra.value])
const pending = computed(() => runs.value.some(isReportPending))
useAutoRefresh(() => pending.value && refresh(), 5_000)
useAutoRefresh(() => refresh(), 60_000)
useRealtime('me', (m) => {
  if (m.type === 'event' && m.event.type.startsWith('horus.reporting.report.')) {
    refresh()
    if (m.event.type.endsWith('.completed')) {
      toast.add({ title: t('reports.ready'), color: 'success', icon: 'i-lucide-circle-check' })
    }
  }
})
const loadingMore = ref(false)
async function loadMore() {
  if (!cursor.value) return
  loadingMore.value = true
  try {
    const res = await unwrap(
      $api.GET('/reports', { params: { query: { limit: 20, cursor: cursor.value } } }),
    )
    extra.value = [...extra.value, ...res.data]
    cursor.value = res.page.next_cursor ?? null
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  } finally {
    loadingMore.value = false
  }
}

function periodTitle(r: ReportRun) {
  if (!r.scheduled) return t('reports.customRange')
  const w = parseIsoWeek(r.iso_week)
  return w ? t('reports.week', { week: w.week, year: w.year }) : r.iso_week
}

// --- Descarga ----------------------------------------------------------------------------
const downloading = ref<string | null>(null)
async function download(r: ReportRun, format: ReportFormat) {
  downloading.value = `${r.id}:${format}`
  try {
    const res = await $api.GET('/reports/{report_id}/download', {
      params: { path: { report_id: r.id }, query: { format } },
      parseAs: 'blob',
    })
    if (!res.response.ok) {
      let body: unknown = res.error
      if (typeof body === 'string') {
        try {
          body = JSON.parse(body)
        } catch {
          // no era JSON
        }
      }
      throw new ApiError(toProblem(res.response.status, body))
    }
    const blob = res.data as unknown as Blob
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filenameFrom(
      res.response.headers.get('Content-Disposition'),
      `horus-seguridad-${r.iso_week}.${format === 'pdf' ? 'pdf' : 'zip'}`,
    )
    a.click()
    URL.revokeObjectURL(url)
  } catch (e) {
    toast.add({
      title: t('reports.downloadFailed'),
      description: e instanceof ApiError && e.problem.detail ? e.problem.detail : undefined,
      color: 'error',
    })
  } finally {
    downloading.value = null
  }
}

// --- Programación -------------------------------------------------------------------------
const {
  data: defs,
  refresh: refreshDefs,
  error: defsError,
} = useTenantQuery('report-definitions', () =>
  unwrap($api.GET('/report-definitions')).then((r) => r.data),
)
const definition = computed<ReportDefinition | null>(
  () => defs.value?.find((x) => x.kind === 'security_weekly') ?? null,
)
const tz = computed(() => definition.value?.timezone ?? 'UTC')
const form = reactive({
  enabled: true,
  weekday: 1,
  time: '07:00',
  timezone: 'UTC',
  formats: ['pdf', 'csv'] as ReportFormat[],
  recipients: '',
})
const formErrors = ref<Record<string, string>>({})
function load(def: ReportDefinition | null) {
  if (!def) return
  Object.assign(form, {
    enabled: def.enabled,
    weekday: def.weekday,
    time: `${String(def.hour).padStart(2, '0')}:${String(def.minute).padStart(2, '0')}`,
    timezone: def.timezone,
    formats: [...def.formats],
    recipients: def.recipients.join(', '),
  })
}
watch(definition, load, { immediate: true })
const weekdayItems = computed(() =>
  [1, 2, 3, 4, 5, 6, 7].map((n) => ({ label: t(`reports.weekday.${n}`), value: n })),
)
const zones = computed(() => {
  const list =
    typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : []
  return [...new Set([form.timezone, ...list])].filter(Boolean)
})
function toggle(list: ReportFormat[], f: ReportFormat, on: boolean) {
  const out = list.filter((x) => x !== f)
  if (on) out.push(f)
  return REPORT_FORMATS.filter((x) => out.includes(x))
}
const savingSchedule = ref(false)
async function saveSchedule() {
  const def = definition.value
  if (!def) return
  formErrors.value = {}
  const [hour, minute] = form.time.split(':').map(Number)
  const recipients = form.recipients
    .split(/[,;\s]+/)
    .map((s) => s.trim())
    .filter(Boolean)
  savingSchedule.value = true
  try {
    const updated = await unwrap(
      $api.PATCH('/report-definitions/{definition_id}', {
        params: { path: { definition_id: def.id }, header: { 'If-Match': ifMatch(def.version) } },
        body: {
          enabled: form.enabled,
          weekday: form.weekday,
          hour: hour ?? 0,
          minute: minute ?? 0,
          timezone: form.timezone,
          formats: form.formats,
          ...(def.email_available ? { recipients } : {}),
        },
      }),
    )
    defs.value = (defs.value ?? []).map((x) => (x.id === updated.id ? updated : x))
    toast.add({ title: t('reports.schedule.saved'), color: 'success', icon: 'i-lucide-check' })
  } catch (e) {
    if (e instanceof ApiError && e.status === 412) {
      toast.add({ title: t('reports.schedule.conflict'), color: 'warning' })
      await refreshDefs()
      load(definition.value)
    } else if (e instanceof ApiError && e.problem.errors?.length) {
      formErrors.value = Object.fromEntries(
        e.problem.errors.map((x) => [x.field.split('.')[0]!, x.message]),
      )
    } else {
      toast.add({ title: t(describeError(e).messageKey), color: 'error' })
    }
  } finally {
    savingSchedule.value = false
  }
}

// --- Generar ahora ------------------------------------------------------------------------
const genOpen = ref(false)
const gen = reactive({
  mode: 'lastWeek' as 'lastWeek' | 'custom',
  from: '',
  to: '',
  formats: ['pdf', 'csv'] as ReportFormat[],
  key: '',
})
const genError = ref('')
const generating = ref(false)
function openGenerate() {
  const today = todayInZone(tz.value)
  Object.assign(gen, {
    mode: 'lastWeek',
    from: addCalendarDays(today, -7),
    to: addCalendarDays(today, -1),
    formats: definition.value?.formats.length ? [...definition.value.formats] : ['pdf', 'csv'],
    // Una clave por diálogo: reintentar el envío no duplica el reporte.
    key: idempotencyKey(),
  })
  genError.value = ''
  genOpen.value = true
}
const customPeriod = computed(() =>
  gen.mode === 'custom' ? dayRangeToPeriod(gen.from, gen.to, tz.value) : null,
)
async function generate() {
  genError.value = ''
  if (gen.mode === 'custom' && !customPeriod.value) {
    genError.value = t('reports.rangeInvalid')
    return
  }
  generating.value = true
  try {
    const res = await $api.POST('/reports', {
      params: { header: { 'Idempotency-Key': gen.key } },
      body: {
        kind: 'security_weekly',
        formats: gen.formats,
        ...(customPeriod.value ?? {}),
      },
    })
    const run = await unwrap(Promise.resolve(res))
    genOpen.value = false
    const replayed = res.response.headers.get('Idempotency-Replayed') === 'true'
    toast.add({
      title: replayed ? t('reports.replayed') : t('reports.queued'),
      color: 'neutral',
      icon: 'i-lucide-loader-circle',
    })
    if (data.value && !data.value.data.some((x) => x.id === run.id)) {
      data.value = { ...data.value, data: [run, ...data.value.data] }
    }
  } catch (e) {
    if (e instanceof ApiError && e.status === 429) {
      genError.value = t('reports.rateLimited', {
        minutes: Math.max(1, Math.ceil((e.retryAfter ?? 60) / 60)),
      })
    } else if (e instanceof ApiError && e.problem.errors?.length) {
      genError.value = e.problem.errors.map((x) => x.message).join(' ')
    } else {
      genError.value = t(describeError(e).messageKey)
    }
  } finally {
    generating.value = false
  }
}

const emptyHint = computed(() => {
  const def = definition.value
  if (!def?.enabled || !def.next_run_at) return t('reports.emptyNoSchedule')
  const local = formatInZone(def.next_run_at, def.timezone)
  return t('reports.emptyHint', {
    day: t(`reports.weekday.${def.weekday}`),
    time: local.slice(11),
  })
})

useHead({ title: () => t('reports.title') })
</script>

<template>
  <AppPage :title="t('reports.title')" panel-id="reports">
    <template v-if="canExport" #toolbar-right>
      <UButton
        icon="i-lucide-file-plus-2"
        :label="t('reports.generate')"
        data-testid="report-generate"
        @click="openGenerate"
      />
    </template>

    <p class="text-muted mb-4 max-w-3xl text-sm">
      {{ t('reports.intro') }}
      <span v-if="definition" class="text-dimmed">{{ t('reports.zone', { zone: tz }) }}.</span>
    </p>

    <div class="grid gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
      <section class="min-w-0" :aria-label="t('reports.title')">
        <LoadingState v-if="status === 'pending' && !data" :rows="4" />
        <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
        <UCard v-else-if="!runs.length" class="py-6">
          <EmptyState
            icon="i-lucide-file-chart-column"
            :title="t('reports.empty')"
            :description="emptyHint"
          />
        </UCard>
        <template v-else>
          <div class="ring-default overflow-x-auto rounded-md ring-1">
            <table class="w-full text-sm" data-testid="reports-table">
              <thead class="bg-elevated/50 text-muted text-left text-xs">
                <tr>
                  <th scope="col" class="px-3 py-2 font-medium">
                    {{ t('reports.columns.period') }}
                  </th>
                  <th scope="col" class="px-3 py-2 font-medium">
                    {{ t('reports.columns.status') }}
                  </th>
                  <th scope="col" class="hidden px-3 py-2 text-right font-medium sm:table-cell">
                    {{ t('reports.columns.size') }}
                  </th>
                  <th scope="col" class="hidden px-3 py-2 font-medium md:table-cell">
                    {{ t('reports.columns.created') }}
                  </th>
                  <th scope="col" class="px-3 py-2 text-right font-medium">
                    {{ t('reports.columns.actions') }}
                  </th>
                </tr>
              </thead>
              <tbody class="divide-default divide-y">
                <tr
                  v-for="r in runs"
                  :key="r.id"
                  data-testid="report-row"
                  :data-status="r.status"
                  :data-report="r.id"
                >
                  <td class="px-3 py-2">
                    <p class="text-highlighted font-medium">{{ periodTitle(r) }}</p>
                    <p class="text-muted tabular text-xs">{{ formatReportPeriod(r) }}</p>
                    <p class="text-dimmed text-xs">
                      {{ r.scheduled ? t('reports.origin.scheduled') : t('reports.origin.manual') }}
                      <template v-if="r.timezone !== tz">
                        · {{ t('reports.zone', { zone: r.timezone }) }}
                      </template>
                    </p>
                  </td>
                  <td class="px-3 py-2">
                    <UBadge
                      v-if="r.expired"
                      color="neutral"
                      variant="outline"
                      icon="i-lucide-archive-x"
                      :label="t('reports.expired')"
                      data-testid="report-status"
                    />
                    <UBadge
                      v-else
                      :color="REPORT_STATUS_META[r.status].color"
                      variant="subtle"
                      :icon="REPORT_STATUS_META[r.status].icon"
                      :ui="{
                        leadingIcon: REPORT_STATUS_META[r.status].spinning
                          ? 'animate-spin motion-reduce:animate-none'
                          : '',
                      }"
                      :label="t(`reports.status.${r.status}`)"
                      data-testid="report-status"
                    />
                    <p
                      v-if="r.status === 'failed' && r.error"
                      class="text-error mt-1 max-w-xs text-xs"
                      data-testid="report-error"
                    >
                      {{ r.error }}
                    </p>
                    <p v-else-if="r.expired" class="text-muted mt-1 text-xs">
                      {{ t('reports.expiredHint') }}
                    </p>
                  </td>
                  <td class="tabular hidden px-3 py-2 text-right whitespace-nowrap sm:table-cell">
                    {{ r.size_bytes ? joinUnit(formatBytes(r.size_bytes)) : '—' }}
                  </td>
                  <td class="hidden px-3 py-2 md:table-cell">
                    <RelativeTime :at="r.created_at" />
                    <p v-if="r.expires_at && !r.expired" class="text-dimmed text-xs">
                      {{ t('reports.expires', { date: d(new Date(r.expires_at), 'short') }) }}
                    </p>
                  </td>
                  <td class="px-3 py-2">
                    <span class="flex flex-col items-end gap-1 sm:flex-row sm:justify-end">
                      <UButton
                        v-for="f in r.files"
                        :key="f.format"
                        size="sm"
                        color="neutral"
                        variant="outline"
                        icon="i-lucide-download"
                        :label="t(`reports.formatLabel.${f.format}`)"
                        :loading="downloading === `${r.id}:${f.format}`"
                        :disabled="r.expired"
                        :aria-label="
                          t('reports.downloadAria', {
                            format: t(`reports.formatLabel.${f.format}`),
                            period: formatReportPeriod(r),
                          })
                        "
                        :data-testid="`report-download-${f.format}`"
                        @click="download(r, f.format)"
                      />
                    </span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-if="cursor" class="mt-3">
            <UButton
              color="neutral"
              variant="ghost"
              :loading="loadingMore"
              :label="t('reports.more')"
              @click="loadMore"
            />
          </div>
        </template>
      </section>

      <!-- Programación semanal -->
      <aside>
        <UCard data-testid="report-schedule" :ui="{ body: 'p-4 sm:p-4' }">
          <template #header>
            <div class="flex flex-col gap-2">
              <h2 class="text-highlighted font-semibold">{{ t('reports.schedule.title') }}</h2>
              <p class="text-muted text-xs">{{ t('reports.schedule.body') }}</p>
              <UBadge
                v-if="definition"
                class="self-start"
                :color="definition.enabled ? 'success' : 'neutral'"
                variant="subtle"
                :icon="definition.enabled ? 'i-lucide-calendar-check' : 'i-lucide-calendar-off'"
                :label="
                  definition.enabled && definition.next_run_at
                    ? t('reports.schedule.next', {
                        date: formatInZone(definition.next_run_at, definition.timezone),
                      })
                    : t('reports.schedule.off')
                "
                data-testid="report-next-run"
              />
            </div>
          </template>
          <ErrorState v-if="defsError" :error="defsError" @retry="refreshDefs()" />
          <LoadingState v-else-if="!definition" :rows="3" />
          <form v-else class="flex flex-col gap-4" @submit.prevent="saveSchedule">
            <UFormField :label="t('reports.schedule.enabled')">
              <USwitch
                v-model="form.enabled"
                :disabled="!canSchedule"
                :aria-label="t('reports.schedule.enabled')"
                data-testid="schedule-enabled"
              />
            </UFormField>
            <div class="grid grid-cols-2 gap-3">
              <UFormField :label="t('reports.schedule.day')" :error="formErrors.weekday">
                <USelect
                  v-model="form.weekday"
                  :items="weekdayItems"
                  :disabled="!canSchedule"
                  class="w-full"
                  data-testid="schedule-weekday"
                />
              </UFormField>
              <UFormField
                :label="t('reports.schedule.time')"
                :error="formErrors.hour || formErrors.minute"
              >
                <UInput
                  v-model="form.time"
                  type="time"
                  :disabled="!canSchedule"
                  class="w-full"
                  data-testid="schedule-time"
                />
              </UFormField>
            </div>
            <UFormField :label="t('reports.schedule.timezone')" :error="formErrors.timezone">
              <USelectMenu
                v-model="form.timezone"
                :items="zones"
                :disabled="!canSchedule"
                class="w-full"
                data-testid="schedule-timezone"
              />
            </UFormField>
            <UFormField :label="t('reports.schedule.formats')" :error="formErrors.formats">
              <div class="flex gap-4">
                <UCheckbox
                  v-for="f in REPORT_FORMATS"
                  :key="f"
                  :model-value="form.formats.includes(f)"
                  :label="t(`reports.formatLabel.${f}`)"
                  :disabled="!canSchedule || (form.formats.length === 1 && form.formats[0] === f)"
                  :data-testid="`schedule-format-${f}`"
                  @update:model-value="(v) => (form.formats = toggle(form.formats, f, !!v))"
                />
              </div>
            </UFormField>
            <UFormField
              :label="t('reports.schedule.recipients')"
              :description="
                definition.email_available
                  ? t('reports.schedule.recipientsHelp')
                  : t('reports.schedule.recipientsUnavailable')
              "
              :error="formErrors.recipients"
            >
              <UTextarea
                v-model="form.recipients"
                :rows="2"
                autoresize
                :disabled="!canSchedule || !definition.email_available"
                class="w-full"
                placeholder="noc@isp.example"
                data-testid="schedule-recipients"
              />
            </UFormField>
            <p v-if="!canSchedule" class="text-muted text-xs">
              {{ t('reports.schedule.readOnly') }}
            </p>
            <div v-else class="flex justify-end">
              <UButton
                type="submit"
                :loading="savingSchedule"
                :label="t('common.save')"
                data-testid="schedule-save"
              />
            </div>
          </form>
        </UCard>
      </aside>
    </div>

    <UModal
      v-model:open="genOpen"
      :title="t('reports.generateTitle')"
      :description="t('reports.generateBody')"
    >
      <template #body>
        <form id="report-generate-form" class="flex flex-col gap-4" @submit.prevent="generate">
          <URadioGroup
            v-model="gen.mode"
            :items="[
              { label: t('reports.rangeMode.lastWeek'), value: 'lastWeek' },
              { label: t('reports.rangeMode.custom'), value: 'custom' },
            ]"
            data-testid="generate-mode"
          />
          <div v-if="gen.mode === 'custom'" class="grid grid-cols-2 gap-3">
            <UFormField :label="t('reports.from')">
              <UInput v-model="gen.from" type="date" class="w-full" data-testid="generate-from" />
            </UFormField>
            <UFormField :label="t('reports.to')">
              <UInput v-model="gen.to" type="date" class="w-full" data-testid="generate-to" />
            </UFormField>
            <p class="text-muted col-span-2 text-xs">{{ t('reports.zone', { zone: tz }) }}</p>
          </div>
          <UFormField :label="t('reports.formats')">
            <div class="flex gap-4">
              <UCheckbox
                v-for="f in REPORT_FORMATS"
                :key="f"
                :model-value="gen.formats.includes(f)"
                :label="t(`reports.formatLabel.${f}`)"
                :disabled="gen.formats.length === 1 && gen.formats[0] === f"
                @update:model-value="(v) => (gen.formats = toggle(gen.formats, f, !!v))"
              />
            </div>
          </UFormField>
          <UAlert
            v-if="genError"
            color="error"
            variant="subtle"
            icon="i-lucide-circle-alert"
            :title="genError"
            data-testid="generate-error"
          />
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            :label="t('common.cancel')"
            @click="genOpen = false"
          />
          <UButton
            type="submit"
            form="report-generate-form"
            icon="i-lucide-file-plus-2"
            :loading="generating"
            :label="t('reports.submit')"
            data-testid="generate-submit"
          />
        </div>
      </template>
    </UModal>
  </AppPage>
</template>
