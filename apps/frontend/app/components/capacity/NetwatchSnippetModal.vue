<script setup lang="ts">
import type { NetwatchSnippet } from '~~/types/api'

/**
 * Script RouterOS de sondas netwatch por enlace (`GET /routers/{id}/netwatch-snippet`): el
 * usuario escribe la IP de la puerta de enlace del proveedor de cada enlace (se completa en
 * el navegador; nunca viaja a la API), revisa el script y lo pega en el router. Horus nunca
 * escribe en routers: solo lee las sondas con su usuario de solo lectura. Con un router Huawei
 * (`language: vrp`) son pruebas NQA ICMP que Horus lee por SNMP (docs/vendors/huawei.md §4).
 */
const props = defineProps<{ routerId: string | null }>()
const open = defineModel<boolean>('open', { default: false })

const { t, te } = useI18n()
const { $api } = useNuxtApp()
const result = ref<NetwatchSnippet | null>(null)
const loading = ref(false)
const error = ref<unknown>(null)
const gateways = ref<Record<string, string>>({})

async function load() {
  if (!props.routerId) return
  loading.value = true
  error.value = null
  try {
    result.value = await unwrap(
      $api.GET('/routers/{router_id}/netwatch-snippet', {
        params: { path: { router_id: props.routerId } },
      }),
    )
  } catch (e) {
    error.value = e
  } finally {
    loading.value = false
  }
}
watch(open, (o) => {
  if (!o) return
  result.value = null
  gateways.value = {}
  load()
})

const filled = computed(() =>
  result.value ? fillNetwatchScript(result.value.script, gateways.value) : null,
)
const gatewayError = (key: string) =>
  gateways.value[key] && !isIPv4(gateways.value[key]!) ? t('capacity.snippet.invalidIp') : undefined
const vrp = computed(() => result.value?.language === 'vrp')
const probesError = computed(() => {
  const e = result.value?.probes_error
  if (!e) return null
  return te(`capacity.probesError.${e}`) ? t(`capacity.probesError.${e}`) : e
})
</script>

<template>
  <UModal
    v-model:open="open"
    :title="
      t(vrp ? 'capacity.snippet.titleVrp' : 'capacity.snippet.title', {
        router: result?.router_name ?? '',
      })
    "
    :description="t(vrp ? 'capacity.snippet.descriptionVrp' : 'capacity.snippet.description')"
    :ui="{ content: 'sm:max-w-3xl' }"
  >
    <template #body>
      <div class="flex flex-col gap-4" data-testid="netwatch-snippet">
        <LoadingState v-if="loading && !result" :rows="3" />
        <ErrorState v-else-if="error" :error="error" @retry="load()" />
        <template v-else-if="result">
          <UAlert
            v-if="probesError"
            color="warning"
            variant="subtle"
            icon="i-lucide-triangle-alert"
            :title="t('capacity.snippet.notRead')"
            :description="probesError"
            data-testid="netwatch-probes-error"
          />
          <UAlert
            v-else-if="result.probes_found"
            color="success"
            variant="subtle"
            icon="i-lucide-circle-check"
            :title="t('capacity.snippet.found', { n: result.probes_found })"
          />
          <div v-if="result.placeholders.length" class="grid gap-3 sm:grid-cols-2">
            <UFormField
              v-for="p in result.placeholders"
              :key="p.key"
              :label="t('capacity.snippet.gateway', { iface: p.interface })"
              :help="p.description"
              :error="gatewayError(p.key)"
            >
              <UInput
                v-model="gateways[p.key]"
                class="w-full"
                :ui="{ base: 'font-mono' }"
                inputmode="decimal"
                autocomplete="off"
                placeholder="198.51.100.1"
                :data-testid="`netwatch-gateway-${p.interface}`"
              />
            </UFormField>
          </div>
          <UAlert
            v-if="filled && filled.missing.length"
            color="neutral"
            variant="subtle"
            icon="i-lucide-info"
            :title="t('capacity.snippet.missing', { n: filled.missing.length })"
          />
          <CodeBlock
            v-if="filled"
            :code="filled.script"
            :label="t(vrp ? 'capacity.snippet.scriptLabelVrp' : 'capacity.snippet.scriptLabel')"
            :filename="
              vrp
                ? `horus-nqa-${routerId?.slice(0, 8)}.txt`
                : `horus-netwatch-${routerId?.slice(0, 8)}.rsc`
            "
            max-height="18rem"
            data-testid="netwatch-script"
          />
          <UAlert
            v-if="result.warnings.length"
            color="warning"
            variant="subtle"
            icon="i-lucide-triangle-alert"
            :title="t('capacity.snippet.warnings')"
          >
            <template #description>
              <ul class="list-disc ps-4">
                <li v-for="w in result.warnings" :key="w">{{ w }}</li>
              </ul>
            </template>
          </UAlert>
          <p class="text-muted text-sm">{{ t('capacity.snippet.readOnly') }}</p>
        </template>
      </div>
    </template>
  </UModal>
</template>
