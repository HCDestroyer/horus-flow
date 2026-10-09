<script setup lang="ts">
import type { CapabilityState } from '~~/types/api'

/**
 * Resumen del ISP (destino tras el login, I0-14). En I1 lo sustituye el dashboard
 * "NOC del ISP" (frontend.md §6.5). Muestra los cuatro estados: carga, vacío, error y datos.
 */
const { t, d } = useI18n()
const { me } = useAuth()
const { membership } = useTenant()
const navigation = useNavigation()
const { $api } = useNuxtApp()

const {
  data: status,
  error,
  status: fetchStatus,
  refresh,
} = useAsyncData('system-status', () => unwrap($api.GET('/system/status')), {
  server: false,
})

const capabilities = computed(() =>
  status.value
    ? Object.entries(status.value.capabilities).filter(
        (entry): entry is [string, CapabilityState] => !!entry[1],
      )
    : [],
)

const summary = computed(() => {
  if (!status.value) return null
  if (status.value.status === 'ok') {
    return { text: t('overview.status.all_ok'), icon: 'i-lucide-circle-check', cls: 'text-success' }
  }
  if (status.value.status === 'degraded') {
    return {
      text: t('overview.status.degraded'),
      icon: 'i-lucide-triangle-alert',
      cls: 'text-warning',
    }
  }
  return { text: t('overview.status.down'), icon: 'i-lucide-circle-x', cls: 'text-error' }
})

const nodesSection = computed(() =>
  navigation.value.flatMap((g) => g.sections).find((s) => s.id === 'nodes'),
)
const visibleSections = computed(() =>
  navigation.value.flatMap((g) => g.sections).filter((s) => s.id !== 'overview'),
)
const firstName = computed(() => me.value?.display_name.split(' ')[0] ?? '')
</script>

<template>
  <AppPage :title="t('overview.title')" panel-id="overview">
    <section aria-labelledby="overview-heading" class="space-y-1">
      <h2 id="overview-heading" class="text-highlighted text-2xl font-semibold">
        {{ t('overview.greeting', { name: firstName }) }}
      </h2>
      <p class="text-muted">
        {{ t('overview.intro', { tenant: membership?.tenant_name ?? '' }) }}
      </p>
    </section>

    <div class="grid items-start gap-6 lg:grid-cols-5">
      <UCard
        as="section"
        aria-labelledby="status-heading"
        class="lg:col-span-3"
        data-testid="system-status"
      >
        <template #header>
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div>
              <h2 id="status-heading" class="text-highlighted font-semibold">
                {{ t('overview.status.title') }}
              </h2>
              <p class="text-muted text-sm">{{ t('overview.status.description') }}</p>
            </div>
            <p v-if="status" class="text-muted text-xs tabular" data-numeric>
              <time :datetime="status.checked_at" :title="d(new Date(status.checked_at), 'long')">
                {{
                  t('overview.status.checkedAt', {
                    time: d(new Date(status.checked_at), 'time'),
                  })
                }}
              </time>
            </p>
          </div>
        </template>

        <LoadingState v-if="fetchStatus === 'pending' && !status" :rows="5" />
        <ErrorState
          v-else-if="error"
          :error="error"
          :title="t('overview.status.error')"
          :hint="t('overview.status.errorHint')"
          @retry="refresh()"
        />
        <div v-else-if="status" class="space-y-4">
          <p v-if="summary" class="flex items-center gap-2 font-medium" :class="summary.cls">
            <UIcon :name="summary.icon" class="size-5 shrink-0" aria-hidden="true" />
            <span>{{ summary.text }}</span>
          </p>
          <ul class="divide-default divide-y" role="list">
            <li
              v-for="[name, state] in capabilities"
              :key="name"
              class="flex items-center justify-between gap-3 py-2.5"
            >
              <span class="text-default min-w-0 text-sm">
                {{ t(`overview.capabilities.${name}`) }}
              </span>
              <CapabilityBadge :state="state" />
            </li>
          </ul>
        </div>
      </UCard>

      <div class="space-y-6 lg:col-span-2">
        <UCard as="section" aria-labelledby="next-heading" data-testid="next-steps">
          <h2 id="next-heading" class="sr-only">{{ t('overview.next.title') }}</h2>
          <EmptyState
            icon="i-lucide-router"
            :title="t('overview.next.title')"
            :description="t('overview.next.description')"
            :actions="
              nodesSection
                ? [
                    {
                      label: t('overview.next.action'),
                      to: nodesSection.href,
                      trailingIcon: 'i-lucide-arrow-right',
                    },
                  ]
                : []
            "
          />
        </UCard>

        <UCard as="section" aria-labelledby="access-heading">
          <template #header>
            <h2 id="access-heading" class="text-highlighted font-semibold">
              {{ t('overview.access.title', { tenant: membership?.tenant_name ?? '' }) }}
            </h2>
          </template>
          <dl class="space-y-3 text-sm">
            <div>
              <dt class="text-muted">{{ t('overview.access.roles') }}</dt>
              <dd class="mt-1 flex flex-wrap gap-1.5">
                <UBadge
                  v-for="role in membership?.roles ?? []"
                  :key="role.role_id + role.scope"
                  color="neutral"
                  variant="subtle"
                  class="font-mono"
                  :label="role.role_key ?? role.role_id"
                />
              </dd>
            </div>
            <div>
              <dt class="text-muted">{{ t('overview.access.sections') }}</dt>
              <dd class="text-default mt-1">
                {{ visibleSections.map((s) => t(s.labelKey)).join(' · ') }}
              </dd>
            </div>
          </dl>
        </UCard>
      </div>
    </div>
  </AppPage>
</template>
