<script setup lang="ts">
import type { WireguardHub, WireguardPool } from '~~/types/api'

/**
 * Rangos de túneles de un hub con su ocupación y el rango de servicios (reservado). Con
 * `platform.wireguard.manage`: añadir rango y quitar uno vacío que no sea el último
 * (`DELETE .../pools/{id}`, re-autenticación reciente). `409 WIREGUARD_POOL_IN_USE`: tiene
 * direcciones asignadas o liberadas hace menos de 24 h (cuarentena).
 */
const props = defineProps<{ hub: WireguardHub; canManage: boolean }>()
const emit = defineEmits<{ add: []; changed: [] }>()

const { t, d } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { withReauth } = useReauth()

const pools = computed<WireguardPool[]>(() =>
  props.hub.pools?.length
    ? props.hub.pools
    : [
        {
          id: '',
          cidr: props.hub.tunnel_cidr,
          addresses_total: props.hub.addresses_total,
          addresses_used: props.hub.addresses_used,
        },
      ],
)

const ratio = (p: WireguardPool) =>
  p.addresses_total > 0 ? p.addresses_used / p.addresses_total : 0

/** Por qué no se puede quitar (o `null` si se puede). */
function blocked(p: WireguardPool): string | null {
  if (!p.id || pools.value.length <= 1) return t('platform.wireguard.pool.lastPool')
  if (p.addresses_used > 0) return t('platform.wireguard.pool.hasAddresses')
  return null
}

const removing = ref<WireguardPool | null>(null)
const removeError = ref<string | null>(null)
/** El error es `WIREGUARD_POOL_IN_USE` (direcciones asignadas o en cuarentena). */
const removeInUse = ref(false)
const busy = ref(false)
const removeOpen = computed({
  get: () => !!removing.value,
  set: (v) => {
    if (!v) removing.value = null
  },
})

function askRemove(p: WireguardPool) {
  removeError.value = null
  removing.value = p
}

