<script setup lang="ts">
import type { ArgosConnectionLink } from '~~/types/api'

/**
 * Enlace de conexión con ArgosISP (contrato docs/integrations/argosisp.md §1.4, H10), en vez de
 * copiar a mano URL, token y secreto en los dos sistemas:
 * - "Generar enlace de conexión": un uso, 15 min, mostrado UNA vez con su cuenta atrás; se pega
 *   en ArgosISP. Al canjearlo, Horus emite el código hf_pat_ y guarda el token y el webhook de
 *   Argos; el conector queda apagado hasta activarlo abajo.
 * - "Canjear enlace de Argos": pegar el enlace generado en ArgosISP; Horus lo canjea de servidor
 *   a servidor.
 * Ambos exigen re-autenticación y los permisos settings.manage, api_tokens.manage y alerts.manage.
 */
const emit = defineEmits<{ connected: [] }>()
const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { withReauth } = useReauth()
const can = useCan()
const now = useNow()

const canLink = computed(
  () => can('settings.manage') && can('api_tokens.manage') && can('alerts.manage'),
)

const link = ref<ArgosConnectionLink | null>(null)
const generating = ref(false)
const generateError = ref('')
const remaining = computed(() =>
  link.value ? Math.max(0, Date.parse(link.value.expires_at) - now.value) : 0,
)
const expired = computed(() => !!link.value && remaining.value <= 0)
const countdown = computed(() => {
  const s = Math.ceil(remaining.value / 1000)
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
})
// Al caducar, el enlace deja de mostrarse (no sirve y no debe quedar a la vista).
watch(expired, (v) => {
  if (v) link.value = { ...link.value!, url: '' }
})

async function generate() {
  generating.value = true
  generateError.value = ''
  try {
    link.value = await withReauth(
      () => unwrap($api.POST('/integrations/argosisp/connection-links')),
      t('integrations.link.reauth'),
    )
  } catch (e) {
    generateError.value = t(describeError(e).messageKey)
  } finally {
    generating.value = false
  }
}

const peerLink = ref('')
const redeeming = ref(false)
const redeemError = ref('')
const redeemed = ref(false)
const looksOk = computed(() => !peerLink.value.trim() || looksLikeArgosLink(peerLink.value))

async function redeem() {
  redeeming.value = true
  redeemError.value = ''
  redeemed.value = false
  try {
    const value = peerLink.value.trim()
    await withReauth(
      () =>
        unwrap(
          $api.POST('/integrations/argosisp/connection-links/redeem', { body: { link: value } }),
        ),
      t('integrations.link.reauth'),
    )
    peerLink.value = ''
    redeemed.value = true
    toast.add({ title: t('integrations.link.redeemed'), icon: 'i-lucide-check', color: 'success' })
    emit('connected')
  } catch (e) {
    if (e instanceof ApiError && e.code === 'CONNECTION_LINK_EXPIRED') {
      redeemError.value = t('integrations.link.peerExpired')
    } else if (e instanceof ApiError && e.problem.errors?.length) {
      redeemError.value = e.problem.errors[0]!.message
    } else if (e instanceof ApiError && e.problem.detail) {
      redeemError.value = e.problem.detail
    } else {
      redeemError.value = t(describeError(e).messageKey)
    }
  } finally {
    redeeming.value = false
  }
}
</script>

<template>
  <section class="flex flex-col gap-3" data-testid="argos-link">
    <div>
      <h2 class="text-highlighted font-semibold">{{ t('integrations.link.title') }}</h2>
      <p class="text-muted text-sm">{{ t('integrations.link.body') }}</p>
    </div>
    <UAlert
      v-if="!canLink"
      color="neutral"
      variant="subtle"
      icon="i-lucide-lock"
      :title="t('integrations.link.noPermission')"
    />
    <div v-else class="grid grid-cols-1 gap-3 lg:grid-cols-2">
      <UCard :ui="{ body: 'p-4 sm:p-4' }">
        <div class="flex flex-col gap-3">
          <h3 class="text-highlighted text-sm font-medium">
            {{ t('integrations.link.generateTitle') }}
          </h3>
          <p class="text-muted text-sm">{{ t('integrations.link.generateBody') }}</p>
          <template v-if="link && !expired">
            <UAlert
              color="warning"
              variant="subtle"
              icon="i-lucide-key-round"
              :title="t('integrations.link.shownOnce')"
              :description="t('integrations.link.expiresIn', { time: countdown })"
              data-testid="argos-link-countdown"
            />
            <CodeBlock
              :code="link.url"
              :label="t('integrations.link.url')"
              data-testid="argos-link-url"
            />
          </template>
          <UAlert
            v-else-if="link && expired"
            color="neutral"
            variant="subtle"
            icon="i-lucide-timer-off"
            :title="t('integrations.link.expired')"
            data-testid="argos-link-expired"
          />
          <div>
            <UButton
              icon="i-lucide-link"
              :loading="generating"
              :label="link ? t('integrations.link.regenerate') : t('integrations.link.generate')"
              data-testid="argos-link-generate"
              @click="generate"
            />
          </div>
          <p v-if="generateError" class="text-error text-sm" role="alert">{{ generateError }}</p>
        </div>
      </UCard>
      <UCard :ui="{ body: 'p-4 sm:p-4' }">
        <form class="flex flex-col gap-3" @submit.prevent="redeem">
          <h3 class="text-highlighted text-sm font-medium">
            {{ t('integrations.link.redeemTitle') }}
          </h3>
          <p class="text-muted text-sm">{{ t('integrations.link.redeemBody') }}</p>
          <UFormField
            :label="t('integrations.link.peerLink')"
            :help="looksOk ? t('integrations.link.peerLinkHelp') : undefined"
            :error="!looksOk ? t('integrations.link.peerLinkInvalid') : undefined"
          >
            <UInput
              v-model="peerLink"
              type="url"
              class="w-full"
              autocomplete="off"
              spellcheck="false"
              :ui="{ base: 'font-mono' }"
              placeholder="https://panel.isp.example/conectar#…"
              data-testid="argos-link-peer"
            />
          </UFormField>
          <div>
            <UButton
              type="submit"
              icon="i-lucide-plug"
              :loading="redeeming"
              :disabled="!peerLink.trim()"
              :label="t('integrations.link.redeem')"
              data-testid="argos-link-redeem"
            />
          </div>
          <UAlert
            v-if="redeemed"
            color="success"
            variant="subtle"
            icon="i-lucide-check"
            :title="t('integrations.link.redeemedTitle')"
            :description="t('integrations.link.redeemedBody')"
            data-testid="argos-link-redeemed"
          />
          <p
            v-if="redeemError"
            class="text-error text-sm"
            role="alert"
            data-testid="argos-link-error"
          >
            {{ redeemError }}
          </p>
        </form>
      </UCard>
    </div>
  </section>
</template>
