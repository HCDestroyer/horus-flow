<script setup lang="ts">
import type { EvidenceFlow, Finding } from '~~/types/api'

/**
 * Detalle de un hallazgo (I1-18, frontend.md §8.2): cabecera con severidad, tipo, cliente,
 * confianza y estado; narrativa "Por qué lo marcamos" con razones con dato; evidencia;
 * acciones recomendadas con comandos RouterOS para copiar y deshacer (D11); línea de
 * tiempo; Reconocer / Resolver / Falso positivo. "Ver flujos de evidencia" solo con
 * `security.evidence.read` (acceso auditado). Lenguaje de señales, sin culpa.
 */
const { t, te, d } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const can = useCan()
const toast = useToast()
const { membership } = useTenant()
const slug = computed(() => String(route.params.slug))
const id = computed(() => String(route.params.findingId))

useSectionGuard()(findSection('tenant', 'security/findings'), slug.value)
const { siteName } = useSites()

const {
  data: finding,
  error,
  status,
  refresh,
} = useTenantQuery(
  () => `finding:${id.value}`,
  () => unwrap($api.GET('/findings/{finding_id}', { params: { path: { finding_id: id.value } } })),
)
watch(error, (e) => {
  if (e instanceof ApiError && e.status === 404) {
    showError(createError({ statusCode: 404, statusMessage: 'Not Found' }))
  }
})

const manage = computed(() => can('security.findings.manage'))
const canEvidence = computed(() => can('security.evidence.read'))
const activeState = computed(
  () => finding.value?.state === 'open' || finding.value?.state === 'acknowledged',
)
const kindLabel = computed(() =>
  finding.value && te(`findingKind.${finding.value.kind}`)
    ? t(`findingKind.${finding.value.kind}`)
    : t('findings.kindOther'),
)

const applied = ref<string[]>([])
const transition = ref<'resolve' | 'false_positive'>('resolve')
const transitionOpen = ref(false)
const acknowledging = ref(false)

function open(mode: 'resolve' | 'false_positive') {
  transition.value = mode
  transitionOpen.value = true
}

async function acknowledge() {
  if (!finding.value) return
  acknowledging.value = true
  try {
    finding.value = await unwrap(
      $api.POST('/findings/{finding_id}/acknowledge', {
        params: {
          path: { finding_id: finding.value.id },
          header: { 'If-Match': ifMatch(finding.value.version) },
        },
        body: {},
      }),
    )
    toast.add({ title: t('findings.ack.done'), icon: 'i-lucide-eye', color: 'success' })
  } catch (e) {
    toast.add({
      title: t(describeError(e).messageKey),
      color: 'error',
      icon: 'i-lucide-circle-alert',
    })
    refresh()
  } finally {
    acknowledging.value = false
  }
}

function done(next: Finding) {
  finding.value = next
}

/** Línea de tiempo: apertura, ocurrencias, reconocimiento y cierre. */
const timeline = computed(() => {
  const f = finding.value
  if (!f) return []
  const items: { at: string; icon: string; text: string; detail?: string | null }[] = [
    { at: f.first_seen_at, icon: 'i-lucide-radar', text: t('findings.timeline.firstSeen') },
    {
      at: f.opened_at,
      icon: 'i-lucide-circle-dot',
      text: t('findings.timeline.opened', { rule: f.rule_version }),
    },
  ]
  if (f.acknowledged_at)
    items.push({
      at: f.acknowledged_at,
      icon: 'i-lucide-eye',
      text: t('findings.timeline.acknowledged'),
    })
  items.push({
    at: f.last_seen_at,
    icon: 'i-lucide-repeat',
    text: t('findings.timeline.lastSeen', { n: f.occurrences }),
  })
  if (f.resolution) {
    items.push({
      at: f.resolution.resolved_at,
      icon:
        f.resolution.verdict === 'false_positive'
          ? 'i-lucide-circle-slash'
          : 'i-lucide-circle-check',
      text: t(`findings.timeline.${f.resolution.verdict}`),
      detail: f.resolution.comment,
    })
  }
  return items.sort((a, b) => a.at.localeCompare(b.at))
})