async function remove() {
  const p = removing.value
  if (!p) return
  busy.value = true
  removeError.value = null
  removeInUse.value = false
  try {
    await withReauth(
      () =>
        unwrap(
          $api.DELETE('/platform/wireguard/hubs/{hub_id}/pools/{pool_id}', {
            params: { path: { hub_id: props.hub.id, pool_id: p.id } },
          }),
        ),
      t('platform.wireguard.reauth'),
    )
    toast.add({
      title: t('platform.wireguard.pool.removed', { cidr: p.cidr }),
      icon: 'i-lucide-check',
      color: 'success',
    })
    removing.value = null
    emit('changed')
  } catch (e) {
    if (isReauthRequired(e)) {
      removeError.value = t('platform.wireguard.reauthCancelled')
    } else if (e instanceof ApiError && e.code === 'WIREGUARD_POOL_IN_USE') {
      removeError.value = t('platform.wireguard.pool.inUse')
      removeInUse.value = true
      emit('changed')
    } else if (e instanceof ApiError && e.status === 404) {
      removing.value = null
      emit('changed')
    } else {
      removeError.value = t(describeError(e).messageKey)
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section class="flex flex-col gap-3" :aria-labelledby="`wg-pools-${hub.id}`">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h3 :id="`wg-pools-${hub.id}`" class="text-highlighted text-sm font-semibold">
        {{ t('platform.wireguard.pool.title') }}
      </h3>
      <UButton
        v-if="canManage"
        size="sm"
        color="neutral"
        variant="outline"
        icon="i-lucide-plus"
        :label="t('platform.wireguard.pool.add')"
        data-testid="wg-pool-add"
        @click="emit('add')"
      />
    </div>
    <table class="block w-full text-sm md:table" data-testid="wg-pools">
      <thead class="sr-only md:not-sr-only md:table-header-group">
        <tr class="text-muted border-default border-b text-start text-xs">
          <th scope="col" class="py-2 pe-4 text-start font-medium">
            {{ t('platform.wireguard.pool.cidr') }}
          </th>
          <th scope="col" class="py-2 pe-4 text-start font-medium">
            {{ t('platform.wireguard.pool.occupancy') }}
          </th>
          <th scope="col" class="py-2 pe-4 text-start font-medium">
            {{ t('platform.wireguard.pool.createdAt') }}
          </th>
          <th v-if="canManage" scope="col" class="py-2 text-end font-medium">
            <span class="sr-only">{{ t('platform.wireguard.pool.actions') }}</span>
          </th>
        </tr>
      </thead>
      <tbody class="divide-default block divide-y md:table-row-group">
        <tr
          v-for="p in pools"
          :key="p.id || p.cidr"
          class="flex flex-col gap-2 py-3 md:table-row"
          :data-pool="p.cidr"
          data-testid="wg-pool"
        >
          <td class="block py-0 md:table-cell md:py-3 md:pe-4 md:align-middle">
            <span class="text-highlighted font-mono break-all">{{ p.cidr }}</span>
          </td>
          <td class="block py-0 md:table-cell md:w-1/2 md:py-3 md:pe-4 md:align-middle">
            <p class="text-default tabular" data-testid="wg-pool-occupancy">
              {{
                t('platform.wireguard.pool.used', {
                  used: formatNumber(p.addresses_used),
                  total: formatNumber(p.addresses_total),
                  percent: formatPercent(ratio(p), ratio(p) > 0 && ratio(p) < 0.01 ? 2 : 0),
                })
              }}
            </p>
            <UProgress
              :model-value="ratio(p) * 100"
              size="sm"
              :color="ratio(p) > 0.8 ? 'warning' : 'primary'"
              class="mt-1.5"
              :aria-label="t('platform.wireguard.pool.occupancyOf', { cidr: p.cidr })"
            />
          </td>
          <td class="text-muted block py-0 md:table-cell md:py-3 md:pe-4 md:align-middle">
            <span class="md:hidden">{{ t('platform.wireguard.pool.createdAt') }}: </span>
            {{ p.created_at ? d(new Date(p.created_at), 'short') : '—' }}
          </td>
          <td v-if="canManage" class="block py-0 md:table-cell md:py-3 md:text-end md:align-middle">
            <div class="flex flex-col items-start gap-1 md:items-end">
              <UButton
                size="sm"
                color="neutral"
                variant="ghost"
                icon="i-lucide-trash-2"
                :disabled="!!blocked(p)"
                :label="t('platform.wireguard.pool.remove')"
                :aria-label="t('platform.wireguard.pool.removeLabel', { cidr: p.cidr })"
                data-testid="wg-pool-remove"
                @click="askRemove(p)"
              />
              <span v-if="blocked(p)" class="text-dimmed text-xs" data-testid="wg-pool-blocked">
                {{ blocked(p) }}
              </span>
            </div>
          </td>
        </tr>
        <tr
          v-if="hub.services_cidr"
          class="flex flex-col gap-2 py-3 md:table-row"
          data-testid="wg-services"
        >
          <td class="block py-0 md:table-cell md:py-3 md:pe-4 md:align-middle">
            <span class="text-highlighted font-mono break-all">{{ hub.services_cidr }}</span>
          </td>
          <td
            class="text-muted block py-0 md:table-cell md:py-3 md:pe-4 md:align-middle"
            :colspan="canManage ? 3 : 2"
          >
            <UBadge
              color="neutral"
              variant="subtle"
              icon="i-lucide-lock"
              :label="t('platform.wireguard.pool.reserved')"
              class="me-2"
            />
            {{ t('platform.wireguard.pool.servicesNote') }}
          </td>
        </tr>
      </tbody>
    </table>

    <UModal
      v-model:open="removeOpen"
      :title="t('platform.wireguard.pool.removeTitle', { cidr: removing?.cidr ?? '' })"
      :description="t('platform.wireguard.pool.removeBody')"
      data-testid="wg-pool-remove-modal"
    >
      <template v-if="removeError" #body>
        <UAlert
          :color="removeInUse ? 'warning' : 'error'"
          variant="subtle"
          :icon="removeInUse ? 'i-lucide-hourglass' : 'i-lucide-circle-alert'"
          :title="removeInUse ? t('platform.wireguard.pool.inUseTitle') : removeError"
          :description="removeInUse ? removeError : undefined"
          data-testid="wg-pool-remove-error"
        />
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            :label="t('common.cancel')"
            autofocus
            @click="removing = null"
          />
          <UButton
            color="error"
            icon="i-lucide-trash-2"
            :loading="busy"
            :label="t('platform.wireguard.pool.removeSubmit')"
            data-testid="wg-pool-remove-confirm"
            @click="remove"
          />
        </div>
      </template>
    </UModal>
  </section>
</template>
