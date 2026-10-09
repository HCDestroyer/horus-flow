<script setup lang="ts">
/**
 * Estado del sistema (I1-31, frontend.md §3.4, §9.4): capacidades y componentes con su
 * nombre técnico (usuario de plataforma), aviso "Sin copia remota configurada" y acceso a la
 * instalación (D19): modo dominio / subdominio / solo IP, con su aviso y la huella del
 * certificado para verificarla a mano.
 */
const { t, te, d } = useI18n()
const { $api } = useNuxtApp()
useSectionGuard()(findSection('platform', '/platform/system'))

const {
  data: system,
  error,
  status,
  refresh,
} = useTenantQuery('platform:system', () => unwrap($api.GET('/system/status')))
const { data: access, error: accessError } = useTenantQuery('platform:installation', () =>
  unwrap($api.GET('/platform/installation')),
)
const noRemote = computed(
  () =>
    system.value?.components.find((c) => c.name === 'remote_storage')?.status === 'not_configured',
)
const COMPONENT_STATE = {
  up: 'ok',
  degraded: 'degraded',
  down: 'unavailable',
  not_configured: 'stale',
} as const
const capabilities = computed(() => Object.entries(system.value?.capabilities ?? {}))
const cap = (k: string) => (te(`overview.capabilities.${k}`) ? t(`overview.capabilities.${k}`) : k)
const modeIcon = {
  domain: 'i-lucide-globe',
  subdomain: 'i-lucide-globe-lock',
  ip_only: 'i-lucide-network',
} as const
</script>

<template>
  <AppPage :title="t('nav.items.platformSystem')" panel-id="platform-system">
    <LoadingState v-if="status === 'pending' && !system" :rows="4" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <template v-else-if="system">
      <DiskUsageNotice :ratio="system.disk_usage_ratio" />
      <NoRemoteCopyNotice v-if="noRemote" />

      <UCard data-testid="installation-access">
        <h2 class="text-highlighted text-base font-semibold">{{ t('platform.access.title') }}</h2>
        <ErrorState v-if="accessError" :error="accessError" />
        <div v-else-if="access" class="mt-3 flex flex-col gap-4">
          <p class="flex flex-wrap items-center gap-2">
            <UIcon
              :name="modeIcon[access.access_mode]"
              class="text-muted size-5"
              aria-hidden="true"
            />
            <span class="text-highlighted font-medium" data-testid="access-mode">{{
              t(`platform.access.mode.${access.access_mode}`)
            }}</span>
            <span class="text-muted font-mono text-sm break-all">{{ access.public_base_url }}</span>
          </p>
          <UAlert
            v-for="w in access.warnings"
            :key="w.code"
            :color="
              w.severity === 'critical' ? 'error' : w.severity === 'warning' ? 'warning' : 'info'
            "
            variant="subtle"
            :icon="w.severity === 'info' ? 'i-lucide-info' : 'i-lucide-triangle-alert'"
            :title="
              te(`platform.access.warnings.${w.code}`)
                ? t(`platform.access.warnings.${w.code}`)
                : w.message
            "
            :description="w.message"
            data-testid="access-warning"
          />
          <dl class="grid grid-cols-1 gap-x-4 gap-y-3 sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <dt class="text-muted text-xs">{{ t('platform.access.tls') }}</dt>
              <dd class="text-highlighted text-sm">
                {{ t(`platform.access.tlsMode.${access.tls.mode}`) }}
              </dd>
            </div>
            <div>
              <dt class="text-muted text-xs">{{ t('platform.access.issuer') }}</dt>
              <dd class="text-highlighted text-sm">{{ access.tls.issuer ?? '—' }}</dd>
            </div>
            <div>
              <dt class="text-muted text-xs">{{ t('platform.access.expires') }}</dt>
              <dd class="text-highlighted text-sm">
                {{ access.tls.not_after ? d(new Date(access.tls.not_after), 'short') : '—' }}
              </dd>
            </div>
            <div>
              <dt class="text-muted text-xs">{{ t('platform.access.hsts') }}</dt>
              <dd class="text-highlighted text-sm">
                {{ access.tls.hsts ? t('common.yes') : t('common.no') }}
              </dd>
            </div>
            <div v-if="access.tls.fingerprint_sha256" class="sm:col-span-2 lg:col-span-4">
              <dt class="text-muted text-xs">{{ t('platform.access.fingerprint') }}</dt>
              <dd class="flex flex-wrap items-center gap-2">
                <span
                  class="text-highlighted font-mono text-sm break-all"
                  data-testid="cert-fingerprint"
                  >{{ access.tls.fingerprint_sha256 }}</span
                >
                <CopyButton :value="access.tls.fingerprint_sha256" size="xs" variant="ghost" />
              </dd>
              <dd class="text-muted mt-1 text-xs">{{ t('platform.access.fingerprintHint') }}</dd>
            </div>
            <div class="sm:col-span-2">
              <dt class="text-muted text-xs">{{ t('platform.access.wgEndpoint') }}</dt>
              <dd class="text-highlighted font-mono text-sm break-all">
                {{ access.wireguard_endpoint }}
              </dd>
            </div>
          </dl>
        </div>
      </UCard>

      <UCard>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h2 class="text-highlighted text-base font-semibold">
            {{ t('platform.system.capabilities') }}
          </h2>
          <span class="text-muted text-sm"
            >{{ t('platform.system.checked') }} <RelativeTime :at="system.checked_at"
          /></span>
        </div>
        <ul class="mt-3 grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
          <li
            v-for="[key, state] in capabilities"
            :key="key"
            class="flex items-center justify-between gap-2 text-sm"
          >
            <span class="text-default">{{ cap(key) }}</span>
            <CapabilityBadge :state="state" />
          </li>
        </ul>
      </UCard>

      <UCard data-testid="system-components">
        <h2 class="text-highlighted text-base font-semibold">
          {{ t('platform.system.components') }}
        </h2>
        <ul class="mt-3 flex flex-col divide-y divide-(--ui-border)">
          <li
            v-for="c in system.components"
            :key="c.name"
            class="flex flex-col gap-1 py-2 sm:flex-row sm:items-center sm:justify-between"
          >
            <span class="min-w-0">
              <span class="text-highlighted font-mono text-sm">{{ c.name }}</span>
              <span v-if="c.detail" class="text-muted block text-xs">{{ c.detail }}</span>
            </span>
            <span class="flex items-center gap-2">
              <span v-if="c.since" class="text-muted text-xs"
                ><RelativeTime :at="c.since" :prefix="t('router.since')"
              /></span>
              <CapabilityBadge
                v-if="c.status !== 'not_configured'"
                :state="COMPONENT_STATE[c.status]"
              />
              <UBadge
                v-else
                color="warning"
                variant="subtle"
                icon="i-lucide-circle-dashed"
                :label="t('platform.system.notConfigured')"
              />
            </span>
          </li>
        </ul>
      </UCard>
    </template>
  </AppPage>
</template>