// --- Flujos de evidencia (auditado) ---
const flows = ref<EvidenceFlow[] | null>(null)
const flowsLoading = ref(false)
const flowsError = shallowRef<unknown>(null)
async function loadFlows() {
  flowsLoading.value = true
  flowsError.value = null
  try {
    flows.value = (
      await unwrap(
        $api.GET('/findings/{finding_id}/evidence', { params: { path: { finding_id: id.value } } }),
      )
    ).data
  } catch (e) {
    flowsError.value = e
  } finally {
    flowsLoading.value = false
  }
}

const breadcrumb = computed(() => [
  { label: membership.value?.tenant_name ?? '', to: `/t/${slug.value}` },
  { label: t('nav.items.findings'), to: `/t/${slug.value}/security/findings` },
  { label: finding.value?.summary.text ?? '' },
])

useHead({ title: () => finding.value?.summary.text ?? t('findings.detailTitle') })
</script>

<template>
  <AppPage :title="t('findings.detailTitle')" panel-id="finding-detail">
    <UBreadcrumb :items="breadcrumb" class="min-w-0" />
    <LoadingState v-if="status === 'pending' && !finding" :rows="6" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />

    <template v-else-if="finding">
      <UCard data-testid="finding-header">
        <div class="flex flex-col gap-4 xl:flex-row xl:items-start xl:justify-between">
          <div class="flex min-w-0 flex-col gap-2">
            <p class="flex flex-wrap items-center gap-x-3 gap-y-1">
              <SeverityBadge :severity="finding.severity" />
              <span class="text-muted">· {{ kindLabel }}</span>
            </p>
            <h2 class="text-highlighted text-lg font-semibold">{{ finding.summary.text }}</h2>
            <p class="text-muted flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
              <NuxtLink
                v-if="finding.customer && !finding.customer.address_masked"
                :to="`/t/${slug}/clients/${finding.customer_id}`"
                class="hover:underline"
              >
                <ClientAddress
                  :address="finding.customer.address"
                  :alias="finding.customer.alias"
                  :stacked="false"
                />
              </NuxtLink>
              <ClientAddress
                v-else-if="finding.customer"
                :address="finding.customer.address"
                :alias="finding.customer.alias"
                :stacked="false"
              />
              <span>({{ siteName(finding.site_id) }})</span>
            </p>
            <p class="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
              <span class="text-muted">{{ t('findings.col.confidence') }}:</span>
              <ConfidenceLabel :level="finding.confidence_level" :value="finding.confidence" />
              <span class="text-muted">·</span>
              <FindingStateBadge :state="finding.state" />
              <span class="text-muted"
                >·
                {{
                  t('findings.firstLast', {
                    first: d(new Date(finding.first_seen_at), 'time'),
                    last: d(new Date(finding.last_seen_at), 'time'),
                  })
                }}</span
              >
              <span class="text-muted"
                >· {{ t('findings.occurrences', { n: finding.occurrences }) }}</span
              >
            </p>
          </div>
          <div
            v-if="manage && activeState"
            class="flex shrink-0 flex-wrap gap-2"
            data-testid="finding-actions"
          >
            <UButton
              v-if="finding.state === 'open'"
              color="neutral"
              variant="outline"
              icon="i-lucide-eye"
              :loading="acknowledging"
              :label="t('findings.ack.action')"
              data-testid="ack"
              @click="acknowledge"
            />
            <UButton
              icon="i-lucide-circle-check"
              :label="t('findings.resolve.action')"
              data-testid="resolve"
              @click="open('resolve')"
            />
            <UButton
              color="neutral"
              variant="outline"
              icon="i-lucide-circle-slash"
              :label="t('findings.fp.action')"
              data-testid="false-positive"
              @click="open('false_positive')"
            />
          </div>
        </div>
        <UAlert
          v-if="finding.resolution"
          class="mt-4"
          color="neutral"
          variant="subtle"
          :icon="
            finding.resolution.verdict === 'false_positive'
              ? 'i-lucide-circle-slash'
              : 'i-lucide-circle-check'
          "
          :title="t(`findings.timeline.${finding.resolution.verdict}`)"
          :description="finding.resolution.comment ?? undefined"
          data-testid="finding-resolution"
        />
      </UCard>

      <div class="grid gap-6 xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
        <div class="flex min-w-0 flex-col gap-6">
          <UCard data-testid="finding-why">
            <h2 class="text-highlighted text-base font-semibold">{{ t('findings.why') }}</h2>
            <p class="text-default mt-2">
              {{
                te(`findings.narrative.${finding.kind}`)
                  ? t(`findings.narrative.${finding.kind}`)
                  : t('findings.narrative.other')
              }}
            </p>
            <ul class="mt-3 flex flex-col gap-2" data-testid="finding-reasons">
              <li v-for="r in finding.reasons" :key="r.code" class="flex gap-2">
                <UIcon
                  name="i-lucide-check"
                  class="text-muted mt-1 size-4 shrink-0"
                  aria-hidden="true"
                />
                <span class="text-default">{{ r.detail }}</span>
              </li>
            </ul>
            <USeparator class="my-4" />
            <h3 class="text-highlighted mb-3 text-sm font-semibold">
              {{ t('findings.evidence.title') }}
            </h3>
            <FindingEvidence :evidence="finding.evidence" />
            <div v-if="canEvidence" class="mt-4 flex flex-col gap-3">
              <UButton
                v-if="!flows"
                color="neutral"
                variant="outline"
                icon="i-lucide-list-tree"
                :loading="flowsLoading"
                :label="t('findings.flows.action')"
                class="self-start"
                data-testid="evidence-flows"
                @click="loadFlows"
              />
              <p v-if="!flows" class="text-muted text-xs">{{ t('findings.flows.audited') }}</p>
              <ErrorState v-if="flowsError" :error="flowsError" @retry="loadFlows" />
              <div v-if="flows" class="ring-default overflow-hidden rounded-md ring-1">
                <table class="w-full text-xs" data-testid="evidence-flows-table">
                  <caption class="sr-only">
                    {{
                      t('findings.flows.caption')
                    }}
                  </caption>
                  <thead class="bg-elevated/50 text-muted text-left">
                    <tr>
                      <th scope="col" class="px-2 py-1.5 font-medium">
                        {{ t('findings.flows.time') }}
                      </th>
                      <th scope="col" class="px-2 py-1.5 font-medium">
                        {{ t('findings.flows.remote') }}
                      </th>
                      <th scope="col" class="px-2 py-1.5 text-end font-medium">
                        {{ t('findings.flows.bytes') }}
                      </th>
                    </tr>
                  </thead>
                  <tbody class="divide-default divide-y">
                    <tr v-for="(f, i) in flows.slice(0, 15)" :key="i">
                      <td class="px-2 py-1 tabular">{{ d(new Date(f.ts), 'time') }}</td>
                      <td class="px-2 py-1 font-mono break-all">
                        {{ f.remote_ip }}:{{ f.remote_port }} ·
                        {{ f.protocol === 17 ? 'UDP' : 'TCP' }}
                      </td>
                      <td class="px-2 py-1 text-end tabular">{{ f.bytes }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>
          </UCard>

          <UCard>
            <h2 class="text-highlighted mb-3 text-base font-semibold">
              {{ t('findings.timeline.title') }}
            </h2>
            <ol class="flex flex-col gap-3" data-testid="finding-timeline">
              <li v-for="(item, i) in timeline" :key="i" class="flex gap-3">
                <UIcon
                  :name="item.icon"
                  class="text-muted mt-0.5 size-4 shrink-0"
                  aria-hidden="true"
                />
                <div class="min-w-0">
                  <p class="text-default text-sm">{{ item.text }}</p>
                  <p class="text-muted text-xs">{{ d(new Date(item.at), 'long') }}</p>
                  <p v-if="item.detail" class="text-default mt-0.5 text-sm">“{{ item.detail }}”</p>
                </div>
              </li>
            </ol>
          </UCard>
        </div>

        <RecommendedActions
          v-model:applied="applied"
          :actions="finding.recommended_actions"
          :finding-id="finding.id"
        />
      </div>

      <FindingTransitionModal
        v-model:open="transitionOpen"
        :finding="finding"
        :mode="transition"
        :applied-actions="applied"
        @done="done"
      />
    </template>
  </AppPage>
</template>
