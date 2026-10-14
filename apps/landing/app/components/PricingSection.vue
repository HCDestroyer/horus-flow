<script setup lang="ts">
// Planes y precios desde la base de datos (editables en /admin › Precios). Con "Mostrar
// importes" desactivado (confirmed: false) no se muestra ninguna cifra. El panel usa este mismo
// componente para la vista previa (`catalog` = borrador, `preview` = sin enlaces).
import type { Catalog, CatalogPlan, Currency, Period } from '#shared/catalog'

const props = defineProps<{ catalog?: Catalog | null; preview?: boolean }>()

const { t } = useI18n()
const localePath = useLocalePath()
const { pricing, plans, period, currency, l, money, price } = usePricing(toRef(props, 'catalog'))
const site = useSiteState()
const responseTime = computed(() =>
  props.preview || !site.value ? '' : l(site.value.settings.support.responseTime),
)

const periodOptions = computed(() => [
  { value: 'monthly' as Period, label: t('pricing.monthly') },
  { value: 'annual' as Period, label: t('pricing.annual') },
])
const currencyOptions = computed(() =>
  pricing.value.currencies.map((c) => ({ value: c as Currency, label: c })),
)

function buyLink(plan: CatalogPlan) {
  return localePath({ path: '/comprar', query: { plan: plan.id, period: period.value } })
}

const faq = computed(() =>
  (['license', 'support', 'updates', 'grow', 'currency'] as const).map((k) => ({
    label: t(`pricing.faq.${k}.q`),
    content: t(`pricing.faq.${k}.a`),
    value: k,
  })),
)
</script>

<template>
  <section
    id="pricing"
    aria-labelledby="pricing-title"
    class="mx-auto max-w-6xl px-4 py-16 sm:px-6"
  >
    <SectionHeading
      id="pricing-title"
      :eyebrow="t('pricing.eyebrow')"
      :title="t('pricing.title')"
      :lead="t('pricing.lead')"
    />

    <div class="mt-8 flex flex-wrap items-center gap-x-6 gap-y-3">
      <SegmentedControl
        v-model="period"
        name="pricing-period"
        :legend="t('pricing.period')"
        :options="periodOptions"
      />
      <SegmentedControl
        v-if="pricing.confirmed"
        v-model="currency"
        name="pricing-currency"
        :legend="t('pricing.currency')"
        :options="currencyOptions"
      />
      <p class="text-[0.95rem] text-muted">
        {{ pricing.confirmed ? l(pricing.annualNote) : t('pricing.unconfirmedNote') }}
      </p>
    </div>

    <ul class="mt-8 grid gap-5 md:grid-cols-2 xl:grid-cols-4">
      <li
        v-for="plan in plans"
        :key="plan.id"
        class="surface relative flex flex-col rounded-2xl p-6"
        :class="plan.highlighted ? 'ring-2 ring-(--ui-primary)' : ''"
        :data-plan="plan.id"
      >
        <div class="flex items-center justify-between gap-2">
          <h3 class="text-xl font-semibold text-highlighted">{{ l(plan.name) }}</h3>
          <span
            v-if="plan.highlighted"
            class="rounded-full bg-(--ui-primary) px-2.5 py-0.5 text-sm font-medium text-(--ui-bg)"
          >
            {{ t('pricing.recommended') }}
          </span>
        </div>
        <p class="mt-2 text-toned">{{ l(plan.summary) }}</p>

        <div class="mt-5 min-h-20" aria-live="polite">
          <template v-if="!plan.prices">
            <p class="text-2xl font-bold text-highlighted">{{ t('pricing.custom') }}</p>
          </template>
          <template v-else-if="price(plan) !== null">
            <p class="text-highlighted">
              <span class="text-3xl font-bold tabular">{{ money(price(plan)!) }}</span>
              <span class="ml-1 text-muted">
                {{ period === 'annual' ? t('pricing.perYear') : t('pricing.perMonth') }}
              </span>
            </p>
            <p class="mt-1 text-sm text-muted">
              {{ period === 'annual' ? t('pricing.billedAnnual') : t('pricing.billedMonthly') }}
            </p>
          </template>
          <template v-else>
            <p
              class="text-lg leading-snug font-semibold text-highlighted"
              data-testid="launch-price"
            >
              {{ t('pricing.launch') }}
            </p>
            <p class="mt-1 text-sm text-muted">
              {{ period === 'annual' ? t('pricing.billedAnnual') : t('pricing.billedMonthly') }}
            </p>
          </template>
        </div>

        <ul class="mt-4 flex-1 space-y-2.5">
          <li
            v-for="(inc, i) in plan.includes"
            :key="i"
            class="flex gap-2.5 text-[0.95rem] text-toned"
          >
            <UIcon
              name="i-lucide-check"
              class="mt-0.5 size-5 shrink-0 text-primary"
              aria-hidden="true"
            />
            <span>{{ l(inc) }}</span>
          </li>
        </ul>
        <p v-if="plan.server" class="mt-4 border-t border-default pt-3 text-sm text-muted">
          {{ t('pricing.server', { spec: l(plan.server) }) }}
        </p>

        <UButton
          v-if="plan.prices"
          :to="preview ? undefined : buyLink(plan)"
          :color="plan.highlighted ? 'primary' : 'neutral'"
          :variant="plan.highlighted ? 'solid' : 'outline'"
          size="xl"
          block
          class="mt-6 min-h-12 rounded-xl"
          :label="pricing.confirmed ? t('pricing.ctaBuy') : t('pricing.ctaQuote')"
          :aria-label="`${pricing.confirmed ? t('pricing.ctaBuy') : t('pricing.ctaQuote')}: ${l(plan.name)}`"
        />
        <UButton
          v-else
          :to="preview ? undefined : '#demo'"
          color="neutral"
          variant="outline"
          size="xl"
          block
          class="mt-6 min-h-12 rounded-xl"
          :label="t('pricing.ctaContact')"
        />
      </li>
    </ul>

    <div class="mt-10 grid gap-10 lg:grid-cols-[1fr_1.4fr]">
      <div>
        <h3 class="text-lg font-semibold text-highlighted">{{ t('pricing.allInclude') }}</h3>
        <ul class="mt-4 space-y-2.5">
          <li v-for="(inc, i) in pricing.allPlansInclude" :key="i" class="flex gap-2.5 text-toned">
            <UIcon
              name="i-lucide-badge-check"
              class="mt-0.5 size-5 shrink-0 text-primary"
              aria-hidden="true"
            />
            <span>{{ l(inc) }}</span>
          </li>
        </ul>
        <p v-if="responseTime" class="mt-3 text-[0.95rem] text-muted">
          {{ t('pricing.responseTime', { time: responseTime }) }}
        </p>
      </div>
      <div v-if="!preview">
        <h3 class="text-lg font-semibold text-highlighted">{{ t('pricing.faqTitle') }}</h3>
        <UAccordion
          :items="faq"
          type="multiple"
          class="mt-2"
          :ui="{ trigger: 'min-h-12 text-base text-highlighted', body: 'text-toned text-base' }"
        />
      </div>
    </div>
  </section>
</template>
