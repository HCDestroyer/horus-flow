<script setup lang="ts">
import type { WireguardHub, WireguardPool } from '~~/types/api'
import { CGNAT_CIDR, type PoolCidrCheck } from '~/utils/cidr'

/**
 * Añadir un rango de túneles al hub (`POST /platform/wireguard/hubs/{id}/pools`, re-autenticación
 * reciente). Solo se añaden rangos: la dirección de túnel de cada router es su identidad y no
 * se mueve. Se comprueba antes de enviar lo mismo que el backend (`utils/cidr.ts`) y se
 * sugiere un /16 privado libre.
 */
const props = defineProps<{ hub: WireguardHub }>()
const emit = defineEmits<{ added: [pool: WireguardPool] }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { withReauth } = useReauth()

const cidr = ref('')
const error = ref<string | null>(null)
const generalError = ref<string | null>(null)
const saving = ref(false)

const poolCidrs = computed(() =>
  (props.hub.pools?.length ? props.hub.pools.map((p) => p.cidr) : [props.hub.tunnel_cidr]).filter(
    Boolean,
  ),
)
const suggestion = computed(() => suggestPoolCidr(poolCidrs.value, props.hub.services_cidr))

watch(
  open,
  (o) => {
    if (!o) return
    cidr.value = ''
    error.value = null
    generalError.value = null
  },
  { immediate: true },
)

function message(check: Exclude<PoolCidrCheck, { ok: true }>) {
  switch (check.error) {
    case 'required':
    case 'syntax':
    case 'ipv6':
      return t(`platform.wireguard.pool.errors.${check.error}`, {
        example: suggestion.value ?? '10.253.0.0/16',
      })
    case 'prefix':
      return t(`platform.wireguard.pool.errors.prefix${check.family === 6 ? 'V6' : 'V4'}`)
    default:
      return t(`platform.wireguard.pool.errors.${check.error}`, { cidr: check.detail ?? '' })
  }
}

function validate() {
  const check = checkPoolCidr(cidr.value, {
    pools: poolCidrs.value,
    services: props.hub.services_cidr,
  })
  error.value = check.ok ? null : message(check)
  return check.ok ? check.cidr : null
}

function useSuggestion() {
  if (!suggestion.value) return
  cidr.value = suggestion.value
  error.value = null
}

async function save() {
  generalError.value = null
  const value = validate()
  if (!value) return
  saving.value = true
  try {
    const pool = await withReauth(
      () =>
        unwrap(
          $api.POST('/platform/wireguard/hubs/{hub_id}/pools', {
            params: { path: { hub_id: props.hub.id } },
            body: { cidr: value },
          }),
        ),
      t('platform.wireguard.reauth'),
    )
    toast.add({
      title: t('platform.wireguard.pool.added', { cidr: pool.cidr }),
      icon: 'i-lucide-check',
      color: 'success',
    })
    open.value = false
    emit('added', pool)
  } catch (e) {
    if (isReauthRequired(e)) {
      generalError.value = t('platform.wireguard.reauthCancelled')
    } else if (e instanceof ApiError && e.code === 'WIREGUARD_POOL_OVERLAP') {
      error.value = t('platform.wireguard.pool.errors.overlapServer')
    } else if (e instanceof ApiError && (e.code === 'WIREGUARD_POOL_INVALID' || e.status === 422)) {
      error.value = fieldErrors(e.problem).cidr ?? t('platform.wireguard.pool.errors.invalidServer')
    } else {
      generalError.value = t(describeError(e).messageKey)
    }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <UModal
    v-model:open="open"
    :title="t('platform.wireguard.pool.addTitle', { name: hub.name })"
    :description="t('platform.wireguard.pool.addDescription')"
    data-testid="wg-pool-modal"
  >
    <template #body>
      <form id="wg-pool-form" class="flex flex-col gap-4" novalidate @submit.prevent="save">
        <UAlert
          color="info"
          variant="subtle"
          icon="i-lucide-info"
          :title="t('platform.wireguard.pool.onlyAddTitle')"
          data-testid="wg-pool-explain"
        >
          <template #description>
            <ul class="mt-1 flex list-disc flex-col gap-1 ps-4">
              <li>{{ t('platform.wireguard.pool.onlyAddIdentity') }}</li>
              <li>{{ t('platform.wireguard.pool.onlyAddCollide') }}</li>
              <li>{{ t('platform.wireguard.pool.onlyAddRules') }}</li>
            </ul>
          </template>
        </UAlert>
        <UFormField
          :label="t('platform.wireguard.pool.cidr')"
          :help="
            suggestion
              ? t('platform.wireguard.pool.cidrHelp', { suggestion })
              : t('platform.wireguard.pool.cidrHelpNoSuggestion')
          "
          :error="error ?? undefined"
          required
        >
          <UInput
            v-model="cidr"
            class="w-full"
            autocomplete="off"
            spellcheck="false"
            autofocus
            :placeholder="suggestion ?? '10.253.0.0/16'"
            :ui="{ base: 'font-mono' }"
            data-testid="wg-pool-cidr"
            @blur="cidr.trim() && validate()"
          />
        </UFormField>
        <div class="flex flex-wrap items-center gap-2 text-sm">
          <UButton
            v-if="suggestion && cidr.trim() !== suggestion"
            color="neutral"
            variant="outline"
            size="sm"
            icon="i-lucide-wand-sparkles"
            :label="t('platform.wireguard.pool.useSuggestion', { suggestion })"
            data-testid="wg-pool-suggestion"
            @click="useSuggestion"
          />
        </div>
        <p class="text-muted text-sm">
          {{ t('platform.wireguard.pool.reservedNote') }}
          <span class="text-highlighted font-mono">{{ CGNAT_CIDR }}</span>
          <template v-if="hub.services_cidr">
            · <span class="text-highlighted font-mono">{{ hub.services_cidr }}</span>
          </template>
        </p>
        <UAlert
          v-if="generalError"
          color="error"
          variant="subtle"
          icon="i-lucide-circle-alert"
          :title="generalError"
          data-testid="wg-pool-error"
        />
      </form>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton
          color="neutral"
          variant="ghost"
          :label="t('common.cancel')"
          @click="open = false"
        />
        <UButton
          type="submit"
          form="wg-pool-form"
          icon="i-lucide-plus"
          :loading="saving"
          :label="t('platform.wireguard.pool.addSubmit')"
          data-testid="wg-pool-submit"
        />
      </div>
    </template>
  </UModal>
</template>
