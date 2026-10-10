<script setup lang="ts">
// Compra de licencia: plan, periodo y datos de facturación → solicitud con referencia (y
// aviso a ventas) → pago con PayPal, link Neo o transferencia (CheckoutPayment). El importe lo
// calcula siempre el servidor desde la base de datos.
import type { Currency, Period, PlanId } from '#shared/catalog'
import { COUNTRY_CODES, purchaseSchema, type PurchaseOk } from '#shared/schemas'

usePageSeo('buy', 'meta.buyTitle', 'meta.buyDescription')

const { t, locale } = useI18n()
const localePath = useLocalePath()
const route = useRoute()
const countries = useCountryOptions()
const { pricing, plans, l, money } = usePricing()
const site = useSite()
const { honeypot, submitting, error, result, validate, submit } = useFormSubmission(
  '/api/purchase',
  purchaseSchema,
)

const buyable = computed(() => plans.value.filter((p) => p.prices))
const qPlan = String(route.query.plan ?? '')
const qPeriod = String(route.query.period ?? '')

const state = reactive({
  plan: (buyable.value.some((p) => p.id === qPlan)
    ? qPlan
    : (buyable.value.find((p) => p.highlighted) ?? buyable.value[0])?.id) as PlanId,
  period: (qPeriod === 'monthly' || qPeriod === 'annual'
    ? qPeriod
    : pricing.value.defaultPeriod) as Period,
  currency: pricing.value.defaultCurrency as Currency,
  legalName: '',
  nit: '',
  country: 'GT' as (typeof COUNTRY_CODES)[number],
  address: '',
  contactName: '',
  email: '',
  phone: '',
  notes: '',
  consent: false,
  locale: 'es' as 'es' | 'en',
})

onMounted(() => {
  const region = navigator.language?.split('-')[1]?.toUpperCase()
  if (region && (COUNTRY_CODES as readonly string[]).includes(region)) {
    state.country = region as (typeof COUNTRY_CODES)[number]
  }
})

const planItems = computed(() =>
  buyable.value.map((p) => ({ value: p.id, label: l(p.name), description: l(p.summary) })),
)
const periodOptions = computed(() => [
  { value: 'monthly' as Period, label: t('pricing.monthly') },
  { value: 'annual' as Period, label: t('pricing.annual') },
])
const currencyOptions = computed(() =>
  pricing.value.currencies.map((c) => ({ value: c as Currency, label: c })),
)

const selected = computed(() => plans.value.find((p) => p.id === state.plan) ?? buyable.value[0]!)
const amount = computed(() =>
  pricing.value.confirmed && selected.value.prices
    ? selected.value.prices[state.currency][state.period]
    : null,
)
const purchase = computed(() => result.value as PurchaseOk | null)
const canPay = computed(() => {
  const m = purchase.value?.methods
  return Boolean(m && (m.paypal || m.neo || m.transfer))
})
const paid = ref(false)
const anyMethod = computed(() => {
  const p = site.value.payments
  return p.paypal.enabled || p.neo.enabled || p.transfer.enabled
})
const periodLabel = computed(() =>
  state.period === 'annual' ? t('pricing.annual') : t('pricing.monthly'),
)

const done = ref<HTMLElement | null>(null)
async function onSubmit() {
  state.locale = locale.value === 'en' ? 'en' : 'es'
  const res = await submit({ ...state })
  if (!res) return
  await nextTick()
  done.value?.focus()
  window.scrollTo({ top: 0 })
}

async function onPaid() {
  paid.value = true
  await nextTick()
  done.value?.focus()
  window.scrollTo({ top: 0 })
}

