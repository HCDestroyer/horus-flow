<script setup lang="ts">
import type { AssistAnalysis, AssistFeature } from '~~/types/api'

/**
 * "Analizar con IA" (docs/assist.md): pide al asistente un análisis del hallazgo o del router.
 * La IA solo ve datos enmascarados y usa herramientas de solo lectura como el usuario. La
 * respuesta se muestra como Markdown seguro con el aviso de que es generada por IA y de que
 * Horus no ejecuta nada en los routers. Se oculta sin permiso de lectura del recurso; si el
 * asistente no está activo, explica cómo activarlo (enlace a la página de administración).
 */
const props = defineProps<{ feature: AssistFeature; contextId: string }>()
const { t, d } = useI18n()
const { $api } = useNuxtApp()
const route = useRoute()
const can = useCan()

const READ: Record<string, string> = { finding: 'security.findings.read', router: 'devices.read' }
const allowed = computed(() => can(READ[props.feature] ?? 'settings.manage'))
const slug = computed(() => String(route.params.slug))

const { data: status, refresh: refreshStatus } = useTenantQuery(
  'assist-status',
  () => unwrap($api.GET('/assist/status')),
  { immediate: allowed.value },
)

const result = ref<AssistAnalysis | null>(null)
const running = ref(false)
const failure = ref<{ title: string; detail?: string } | null>(null)

// Texto de progreso: el análisis tarda (el modelo consulta varias herramientas).
const STEPS: Record<string, string[]> = {
  finding: ['collect', 'evidence', 'write'],
  router: ['collect', 'router', 'write'],
}
const step = ref(0)
let timer: ReturnType<typeof setInterval> | undefined
const progressText = computed(() => {
  const steps = STEPS[props.feature] ?? ['collect', 'write']
  return t(`assist.analysis.progress.${steps[Math.min(step.value, steps.length - 1)]}`)
})
onBeforeUnmount(() => clearInterval(timer))

const groundingTitle = computed(() => {
  const g = result.value?.grounding
  if (!g) return ''
  if (g.status === 'verified') {
    return t(
      g.repaired
        ? 'assist.analysis.grounding.verifiedRepaired'
        : 'assist.analysis.grounding.verified',
    )
  }
  return t(`assist.analysis.grounding.${g.status}`)
})
const groundingDetail = computed(() => {
  const g = result.value?.grounding
  if (!g || g.status === 'verified') return undefined
  const parts: string[] = []
  if (g.unsupported.length)
    parts.push(t('assist.analysis.grounding.unsupported', { list: g.unsupported.join(', ') }))
  if (g.unverified_commands.length) {
    parts.push(t('assist.analysis.grounding.commands', { list: g.unverified_commands.join(' · ') }))
  }
  return parts.length ? parts.join(' ') : t('assist.analysis.grounding.partialDetail')
})

const ERRORS: Record<string, string> = {
  ASSIST_LIMIT_REACHED: 'limit',
  ASSIST_PROVIDER_UNAVAILABLE: 'provider',
  ASSIST_REFUSED: 'refused',
  ASSIST_DISABLED: 'disabled',
  ASSIST_NOT_CONFIGURED: 'notConfigured',
  ASSIST_INCOMPLETE: 'incomplete',
  ASSIST_CONTEXT_NOT_FOUND: 'notFound',
  ASSIST_KEY_STORE_UNAVAILABLE: 'notConfigured',
}

async function analyze() {
  running.value = true
  failure.value = null
  step.value = 0
  clearInterval(timer)
  timer = setInterval(() => step.value++, 4000)
  try {
    result.value = await unwrap(
      $api.POST('/assist/analyses', {
        body: { feature: props.feature, context_id: props.contextId },
      }),
    )
  } catch (e) {
    const key = e instanceof ApiError ? ERRORS[e.code] : undefined
    if (key) {
      failure.value = {
        title: t(`assist.analysis.errors.${key}`),
        detail: e instanceof ApiError ? e.problem.detail : undefined,
      }
    } else {
      const info = describeError(e)
      failure.value = { title: t(info.messageKey, info.params ?? {}) }
    }
  } finally {
    clearInterval(timer)
    running.value = false
    refreshStatus()
  }
}

