<script setup lang="ts">
import type { IpLookupResponse } from '~~/types/api'

/**
 * Consulta 360 de una IP de cliente (POST /integrations/ip-lookup, ADR-0030): identidad en
 * ArgosISP, consumo, salud del servicio y seguridad. La IP vive solo en el estado de la página
 * y en el cuerpo de la petición: nunca en la URL ni en el historial.
 */
const props = defineProps<{ address?: string | null }>()
const open = defineModel<boolean>('open', { default: false })
const { t, d } = useI18n()
const { $api } = useNuxtApp()
const route = useRoute()
const slug = computed(() => String(route.params.slug))

const ip = ref('')
const result = ref<IpLookupResponse | null>(null)
const loading = ref(false)
const errorText = ref('')
watch(open, (o) => {
  if (!o) return
  result.value = null
  errorText.value = ''
  ip.value = props.address ?? ''
  if (ip.value) lookup()
})

async function lookup() {
  errorText.value = ''
  result.value = null
  loading.value = true
  try {
    result.value = await unwrap(
      $api.POST('/integrations/ip-lookup', { body: { address: ip.value.trim() } }),
    )
  } catch (e) {
    errorText.value =
      e instanceof ApiError && e.status === 404
        ? t('clients.ip360.notFound')
        : e instanceof ApiError && e.problem.errors?.length
          ? e.problem.errors[0]!.message
          : t(describeError(e).messageKey)
  } finally {
    loading.value = false
  }
}
const bytes = (n: number) => joinUnit(formatBytes(n))
function omittedReason(section: string) {
  const o = result.value?.meta.omitted.find((x) => x.section === section)
  return o ? t(`clients.ip360.omitted.${o.reason}`) : t('clients.ip360.omitted.unavailable')
}
</script>

<template>
  <UModal
    v-model:open="open"
    :title="t('clients.ip360.title')"
    :description="t('clients.ip360.description')"
  >
    <template #body>
      <div class="flex flex-col gap-4" data-testid="ip360">
        <form class="flex flex-wrap items-end gap-2" @submit.prevent="lookup">
          <UFormField :label="t('clients.ip360.address')" class="min-w-60 flex-1">
            <UInput
              v-model="ip"
              class="w-full"
              :ui="{ base: 'font-mono' }"
              autocomplete="off"
              placeholder="100.64.1.20"
              data-testid="ip360-address"
            />
          </UFormField>
          <UButton
            type="submit"
            icon="i-lucide-search"
            :loading="loading"
            :disabled="!ip.trim()"
            :label="t('clients.ip360.search')"
            data-testid="ip360-search"
          />
        </form>
        <UAlert
          v-if="errorText"
          color="neutral"
          variant="subtle"
          :title="errorText"
          data-testid="ip360-error"
        />
        <template v-if="result">
          <section class="flex flex-col gap-1" data-testid="ip360-customer">
            <h3 class="text-highlighted text-sm font-semibold">
              {{ t('clients.ip360.customer') }}
            </h3>
            <p class="text-sm">
              <NuxtLink
                :to="`/t/${slug}/clients/${result.customer.id}`"
                class="font-mono hover:underline"
              >
                {{ result.customer.address }}
              </NuxtLink>
              <span v-if="result.customer.alias"> · “{{ result.customer.alias }}”</span>
              <span class="text-muted"> · {{ result.customer.site.name ?? '—' }}</span>
            </p>
            <p class="text-muted text-xs">
              {{ t(`clients.kind.${result.customer.kind}`) }} ·
              {{ t(`clients.status.${result.customer.status}`) }}
            </p>
          </section>
          <section class="flex flex-col gap-1" data-testid="ip360-external">
            <h3 class="text-highlighted text-sm font-semibold">
              {{ t('clients.external.title') }}
            </h3>
            <p v-if="result.external" class="text-sm">
              {{ result.external.display_name ?? '—' }}
              <span class="text-muted font-mono text-xs">{{ result.external.code }}</span>
              · {{ result.external.service_state ?? '—' }}
              <UBadge
                v-if="result.external.stale"
                color="warning"
                variant="subtle"
                size="sm"
                :label="t('clients.external.stale')"
              />
            </p>
            <p v-else class="text-muted text-sm">{{ omittedReason('external') }}</p>
          </section>
          <section class="flex flex-col gap-1" data-testid="ip360-consumption">
            <h3 class="text-highlighted text-sm font-semibold">
              {{ t('clients.ip360.consumption') }}
            </h3>
            <dl v-if="result.consumption" class="grid grid-cols-3 gap-2 text-sm">
              <div v-for="k in ['last_24h', 'last_7d', 'last_30d'] as const" :key="k">
                <dt class="text-muted text-xs">{{ t(`clients.ip360.${k}`) }}</dt>
                <dd>
                  ↓ {{ bytes(result.consumption[k].down_bytes) }}<br />↑
                  {{ bytes(result.consumption[k].up_bytes) }}
                </dd>
              </div>
            </dl>
            <p v-if="result.consumption?.top_services_7d.length" class="text-muted text-xs">
              {{ t('clients.ip360.topServices') }}:
              {{
                result.consumption.top_services_7d
                  .map((s) => `${s.name} (${bytes(s.bytes)})`)
                  .join(', ')
              }}
            </p>
            <p v-if="!result.consumption" class="text-muted text-sm">
              {{ omittedReason('consumption') }}
            </p>
          </section>
          <section class="flex flex-col gap-1" data-testid="ip360-health">
            <h3 class="text-highlighted text-sm font-semibold">{{ t('clients.ip360.health') }}</h3>
            <p v-if="result.health" class="text-sm">
              <template v-if="result.health.router">
                {{ result.health.router.name }} ·
                {{ t(`routerStatus.${result.health.router.status}`, result.health.router.status) }}
              </template>
              <template v-else>{{ t('clients.ip360.noRouter') }}</template>
              <span v-if="result.health.exporter.status" class="text-muted">
                ·
                {{
                  t(`exporterState.${result.health.exporter.status}`, result.health.exporter.status)
                }}</span
              >
              <span class="text-muted">
                · {{ t('clients.ip360.lastSeen', { n: result.health.last_seen_minutes }) }}</span
              >
            </p>
            <p v-else class="text-muted text-sm">{{ omittedReason('health') }}</p>
          </section>
          <section class="flex flex-col gap-1" data-testid="ip360-security">
            <h3 class="text-highlighted text-sm font-semibold">
              {{ t('clients.ip360.security') }}
            </h3>
            <template v-if="result.security">
              <div class="flex flex-wrap items-center gap-2 text-sm">
                <SecurityStateBadge :state="result.security.state" />
                <span class="text-muted">{{
                  t('clients.findingsCount', { n: result.security.open_findings })
                }}</span>
              </div>
              <ul v-if="result.security.findings.length" class="space-y-1 text-sm">
                <li v-for="f in result.security.findings" :key="f.id">
                  <NuxtLink :to="`/t/${slug}/security/findings/${f.id}`" class="hover:underline">
                    {{ f.summary }}
                  </NuxtLink>
                  <span class="text-muted text-xs">
                    · {{ t(`severity.${f.severity}`, f.severity) }} ·
                    {{ d(new Date(f.opened_at), 'short') }}</span
                  >
                </li>
              </ul>
            </template>
            <p v-else class="text-muted text-sm">{{ omittedReason('security') }}</p>
          </section>
        </template>
      </div>
    </template>
  </UModal>
</template>
