<script setup lang="ts">
// Paso de pago tras crear la solicitud de compra: PayPal (botón del SDK, cargado solo aquí y
// solo al elegirlo), link de pago Neo o transferencia bancaria. El importe lo fija el servidor;
// el navegador solo envía la referencia y el token de la solicitud.
import type { PublicPayments } from '#shared/catalog'
import type { PurchaseOk } from '#shared/schemas'

interface BankAccount {
  bank: string
  type: { es: string; en: string }
  number: string
  holder: string
  currency: string
}
interface Instructions {
  method: 'neo' | 'transfer'
  neoUrl?: string
  accounts?: BankAccount[]
  instructions?: { es: string; en: string }
}
type Method = 'paypal' | 'neo' | 'transfer'

const props = defineProps<{
  result: PurchaseOk
  planName: string
  periodLabel: string
  payments: PublicPayments
}>()
const emit = defineEmits<{ paid: [] }>()

const { t, locale } = useI18n()
const loc = computed<'es' | 'en'>(() => (locale.value === 'en' ? 'en' : 'es'))
const { money } = usePricing()

const available = computed(() =>
  (['paypal', 'neo', 'transfer'] as const).filter((m) => props.result.methods[m]),
)
const method = ref<Method | null>(available.value.length === 1 ? available.value[0]! : null)
const loading = ref(false)
const error = ref<string | null>(null)
const instructions = ref<Instructions | null>(null)

const body = () => ({ reference: props.result.reference, token: props.result.accessToken })

async function chooseManual(m: 'neo' | 'transfer') {
  loading.value = true
  error.value = null
  instructions.value = null
  try {
    instructions.value = await $fetch<Instructions>('/api/checkout/method', {
      method: 'POST',
      body: { ...body(), method: m },
    })
  } catch {
    error.value = t('checkout.error')
  } finally {
    loading.value = false
  }
}

// ---- PayPal ---------------------------------------------------------------------------------
interface PaypalButtons {
  render(el: HTMLElement): Promise<void>
}
interface PaypalNamespace {
  Buttons(opts: Record<string, unknown>): PaypalButtons
}
const paypalBox = ref<HTMLElement | null>(null)
const paypalReady = ref(false)

function loadPaypalSdk(): Promise<PaypalNamespace> {
  const w = window as unknown as { paypal?: PaypalNamespace }
  if (w.paypal) return Promise.resolve(w.paypal)
  return new Promise((resolve, reject) => {
    const params = new URLSearchParams({
      'client-id': props.payments.paypal.clientId,
      currency: 'USD',
      intent: 'capture',
      components: 'buttons',
    })
    const s = document.createElement('script')
    s.src = `https://www.paypal.com/sdk/js?${params.toString()}`
    s.async = true
    s.dataset.namespace = 'paypal'
    s.onload = () => (w.paypal ? resolve(w.paypal) : reject(new Error('paypal')))
    s.onerror = () => reject(new Error('paypal'))
    document.head.appendChild(s)
  })
}

async function mountPaypal() {
  loading.value = true
  error.value = null
  try {
    const paypal = await loadPaypalSdk()
    await nextTick()
    if (!paypalBox.value) return
    paypalBox.value.innerHTML = ''
    await paypal
      .Buttons({
        style: { layout: 'vertical', shape: 'rect', label: 'pay', height: 48 },
        createOrder: async () => {
          const r = await $fetch<{ orderId: string }>('/api/checkout/paypal/order', {
            method: 'POST',
            body: body(),
          })
          return r.orderId
        },
        onApprove: async (data: { orderID: string }) => {
          loading.value = true
          try {
            await $fetch('/api/checkout/paypal/capture', {
              method: 'POST',
              body: { ...body(), orderId: data.orderID },
            })
            emit('paid')
          } catch {
            error.value = t('checkout.paypalCaptureError')
          } finally {
            loading.value = false
          }
        },
        onError: () => {
          error.value = t('checkout.paypalError')
        },
      })
      .render(paypalBox.value)
    paypalReady.value = true
  } catch {
    error.value = t('checkout.paypalLoadError')
  } finally {
    loading.value = false
  }
}

watch(
  method,
  async (m) => {
    if (!m || !import.meta.client) return
    if (m === 'paypal') {
      instructions.value = null
      await nextTick()
      await mountPaypal()
    } else {
      await chooseManual(m)
    }
  },
  { immediate: true },
)

const methodOptions = computed(() =>
  available.value.map((m) => ({
    value: m,
    label: t(`checkout.methods.${m}.label`),
    description: t(`checkout.methods.${m}.description`),
  })),
)

const copied = ref(false)
async function copyRef() {
  try {
    await navigator.clipboard.writeText(props.result.reference)
    copied.value = true
    setTimeout(() => (copied.value = false), 2500)
  } catch {
    copied.value = false
  }
}
</script>

