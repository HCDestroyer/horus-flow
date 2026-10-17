<script setup lang="ts">
import type { CustomerExternalIdentity } from '~~/types/api'

/**
 * Identidad del cliente en ArgosISP (ADR-0030): nombre, código, estado del servicio y plan
 * vigentes, con su fecha de sincronización; "desactualizado" si ArgosISP no respondió (se
 * conserva el último dato). "Actualizar desde ArgosISP" resuelve ahora (customers.update).
 * Las vigencias anteriores explican a quién pertenecía la IP en cada periodo.
 */
const props = defineProps<{ customerId: string }>()
const emit = defineEmits<{ refreshed: [] }>()
const { t, d } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const can = useCan()

const { data, error, refresh } = useTenantQuery(
  () => `customer:${props.customerId}:external`,
  () =>
    unwrap(
      $api.GET('/customers/{customer_id}/external-identity', {
        params: { path: { customer_id: props.customerId } },
      }),
    ),
)
const showHistory = ref(false)
const updating = ref(false)
async function update() {
  updating.value = true
  try {
    data.value = (await unwrap(
      $api.POST('/customers/{customer_id}/external-identity/refresh', {
        params: { path: { customer_id: props.customerId } },
      }),
    )) as CustomerExternalIdentity
    emit('refreshed')
    if (data.value.current?.stale) {
      toast.add({
        title: t('clients.external.staleToast'),
        color: 'warning',
        icon: 'i-lucide-cloud-off',
      })
    }
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  } finally {
    updating.value = false
  }
}
const STATE_COLOR: Record<string, 'success' | 'warning' | 'error' | 'neutral'> = {
  activo: 'success',
  limitado: 'warning',
  suspendido: 'error',
  retirado: 'neutral',
}
</script>

<template>
  <UCard
    v-if="data && (data.connector_enabled || data.current || data.history.length)"
    data-testid="client-external"
  >
    <div class="flex flex-col gap-3">
      <div class="flex flex-wrap items-start justify-between gap-2">
        <div class="min-w-0">
          <h3 class="text-highlighted text-sm font-semibold">{{ t('clients.external.title') }}</h3>
          <p v-if="!data.current" class="text-muted text-sm">
            {{
              data.last_check_status === 'not_found'
                ? t('clients.external.notFound')
                : t('clients.external.none')
            }}
          </p>
        </div>
        <UButton
          v-if="data.connector_enabled && can('customers.update')"
          size="sm"
          color="neutral"
          variant="outline"
          icon="i-lucide-refresh-cw"
          :loading="updating"
          :label="t('clients.external.update')"
          data-testid="client-external-refresh"
          @click="update"
        />
      </div>
      <dl v-if="data.current" class="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2 lg:grid-cols-4">
        <div>
          <dt class="text-muted text-xs">{{ t('clients.external.name') }}</dt>
          <dd class="text-highlighted" data-testid="client-external-name">
            {{ data.current.display_name ?? '—' }}
          </dd>
        </div>
        <div>
          <dt class="text-muted text-xs">{{ t('clients.external.code') }}</dt>
          <dd class="font-mono">{{ data.current.code ?? '—' }}</dd>
        </div>
        <div>
          <dt class="text-muted text-xs">{{ t('clients.external.service') }}</dt>
          <dd class="flex flex-wrap items-center gap-1.5">
            <UBadge
              v-if="data.current.service_state"
              :color="STATE_COLOR[data.current.service_state] ?? 'neutral'"
              variant="subtle"
              :label="data.current.service_state"
            />
            <span v-if="data.current.plan" class="text-muted">{{ data.current.plan }}</span>
            <span v-if="data.current.service_id" class="text-muted font-mono text-xs"
              >#{{ data.current.service_id }}</span
            >
          </dd>
        </div>
        <div>
          <dt class="text-muted text-xs">{{ t('clients.external.synced') }}</dt>
          <dd class="flex flex-wrap items-center gap-1.5">
            <RelativeTime :at="data.current.synced_at" />
            <UBadge
              v-if="data.current.stale"
              color="warning"
              variant="subtle"
              icon="i-lucide-cloud-off"
              :label="t('clients.external.stale')"
              data-testid="client-external-stale"
            />
          </dd>
        </div>
      </dl>
      <div v-if="data.history.length > 1">
        <UButton
          size="xs"
          color="neutral"
          variant="ghost"
          :trailing-icon="showHistory ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
          :label="t('clients.external.history', { n: data.history.length })"
          @click="showHistory = !showHistory"
        />
        <ul v-if="showHistory" class="mt-2 space-y-1 text-sm">
          <li v-for="p in data.history" :key="p.id" class="flex flex-wrap gap-x-2">
            <span class="text-highlighted">{{ p.display_name ?? '—' }}</span>
            <span class="text-muted font-mono text-xs">#{{ p.external_service_id }}</span>
            <span class="text-muted">
              {{ d(new Date(p.valid_from), 'long') }} →
              {{ p.valid_to ? d(new Date(p.valid_to), 'long') : t('clients.external.current') }}
            </span>
          </li>
        </ul>
      </div>
    </div>
  </UCard>
  <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
</template>