const copied = ref(false)
async function copyRef() {
  if (!result.value) return
  try {
    await navigator.clipboard.writeText(result.value.reference)
    copied.value = true
    setTimeout(() => (copied.value = false), 2500)
  } catch {
    copied.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 pt-10 pb-8 sm:px-6 md:pt-16">
    <div v-if="result && canPay && !paid" class="mx-auto max-w-3xl">
      <div
        ref="done"
        tabindex="-1"
        role="status"
        class="outline-none"
        data-testid="purchase-success"
      >
        <p class="eyebrow">{{ t('buy.eyebrow') }}</p>
        <h1 class="mt-2 text-3xl font-bold text-highlighted sm:text-4xl">
          {{ t('checkout.created') }}
        </h1>
        <p class="mt-3 text-lg text-toned">{{ t('checkout.lead') }}</p>
      </div>
      <CheckoutPayment
        class="mt-8"
        :result="purchase!"
        :plan-name="l(selected.name)"
        :period-label="periodLabel"
        :payments="site.payments"
        @paid="onPaid"
      />
    </div>

    <div
      v-else-if="result"
      ref="done"
      tabindex="-1"
      role="status"
      class="surface mx-auto max-w-2xl rounded-2xl p-6 outline-none sm:p-10"
      data-testid="purchase-success"
    >
      <UIcon name="i-lucide-circle-check" class="size-9 text-primary" aria-hidden="true" />
      <h1 class="mt-3 text-3xl font-bold text-highlighted" data-testid="purchase-title">
        {{ paid ? t('checkout.paidTitle') : t('buy.successTitle') }}
      </h1>
      <p class="mt-6 text-sm font-semibold text-muted">{{ t('buy.reference') }}</p>
      <div class="mt-1 flex flex-wrap items-center gap-3">
        <span
          class="font-mono text-2xl font-semibold text-highlighted"
          data-testid="purchase-reference"
        >
          {{ result.reference }}
        </span>
        <UButton
          color="neutral"
          variant="outline"
          size="lg"
          class="min-h-11"
          :icon="copied ? 'i-lucide-check' : 'i-lucide-copy'"
          :label="copied ? t('buy.copied') : t('buy.copy')"
          @click="copyRef"
        />
      </div>
      <p class="mt-6 text-toned">{{ paid ? t('checkout.paidBody') : t('buy.manualSteps') }}</p>
      <UButton
        :to="localePath('/')"
        color="neutral"
        variant="ghost"
        size="lg"
        class="mt-6 min-h-11 px-0"
        :label="t('buy.backHome')"
        icon="i-lucide-arrow-left"
      />
    </div>

    <template v-else>
      <div class="max-w-3xl">
        <p class="eyebrow">{{ t('buy.eyebrow') }}</p>
        <h1 class="mt-2 text-4xl font-bold text-highlighted sm:text-5xl">{{ t('buy.title') }}</h1>
        <p class="mt-4 text-lg text-toned">{{ t('buy.lead') }}</p>
      </div>

      <UForm
        :state="state"
        :validate="validate"
        class="relative mt-10 grid gap-8 lg:grid-cols-[1.6fr_1fr]"
        novalidate
        @submit="onSubmit"
      >
        <HoneypotField v-model="honeypot" />
        <div class="space-y-8">
          <section class="surface rounded-2xl p-5 sm:p-8" aria-labelledby="buy-plan">
            <h2 id="buy-plan" class="text-xl font-semibold text-highlighted">
              {{ t('buy.stepPlan') }}
            </h2>
            <UFormField name="plan" class="mt-5" :label="t('buy.plan')">
              <URadioGroup
                v-model="state.plan"
                :items="planItems"
                variant="card"
                size="lg"
                :ui="{ fieldset: 'grid gap-3 sm:grid-cols-3', item: 'min-h-11 rounded-xl' }"
              />
            </UFormField>
            <div class="mt-6 flex flex-wrap gap-4">
              <SegmentedControl
                v-model="state.period"
                solid
                name="buy-period"
                :legend="t('pricing.period')"
                :options="periodOptions"
              />
              <SegmentedControl
                v-model="state.currency"
                solid
                name="buy-currency"
                :legend="t('pricing.currency')"
                :options="currencyOptions"
              />
            </div>
          </section>

          <section class="surface rounded-2xl p-5 sm:p-8" aria-labelledby="buy-billing">
            <h2 id="buy-billing" class="text-xl font-semibold text-highlighted">
              {{ t('buy.stepBilling') }}
            </h2>
            <div class="mt-5 grid gap-5 sm:grid-cols-2">
              <UFormField
                :label="t('buy.legalName')"
                name="legalName"
                required
                class="sm:col-span-2"
              >
                <UInput
                  v-model="state.legalName"
                  autocomplete="organization"
                  size="xl"
                  class="w-full"
                />
              </UFormField>
              <UFormField :label="t('form.country')" name="country" required>
                <USelect v-model="state.country" :items="countries" size="xl" class="w-full" />
              </UFormField>
              <UFormField :label="t('buy.nit')" name="nit" :help="t('buy.nitHint')">
                <UInput v-model="state.nit" autocomplete="off" size="xl" class="w-full" />
              </UFormField>
              <UFormField
                :label="t('buy.address')"
                name="address"
                :hint="t('form.optional')"
                class="sm:col-span-2"
              >
                <UInput
                  v-model="state.address"
                  autocomplete="street-address"
                  size="xl"
                  class="w-full"
                />
              </UFormField>
            </div>
          </section>

          <section class="surface rounded-2xl p-5 sm:p-8" aria-labelledby="buy-contact">
            <h2 id="buy-contact" class="text-xl font-semibold text-highlighted">
              {{ t('buy.stepContact') }}
            </h2>
            <div class="mt-5 grid gap-5 sm:grid-cols-2">
              <UFormField :label="t('buy.contactName')" name="contactName" required>
                <UInput v-model="state.contactName" autocomplete="name" size="xl" class="w-full" />
              </UFormField>
              <UFormField :label="t('form.email')" name="email" required>
                <UInput
                  v-model="state.email"
                  type="email"
                  autocomplete="email"
                  inputmode="email"
                  size="xl"
                  class="w-full"
                />
              </UFormField>
              <UFormField :label="t('form.phone')" name="phone" :hint="t('form.optional')">
                <UInput
                  v-model="state.phone"
                  type="tel"
                  autocomplete="tel"
                  inputmode="tel"
                  size="xl"
                  class="w-full"
                />
              </UFormField>
              <UFormField
                :label="t('buy.notes')"
                name="notes"
                :help="t('buy.notesHint')"
                class="sm:col-span-2"
              >
                <UTextarea v-model="state.notes" :rows="3" autoresize size="xl" class="w-full" />
              </UFormField>
            </div>
          </section>
        </div>

        <aside class="lg:sticky lg:top-24 lg:self-start" aria-labelledby="buy-summary">
          <div class="surface rounded-2xl p-5 sm:p-6">
            <h2 id="buy-summary" class="text-lg font-semibold text-highlighted">
              {{ t('buy.summary') }}
            </h2>
            <dl class="mt-4 space-y-3">
              <div class="flex justify-between gap-4">
                <dt class="text-muted">{{ t('buy.plan') }}</dt>
                <dd class="text-right font-medium text-highlighted">{{ l(selected.name) }}</dd>
              </div>
              <div class="flex justify-between gap-4">
                <dt class="text-muted">{{ t('pricing.period') }}</dt>
                <dd class="text-right font-medium text-highlighted">
                  {{ state.period === 'annual' ? t('pricing.annual') : t('pricing.monthly') }}
                </dd>
              </div>
              <div class="flex justify-between gap-4 border-t border-default pt-3">
                <dt class="text-muted">{{ t('buy.amount') }}</dt>
                <dd class="text-right font-semibold text-highlighted" aria-live="polite">
                  {{ amount !== null ? money(amount, state.currency) : t('buy.toQuote') }}
                </dd>
              </div>
            </dl>
            <p v-if="!pricing.confirmed" class="mt-3 text-sm text-muted">
              {{ t('pricing.launch') }}
            </p>

            <UFormField name="consent" class="mt-6">
              <UCheckbox
                v-model="state.consent"
                size="lg"
                required
                :ui="{ root: 'items-start', container: 'mt-1', label: 'min-h-11' }"
              >
                <template #label>
                  <span class="text-[0.95rem] font-normal text-toned">
                    {{ t('buy.consentBefore') }}
                    <NuxtLink
                      :to="localePath('/legal/terminos')"
                      target="_blank"
                      class="text-primary underline underline-offset-4"
                      >{{ t('form.termsLink') }}</NuxtLink
                    >
                    {{ t('buy.consentAnd') }}
                    <NuxtLink
                      :to="localePath('/legal/privacidad')"
                      target="_blank"
                      class="text-primary underline underline-offset-4"
                      >{{ t('form.privacyLink') }}</NuxtLink
                    >.
                  </span>
                </template>
              </UCheckbox>
            </UFormField>

            <UAlert
              v-if="error"
              class="mt-4"
              color="error"
              variant="subtle"
              :title="error"
              role="alert"
              icon="i-lucide-circle-alert"
            />
            <UButton
              type="submit"
              size="xl"
              color="primary"
              block
              :loading="submitting"
              class="mt-5 min-h-12 rounded-xl"
              :label="
                submitting
                  ? t('form.sending')
                  : anyMethod && pricing.confirmed
                    ? t('buy.submit')
                    : t('buy.submitRequest')
              "
            />
            <p class="mt-4 flex gap-2 text-sm text-muted">
              <UIcon
                name="i-lucide-shield-check"
                class="mt-0.5 size-4 shrink-0"
                aria-hidden="true"
              />
              {{ t('buy.noPayment') }}
            </p>
          </div>
        </aside>
      </UForm>
    </template>
  </div>
</template>
