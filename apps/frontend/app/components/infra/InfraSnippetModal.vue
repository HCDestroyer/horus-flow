<script setup lang="ts">
import type { InfraRouterSnippet, InfraService, Router } from '~~/types/api'

/**
 * Script RouterOS para alcanzar un servidor privado por el túnel: el usuario elige el router
 * del nodo, Horus genera las reglas (reenvío de puertos limitado a la red de servicios de
 * Horus) y el usuario las revisa y las pega. Horus nunca escribe en routers.
 */
const props = defineProps<{ service: InfraService | null }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { $api } = useNuxtApp()
const routers = ref<Router[]>([])
const routerId = ref('')
const result = ref<InfraRouterSnippet | null>(null)
const loading = ref(false)
const error = ref<unknown>(null)

watch(open, async (o) => {
  if (!o || !props.service) return
  result.value = null
  error.value = null
  routerId.value = props.service.probe_via_router_id ?? ''
  try {
    routers.value = (await unwrap($api.GET('/routers', { params: { query: { limit: 200 } } }))).data
  } catch {
    routers.value = []
  }
  if (!routerId.value) {
    routerId.value =
      routers.value.find((r) => r.site_id === props.service?.site_id && r.is_primary)?.id ??
      routers.value.find((r) => r.site_id === props.service?.site_id)?.id ??
      ''
  }
  if (routerId.value) generate()
})

const routerItems = computed(() => routers.value.map((r) => ({ label: r.name, value: r.id })))

async function generate() {
  if (!props.service || !routerId.value) return
  loading.value = true
  error.value = null
  try {
    result.value = await unwrap(
      $api.GET('/infra-services/{service_id}/router-snippet', {
        params: { path: { service_id: props.service.id }, query: { router_id: routerId.value } },
      }),
    )
  } catch (e) {
    error.value = e
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <UModal
    v-model:open="open"
    :title="t('infra.snippet.title')"
    :description="t('infra.snippet.description')"
    :ui="{ content: 'sm:max-w-3xl' }"
  >
    <template #body>
      <div class="flex flex-col gap-4" data-testid="infra-snippet">
        <div class="flex flex-wrap items-end gap-2">
          <UFormField
            :label="t('infra.snippet.router')"
            :help="t('infra.snippet.pickRouter')"
            class="min-w-56 flex-1"
          >
            <USelect
              v-model="routerId"
              :items="routerItems"
              class="w-full"
              data-testid="infra-snippet-router"
            />
          </UFormField>
          <UButton
            icon="i-lucide-wand-sparkles"
            :label="t('infra.snippet.generate')"
            :loading="loading"
            :disabled="!routerId"
            @click="generate"
          />
        </div>
        <ErrorState v-if="error" :error="error" @retry="generate()" />
        <template v-else-if="result">
          <CodeBlock
            v-if="result.applicable"
            :code="result.script"
            :label="t('infra.snippet.scriptLabel')"
            :filename="`horus-infra-${service?.id.slice(0, 8)}.rsc`"
            max-height="18rem"
            data-testid="infra-snippet-script"
          />
          <UAlert
            v-else
            color="neutral"
            variant="subtle"
            icon="i-lucide-info"
            :title="t('infra.snippet.notApplicable')"
            :description="t('infra.snippet.notApplicableHint')"
            data-testid="infra-snippet-na"
          />
          <UAlert
            v-if="result.warnings.length"
            color="warning"
            variant="subtle"
            icon="i-lucide-triangle-alert"
            :title="t('infra.snippet.warnings')"
          >
            <template #description>
              <ul class="list-disc ps-4">
                <li v-for="w in result.warnings" :key="w">{{ w }}</li>
              </ul>
            </template>
          </UAlert>
          <div>
            <h3 class="text-highlighted mb-1 text-sm font-semibold">
              {{ t('infra.snippet.horusSide') }}
            </h3>
            <p class="text-muted text-sm" data-testid="infra-snippet-horus">
              {{ result.horus_side }}
            </p>
          </div>
        </template>
      </div>
    </template>
  </UModal>
</template>
