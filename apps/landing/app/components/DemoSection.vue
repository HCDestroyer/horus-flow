<script setup lang="ts">
import { CLIENT_RANGES, COUNTRY_CODES, ROUTER_RANGES, leadSchema } from '#shared/schemas'

const { t, locale } = useI18n()
const localePath = useLocalePath()
const config = useRuntimeConfig()
const countries = useCountryOptions()
const { honeypot, submitting, error, result, validate, submit, reset } = useFormSubmission(
  '/api/lead',
  leadSchema,
)

const blank = () => ({
  kind: 'demo' as const,
  name: '',
  company: '',
  country: 'GT' as (typeof COUNTRY_CODES)[number],
  clients: undefined as (typeof CLIENT_RANGES)[number] | undefined,
  routers: undefined as (typeof ROUTER_RANGES)[number] | undefined,
  email: '',
  phone: '',
  message: '',
  consent: false,
  locale: 'es' as 'es' | 'en',
})
const state = reactive(blank())

// País del sistema cuando se puede deducir (HIG entering-data › "Get information from the system").
onMounted(() => {
  const region = navigator.language?.split('-')[1]?.toUpperCase()
  if (region && (COUNTRY_CODES as readonly string[]).includes(region)) {
    state.country = region as (typeof COUNTRY_CODES)[number]
  }
})

const clientItems = computed(() =>
  CLIENT_RANGES.map((v) => ({ value: v, label: t(`form.clientsOptions.${v}`) })),
)
const routerItems = computed(() =>
  ROUTER_RANGES.map((v) => ({ value: v, label: t(`form.routersOptions.${v}`) })),
)

const done = ref<HTMLElement | null>(null)
async function onSubmit() {
  state.locale = locale.value === 'en' ? 'en' : 'es'
  const res = await submit({ ...state })
  if (res) {
    await nextTick()
    done.value?.focus()
  }
}
function again() {
  Object.assign(state, blank())
  reset()
}
</script>

<template>
  <section id="demo" aria-labelledby="demo-title" class="mx-auto max-w-6xl px-4 py-16 sm:px-6">
    <div class="grid gap-10 lg:grid-cols-[1fr_1.5fr]">
      <div>
        <SectionHeading
          id="demo-title"
          :eyebrow="t('demo.eyebrow')"
          :title="t('demo.title')"
          :lead="t('demo.lead', { email: config.public.salesEmail })"
        />
      </div>

      <div class="surface rounded-2xl p-5 sm:p-8">
        <div
          v-if="result"
          ref="done"
          tabindex="-1"
          role="status"
          class="flex flex-col items-start gap-3 outline-none"
          data-testid="demo-success"
        >
          <UIcon name="i-lucide-circle-check" class="size-8 text-primary" aria-hidden="true" />
          <h3 class="text-xl font-semibold text-highlighted">{{ t('demo.successTitle') }}</h3>
          <p class="text-toned">{{ t('demo.successBody', { ref: result.reference }) }}</p>
          <UButton
            color="neutral"
            variant="outline"
            size="lg"
            class="mt-2 min-h-11"
            :label="t('demo.another')"
            @click="again"
          />
        </div>

        <UForm
          v-else
          :state="state"
          :validate="validate"
          class="relative grid gap-5 sm:grid-cols-2"
          novalidate
          @submit="onSubmit"
        >
          <HoneypotField v-model="honeypot" />
          <LabeledField id="demo-name" :label="t('form.name')" name="name" required>
            <UInput
              id="demo-name"
              v-model="state.name"
              autocomplete="name"
              size="xl"
              class="w-full"
            />
          </LabeledField>
          <LabeledField id="demo-company" :label="t('form.company')" name="company" required>
            <UInput
              id="demo-company"
              v-model="state.company"
              autocomplete="organization"
              size="xl"
              class="w-full"
            />
          </LabeledField>
          <LabeledField id="demo-email" :label="t('form.email')" name="email" required>
            <UInput
              id="demo-email"
              v-model="state.email"
              type="email"
              autocomplete="email"
              inputmode="email"
              size="xl"
              class="w-full"
            />
          </LabeledField>
          <LabeledField
            id="demo-phone"
            :label="t('form.phone')"
            name="phone"
            :hint="t('form.optional')"
          >
            <UInput
              id="demo-phone"
              v-model="state.phone"
              type="tel"
              autocomplete="tel"
              inputmode="tel"
              size="xl"
              class="w-full"
            />
          </LabeledField>
          <LabeledField id="demo-country" :label="t('form.country')" name="country" required>
            <USelect
              id="demo-country"
              v-model="state.country"
              :items="countries"
              :placeholder="t('form.countryPlaceholder')"
              size="xl"
              class="w-full"
            />
          </LabeledField>
          <LabeledField id="demo-clients" :label="t('form.clients')" name="clients" required>
            <USelect
              id="demo-clients"
              v-model="state.clients"
              :items="clientItems"
              :placeholder="t('form.clientsPlaceholder')"
              size="xl"
              class="w-full"
            />
          </LabeledField>
          <LabeledField id="demo-routers" :label="t('form.routers')" name="routers" required>
            <USelect
              id="demo-routers"
              v-model="state.routers"
              :items="routerItems"
              :placeholder="t('form.routersPlaceholder')"
              size="xl"
              class="w-full"
            />
          </LabeledField>
          <LabeledField
            id="demo-message"
            :label="t('form.message')"
            name="message"
            :help="t('form.messageHint')"
            class="sm:col-span-2"
          >
            <UTextarea
              id="demo-message"
              v-model="state.message"
              :rows="4"
              autoresize
              size="xl"
              class="w-full"
            />
          </LabeledField>
          <UFormField name="consent" class="sm:col-span-2">
            <UCheckbox
              id="demo-consent"
              v-model="state.consent"
              size="lg"
              required
              :ui="{ root: 'items-start', container: 'mt-1', label: 'min-h-11' }"
            >
              <template #label>
                <span class="text-base font-normal text-toned">
                  {{ t('form.consentBefore') }}
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

          <div class="flex flex-col gap-3 sm:col-span-2">
            <UAlert
              v-if="error"
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
              :loading="submitting"
              class="min-h-12 self-start rounded-xl px-6"
              :label="submitting ? t('form.sending') : t('demo.submit')"
            />
          </div>
        </UForm>
      </div>
    </div>
  </section>
</template>