<template>
  <section
    class="surface rounded-2xl p-5 sm:p-8"
    aria-labelledby="pay-title"
    data-testid="checkout"
  >
    <h2 id="pay-title" class="text-2xl font-bold text-highlighted">{{ t('checkout.title') }}</h2>
    <dl class="mt-5 grid gap-x-6 gap-y-3 sm:grid-cols-[auto_1fr]">
      <dt class="text-muted">{{ t('buy.reference') }}</dt>
      <dd class="flex flex-wrap items-center gap-3">
        <span
          class="font-mono text-xl font-semibold text-highlighted"
          data-testid="purchase-reference"
        >
          {{ result.reference }}
        </span>
        <UButton
          color="neutral"
          variant="outline"
          size="md"
          class="min-h-11"
          :icon="copied ? 'i-lucide-check' : 'i-lucide-copy'"
          :label="copied ? t('buy.copied') : t('buy.copy')"
          @click="copyRef"
        />
      </dd>
      <dt class="text-muted">{{ t('buy.plan') }}</dt>
      <dd class="font-medium text-highlighted">{{ planName }} · {{ periodLabel }}</dd>
      <dt class="text-muted">{{ t('buy.amount') }}</dt>
      <dd class="font-semibold text-highlighted tabular" data-testid="checkout-amount">
        {{ result.amount !== null ? money(result.amount, result.currency) : t('buy.toQuote') }}
      </dd>
    </dl>

    <fieldset class="mt-8">
      <legend class="text-lg font-semibold text-highlighted">{{ t('checkout.choose') }}</legend>
      <div class="mt-3 grid gap-3 sm:grid-cols-3">
        <label
          v-for="o in methodOptions"
          :key="o.value"
          :for="`pay-${o.value}`"
          class="flex min-h-11 cursor-pointer gap-3 rounded-xl border border-default p-4 transition-colors hover:bg-elevated has-[:checked]:border-(--ui-primary) has-[:checked]:ring-1 has-[:checked]:ring-(--ui-primary) has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-(--ui-primary)"
          :data-method="o.value"
        >
          <input
            :id="`pay-${o.value}`"
            v-model="method"
            type="radio"
            name="pay-method"
            :value="o.value"
            class="mt-1 size-5 accent-(--ui-primary)"
          />
          <span>
            <span class="block font-semibold text-highlighted">{{ o.label }}</span>
            <span class="mt-0.5 block text-sm text-muted">{{ o.description }}</span>
          </span>
        </label>
      </div>
    </fieldset>

    <div class="mt-6" aria-live="polite">
      <UAlert
        v-if="error"
        color="error"
        variant="subtle"
        :title="error"
        role="alert"
        icon="i-lucide-circle-alert"
        class="mb-4"
      />

      <div v-if="method === 'paypal'" data-testid="pay-paypal">
        <p v-if="result.currency !== 'USD' && result.amountUsd !== null" class="mb-3 text-toned">
          {{ t('checkout.paypalUsd', { amount: money(result.amountUsd, 'USD') }) }}
        </p>
        <p v-else class="mb-3 text-toned">{{ t('checkout.paypalHint') }}</p>
        <div ref="paypalBox" class="max-w-md" />
        <p v-if="loading && !paypalReady" class="text-muted">{{ t('checkout.loading') }}</p>
      </div>

      <div v-else-if="method === 'neo' && instructions?.neoUrl" data-testid="pay-neo">
        <p class="text-toned">{{ t('checkout.neoSteps', { ref: result.reference }) }}</p>
        <UButton
          :to="instructions.neoUrl"
          target="_blank"
          rel="noopener noreferrer"
          external
          size="xl"
          color="primary"
          class="mt-4 min-h-12 rounded-xl"
          trailing-icon="i-lucide-external-link"
          :label="t('checkout.neoOpen')"
        />
        <p class="mt-4 text-sm text-muted">{{ t('checkout.pendingNote') }}</p>
      </div>

      <div v-else-if="method === 'transfer' && instructions?.accounts" data-testid="pay-transfer">
        <p class="text-toned">{{ t('checkout.transferSteps', { ref: result.reference }) }}</p>
        <ul class="mt-4 grid gap-3 sm:grid-cols-2">
          <li
            v-for="(a, i) in instructions.accounts"
            :key="i"
            class="rounded-xl border border-default p-4"
          >
            <p class="font-semibold text-highlighted">{{ a.bank }}</p>
            <dl class="mt-2 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[0.95rem]">
              <dt class="text-muted">{{ t('checkout.accountType') }}</dt>
              <dd class="text-highlighted">{{ a.type[loc] || a.type.es }} · {{ a.currency }}</dd>
              <dt class="text-muted">{{ t('checkout.accountNumber') }}</dt>
              <dd class="font-mono text-highlighted">{{ a.number }}</dd>
              <dt class="text-muted">{{ t('checkout.accountHolder') }}</dt>
              <dd class="text-highlighted">{{ a.holder }}</dd>
            </dl>
          </li>
        </ul>
        <p
          v-if="
            instructions.instructions &&
            (instructions.instructions[loc] || instructions.instructions.es)
          "
          class="mt-4 whitespace-pre-line text-toned"
        >
          {{ instructions.instructions[loc] || instructions.instructions.es }}
        </p>
        <p class="mt-4 text-sm text-muted">
          {{ t('checkout.emailSent') }} {{ t('checkout.pendingNote') }}
        </p>
      </div>

      <p v-else-if="loading" class="text-muted">{{ t('checkout.loading') }}</p>
    </div>
  </section>
</template>
