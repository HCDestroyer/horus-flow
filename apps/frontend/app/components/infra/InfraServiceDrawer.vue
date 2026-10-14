<script setup lang="ts">
import type { InfraCheck, InfraService } from '~~/types/api'

/**
 * Detalle de un servicio de infraestructura: estado del servicio y de cada chequeo (razón,
 * latencia, desde cuándo, fallos seguidos) con sus últimos resultados como tira de puntos
 * (el más reciente a la derecha). Lee `GET /infra-services/{id}`, que trae `recent`.
 */
const props = defineProps<{ serviceId: string | null }>()
const open = defineModel<boolean>('open', { default: false })
defineEmits<{ edit: [InfraService]; snippet: [InfraService] }>()

const { t, d } = useI18n()
const { $api } = useNuxtApp()
const can = useCan()
const { siteName } = useSites()

const service = ref<InfraService | null>(null)
const loading = ref(false)
const error = ref<unknown>(null)

async function load() {
  if (!props.serviceId) return
  loading.value = true
  error.value = null
  try {
    service.value = await unwrap(
      $api.GET('/infra-services/{service_id}', {
        params: { path: { service_id: props.serviceId } },
      }),
    )
  } catch (e) {
    error.value = e
  } finally {
    loading.value = false
  }
}
watch(
  () => [open.value, props.serviceId] as const,
  ([o]) => {
    if (o) load()
  },
  { immediate: true },
)

function checkOf(id: string): InfraCheck | undefined {
  return service.value?.checks.find((c) => c.id === id)
}
function reasonLabel(r: string | null | undefined) {
  if (!r) return ''
  const key = `infra.reason.${r}`
  const out = t(key)
  return out === key ? r : out
}
const dotClass = {
  up: 'bg-success',
  down: 'bg-error',
  degraded: 'bg-warning',
  unknown: 'bg-neutral-400 dark:bg-neutral-600',
} as const
</script>

<template>
  <USlideover
    v-model:open="open"
    :title="service?.name ?? t('infra.drawer.title')"
    :description="
      service ? `${service.target} · ${t(`infra.kind.${service.kind}`, service.kind)}` : undefined
    "
    :ui="{ content: 'sm:max-w-xl' }"
  >
    <template #body>
      <LoadingState v-if="loading && !service" :rows="3" />
      <ErrorState v-else-if="error" :error="error" @retry="load()" />
      <div v-else-if="service" class="flex flex-col gap-4" data-testid="infra-drawer">
        <div class="flex flex-wrap items-center gap-2">
          <UBadge
            :color="INFRA_STATE_META[service.status.state].color"
            :icon="INFRA_STATE_META[service.status.state].icon"
            variant="subtle"
            size="lg"
            :label="t(`infra.state.${service.status.state}`)"
            data-testid="infra-drawer-state"
          />
          <span v-if="service.status.check_type" class="text-muted text-sm">
            {{ t('infra.causedBy', { check: t(`infra.checkType.${service.status.check_type}`) }) }}
            · {{ reasonLabel(service.status.reason) }}
          </span>
          <UBadge
            v-if="service.target_private"
            color="warning"
            variant="outline"
            icon="i-lucide-lock"
            :label="t('infra.private')"
          />
          <UBadge
            v-if="!service.enabled"
            color="neutral"
            variant="outline"
            :label="t('infra.disabled')"
          />
        </div>
        <dl class="text-muted grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
          <dt>{{ t('infra.columns.site') }}</dt>
          <dd class="text-default">
            {{ service.site_id ? siteName(service.site_id) : t('infra.form.noSite') }}
          </dd>
          <dt>{{ t('infra.drawer.since') }}</dt>
          <dd class="text-default"><RelativeTime :at="service.status.since" /></dd>
          <dt class="sr-only">{{ t('infra.form.interval') }}</dt>
          <dd class="col-span-2">
            {{
              t('infra.drawer.interval', {
                s: service.interval_seconds,
                f: service.failures_before_down,
                r: service.recoveries_before_up,
              })
            }}
          </dd>
        </dl>

        <h3 class="text-highlighted text-sm font-semibold">{{ t('infra.form.checks') }}</h3>
        <ul class="flex flex-col gap-3">
          <li
            v-for="c in service.check_status"
            :key="c.check_id"
            class="ring-default flex flex-col gap-2 rounded-md p-3 ring-1"
            data-testid="infra-drawer-check"
            :data-check-type="c.type"
          >
            <div class="flex flex-wrap items-center justify-between gap-2">
              <span class="text-highlighted font-medium">
                {{
                  checkOf(c.check_id)
                    ? describeCheck(checkOf(c.check_id)!, t)
                    : t(`infra.checkType.${c.type}`)
                }}
              </span>
              <UBadge
                :color="INFRA_STATE_META[c.state].color"
                :icon="INFRA_STATE_META[c.state].icon"
                variant="subtle"
                :label="t(`infra.state.${c.state}`)"
              />
            </div>
            <p class="text-muted flex flex-wrap gap-x-3 gap-y-1 text-xs">
              <span>{{ reasonLabel(c.reason) }}</span>
              <span v-if="c.detail" class="font-mono">{{ c.detail }}</span>
              <span v-if="c.latency_ms != null" class="tabular">{{
                formatLatency(c.latency_ms)
              }}</span>
              <span v-if="c.consecutive_failures > 0">{{
                t('infra.drawer.failures', { n: c.consecutive_failures })
              }}</span>
              <span
                >{{ t('infra.drawer.lastCheck') }} <RelativeTime :at="c.last_checked_at"
              /></span>
            </p>
            <div>
              <p class="text-muted mb-1 text-xs">{{ t('infra.drawer.history') }}</p>
              <p v-if="!c.recent?.length" class="text-dimmed text-xs">
                {{ t('infra.drawer.noHistory') }}
              </p>
              <ol
                v-else
                class="flex flex-row-reverse justify-end gap-1"
                data-testid="infra-history"
              >
                <li
                  v-for="(r, k) in c.recent"
                  :key="k"
                  class="size-3 rounded-sm"
                  :class="dotClass[r.state]"
                  :title="`${d(new Date(r.at), 'long')} · ${t(`infra.state.${r.state}`)}${r.latency_ms != null ? ' · ' + formatLatency(r.latency_ms) : ''}${r.reason && r.state !== 'up' ? ' · ' + reasonLabel(r.reason) : ''}`"
                >
                  <span class="sr-only"
                    >{{ t('infra.drawer.resultAt', { time: d(new Date(r.at), 'long') }) }}:
                    {{ t(`infra.state.${r.state}`) }}</span
                  >
                </li>
              </ol>
            </div>
          </li>
        </ul>
      </div>
    </template>
    <template v-if="service && can('snmp.manage')" #footer>
      <div class="flex w-full flex-wrap justify-end gap-2">
        <UButton
          v-if="service.target_private"
          color="neutral"
          variant="outline"
          icon="i-lucide-terminal"
          :label="t('infra.actions.snippet')"
          @click="$emit('snippet', service)"
        />
        <UButton
          icon="i-lucide-pencil"
          :label="t('infra.form.editTitle')"
          @click="$emit('edit', service)"
        />
      </div>
    </template>
  </USlideover>
</template>