const toolList = computed(() =>
  (result.value?.tool_calls ?? [])
    .map((c) => t(`assist.toolNames.${c.name}`, c.name))
    .filter((v, i, a) => a.indexOf(v) === i)
    .join(', '),
)
const providerLabel = (p: string) => t(`assist.providers.${p}`, p)
</script>

<template>
  <section
    v-if="allowed"
    class="ring-default flex flex-col gap-3 rounded-md p-4 ring-1"
    :aria-labelledby="`assist-${feature}-${contextId}`"
    data-testid="assist-analysis"
  >
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="flex min-w-0 flex-col gap-1">
        <h3
          :id="`assist-${feature}-${contextId}`"
          class="text-highlighted flex items-center gap-2 font-semibold"
        >
          <UIcon name="i-lucide-sparkles" class="text-primary size-4" aria-hidden="true" />
          {{ t('assist.analysis.title') }}
        </h3>
        <p class="text-muted text-sm">{{ t(`assist.analysis.intro.${feature}`) }}</p>
      </div>
      <UButton
        v-if="status?.enabled"
        :loading="running"
        :icon="result ? 'i-lucide-refresh-cw' : 'i-lucide-sparkles'"
        :label="result ? t('assist.analysis.rerun') : t('assist.analysis.run')"
        :color="result ? 'neutral' : 'primary'"
        :variant="result ? 'outline' : 'solid'"
        data-testid="assist-run"
        @click="analyze"
      />
    </div>

    <p v-if="status && !status.enabled" class="text-muted text-sm" data-testid="assist-disabled">
      {{ t('assist.analysis.disabled') }}
      <ULink
        v-if="can('settings.read')"
        :to="`/t/${slug}/admin/ai`"
        class="text-primary font-medium"
        data-testid="assist-configure"
        >{{ t('assist.analysis.configure') }}</ULink
      >
      <template v-else>{{ t('assist.analysis.askAdmin') }}</template>
    </p>

    <p
      v-if="running"
      class="text-muted flex items-center gap-2 text-sm"
      role="status"
      aria-live="polite"
      data-testid="assist-progress"
    >
      <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" aria-hidden="true" />
      {{ progressText }}
    </p>

    <UAlert
      v-if="failure && !running"
      color="error"
      variant="subtle"
      icon="i-lucide-circle-alert"
      :title="failure.title"
      :description="failure.detail"
      data-testid="assist-error"
    />

    <div v-if="result && !running" class="flex flex-col gap-3" data-testid="assist-result">
      <UAlert
        color="warning"
        variant="subtle"
        icon="i-lucide-triangle-alert"
        :title="result.disclaimer || t('assist.analysis.disclaimer')"
        data-testid="assist-disclaimer"
      />
      <AssistMarkdown :source="result.markdown" />
      <!-- Anclaje (docs/assist.md §Anclaje): qué pudo verificarse contra los datos de Horus. -->
      <UAlert
        v-if="result.grounding"
        :color="result.grounding.status === 'verified' ? 'success' : 'warning'"
        variant="subtle"
        :icon="
          result.grounding.status === 'verified' ? 'i-lucide-shield-check' : 'i-lucide-shield-alert'
        "
        :title="groundingTitle"
        :description="groundingDetail"
        data-testid="assist-grounding"
        :data-status="result.grounding.status"
      />
      <UAlert
        v-if="result.truncated"
        color="neutral"
        variant="subtle"
        :title="t('assist.analysis.truncated')"
      />
      <p class="text-dimmed text-xs" data-testid="assist-meta">
        {{
          t('assist.analysis.meta', {
            model: result.model,
            provider: providerLabel(result.provider),
            time: d(new Date(result.generated_at), 'long'),
          })
        }}
        <template v-if="toolList"> · {{ t('assist.analysis.tools', { list: toolList }) }}</template>
        ·
        {{
          t('assist.analysis.tokens', {
            input: result.usage.input_tokens.toLocaleString('es'),
            output: result.usage.output_tokens.toLocaleString('es'),
          })
        }}
      </p>
    </div>
    <p v-if="status?.enabled && !running" class="text-dimmed text-xs">
      {{ t('assist.analysis.remaining', { n: status.remaining_today }, status.remaining_today) }}
    </p>
  </section>
</template>
