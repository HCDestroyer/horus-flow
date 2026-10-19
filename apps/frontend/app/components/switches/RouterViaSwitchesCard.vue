<script setup lang="ts">
import type { ManagementSubnet } from '~~/types/api'

/**
 * Ficha del router: switches que Horus alcanza a través de su túnel (ADR-0032) —
 * `GET /routers?device_type=switch&via_router_id=` — con su IP privada y su dirección Horus, y
 * las redes de gestión del sitio declaradas en el router (una regla NAT por red).
 */
const props = defineProps<{ routerId: string }>()
const { t } = useI18n()
const { $api } = useNuxtApp()
const route = useRoute()
const can = useCan()
const slug = computed(() => String(route.params.slug))
const canEdit = computed(() => can('devices.update'))

const switches = useTenantQuery(
  () => `router:${props.routerId}:via-switches`,
  () =>
    unwrap(
      $api.GET('/routers', {
        params: { query: { device_type: 'switch', via_router_id: props.routerId, limit: 200 } },
      }),
    ).then((r) => r.data),
  { watch: [() => props.routerId] },
)
const subnets = useTenantQuery(
  () => `router:${props.routerId}:subnets`,
  () =>
    unwrap(
      $api.GET('/routers/{router_id}/management-subnets', {
        params: { path: { router_id: props.routerId } },
      }),
    ).then((r) => r.data),
  { watch: [() => props.routerId] },
)

const prefix = ref('')
const prefixError = ref<string | null>(null)
const saving = ref(false)
const saveError = shallowRef<unknown>(null)
async function add() {
  prefixError.value = null
  saveError.value = null
  saving.value = true
  try {
    await unwrap(
      $api.POST('/routers/{router_id}/management-subnets', {
        params: { path: { router_id: props.routerId } },
        body: { prefix: prefix.value.trim() },
      }),
    )
    prefix.value = ''
    subnets.refresh()
  } catch (e) {
    if (e instanceof ApiError && e.code === 'VALIDATION_FAILED')
      prefixError.value = t('switches.viaRouter.subnets.invalid')
    else if (e instanceof ApiError && e.code === 'MANAGEMENT_SUBNET_OVERLAP')
      prefixError.value = t('switches.viaRouter.subnets.overlap')
    else saveError.value = e
  } finally {
    saving.value = false
  }
}
async function remove(s: ManagementSubnet) {
  saveError.value = null
  try {
    await unwrap(
      $api.DELETE('/routers/{router_id}/management-subnets/{subnet_id}', {
        params: { path: { router_id: props.routerId, subnet_id: s.id } },
      }),
    )
    subnets.refresh()
  } catch (e) {
    if (e instanceof ApiError && e.code === 'MANAGEMENT_SUBNET_IN_USE')
      prefixError.value = t('switches.viaRouter.subnets.inUse')
    else saveError.value = e
  }
}
</script>

<template>
  <UCard data-testid="router-via-switches">
    <template #header>
      <div class="flex flex-col gap-1">
        <h2 class="text-highlighted font-semibold">{{ t('switches.viaRouter.title') }}</h2>
        <p class="text-muted text-sm">{{ t('switches.viaRouter.subtitle') }}</p>
      </div>
    </template>
    <div class="flex flex-col gap-4">
      <LoadingState v-if="switches.status.value === 'pending' && !switches.data.value" :rows="2" />
      <ErrorState
        v-else-if="switches.error.value"
        :error="switches.error.value"
        @retry="switches.refresh()"
      />
      <p v-else-if="!switches.data.value?.length" class="text-muted text-sm">
        {{ t('switches.viaRouter.empty') }}
      </p>
      <template v-else>
        <ul class="flex flex-col divide-y divide-(--ui-border)">
          <li
            v-for="s in switches.data.value"
            :key="s.id"
            class="flex flex-wrap items-baseline gap-x-4 gap-y-1 py-2 text-sm"
            :data-via-switch="s.name"
          >
            <NuxtLink
              :to="`/t/${slug}/switches/${s.id}`"
              class="text-highlighted font-mono font-semibold hover:underline"
              >{{ s.name }}</NuxtLink
            >
            <span class="text-muted">
              {{ t('switches.viaRouter.columns.private') }}:
              <span class="text-default font-mono">{{ s.management_address ?? '—' }}</span>
            </span>
            <span class="text-muted">
              {{ t('switches.viaRouter.columns.horus') }}:
              <span class="text-default font-mono">{{ s.via_address ?? '—' }}</span>
            </span>
            <UBadge
              :color="s.onboarding_state === 'pending_configuration' ? 'neutral' : 'success'"
              variant="subtle"
              size="sm"
              :icon="
                s.onboarding_state === 'pending_configuration'
                  ? 'i-lucide-circle-dashed'
                  : 'i-lucide-shield-check'
              "
              :label="
                s.onboarding_state === 'pending_configuration'
                  ? t('switches.viaRouter.statePending')
                  : t('switches.viaRouter.stateVerified')
              "
            />
          </li>
        </ul>
        <p class="text-muted flex items-center gap-1.5 text-xs">
          <UIcon name="i-lucide-info" class="size-3.5" aria-hidden="true" />
          {{ t('switches.viaRouter.blocked') }}
        </p>
      </template>

      <USeparator />
      <div class="flex flex-col gap-2" data-testid="router-subnets">
        <h3 class="text-highlighted text-sm font-semibold">
          {{ t('switches.viaRouter.subnets.title') }}
        </h3>
        <p class="text-muted text-sm">{{ t('switches.viaRouter.subnets.help') }}</p>
        <ErrorState
          v-if="subnets.error.value"
          :error="subnets.error.value"
          @retry="subnets.refresh()"
        />
        <p v-else-if="subnets.data.value && !subnets.data.value.length" class="text-muted text-sm">
          {{ t('switches.viaRouter.subnets.empty') }}
        </p>
        <ul v-else class="flex flex-wrap gap-2">
          <li v-for="s in subnets.data.value ?? []" :key="s.id" :data-subnet="s.prefix">
            <UBadge color="neutral" variant="outline" class="font-mono">
              {{ s.prefix }}
              <UButton
                v-if="canEdit"
                color="neutral"
                variant="link"
                size="xs"
                icon="i-lucide-x"
                :aria-label="t('switches.viaRouter.subnets.remove', { prefix: s.prefix })"
                @click="remove(s)"
              />
            </UBadge>
          </li>
        </ul>
        <div v-if="canEdit" class="flex flex-wrap items-start gap-2">
          <UFormField :error="prefixError ?? undefined" class="w-full sm:w-64">
            <UInput
              v-model="prefix"
              :placeholder="'192.168.88.0/24'"
              :aria-label="t('switches.viaRouter.subnets.prefix')"
              class="w-full"
              :ui="{ base: 'font-mono' }"
              autocomplete="off"
              spellcheck="false"
              data-testid="router-subnet-input"
            />
          </UFormField>
          <UButton
            icon="i-lucide-plus"
            color="neutral"
            variant="outline"
            :label="t('switches.viaRouter.subnets.add')"
            :loading="saving"
            :disabled="!prefix.trim()"
            data-testid="router-subnet-add"
            @click="add"
          />
        </div>
        <ErrorState v-if="saveError" :error="saveError" @retry="add" />
      </div>
    </div>
  </UCard>
</template>
