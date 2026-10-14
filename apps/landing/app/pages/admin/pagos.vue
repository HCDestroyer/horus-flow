<script setup lang="ts">
// Métodos de pago: PayPal (sandbox/live, credenciales de solo escritura cifradas en el
// servidor), link de pago Neo por plan y periodo, y datos de transferencia bancaria.
import type { Currency, Localized, Period, PlanId } from '#shared/catalog'

definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: 'Pagos' })

interface Account {
  bank: string
  type: Localized
  number: string
  holder: string
  currency: Currency
}
interface Payments {
  paypal: {
    enabled: boolean
    mode: 'sandbox' | 'live'
    webhookId: string
    hasCredentials: boolean
    clientIdHint: string
    credentialsError: string
  }
  neo: { enabled: boolean; links: Partial<Record<PlanId, Partial<Record<Period, string>>>> }
  transfer: { enabled: boolean; accounts: Account[]; instructions: Localized }
  dataKey: { origin: 'file' | 'dev-generated' | 'missing'; error: string }
  webhookUrl: string
}

const toast = useToast()
const cfg = ref<Payments | null>(null)
const plans = ref<{ id: PlanId; name: string; buyable: boolean }[]>([])
const paypalForm = reactive({ clientId: '', clientSecret: '' })
const busy = ref<string | null>(null)
const neoForm = reactive<Record<string, Record<Period, string>>>({})
const buyablePlans = computed(() => plans.value.filter((x) => x.buyable))

const PERIODS: { id: Period; label: string }[] = [
  { id: 'monthly', label: 'Mensual' },
  { id: 'annual', label: 'Anual' },
]

async function load() {
  const [p, pricing] = await Promise.all([
    adminApi<Payments>('/payments'),
    adminApi<{ catalog: { plans: { id: PlanId; name: Localized; prices: unknown }[] } }>(
      '/pricing',
    ),
  ])
  for (const plan of pricing.catalog.plans) {
    neoForm[plan.id] = {
      monthly: p.neo.links[plan.id]?.monthly ?? '',
      annual: p.neo.links[plan.id]?.annual ?? '',
    }
  }
  cfg.value = p
  plans.value = pricing.catalog.plans.map((x) => ({
    id: x.id,
    name: x.name.es,
    buyable: Boolean(x.prices),
  }))
}
onMounted(load)

async function save(kind: 'paypal' | 'neo' | 'transfer') {
  if (!cfg.value) return
  busy.value = kind
  try {
    if (kind === 'paypal') {
      await adminApi('/payments/paypal', {
        method: 'POST',
        body: {
          enabled: cfg.value.paypal.enabled,
          mode: cfg.value.paypal.mode,
          webhookId: cfg.value.paypal.webhookId,
          clientId: paypalForm.clientId,
          clientSecret: paypalForm.clientSecret,
        },
      })
      paypalForm.clientId = ''
      paypalForm.clientSecret = ''
    } else if (kind === 'neo') {
      await adminApi('/payments/neo', {
        method: 'POST',
        body: { enabled: cfg.value.neo.enabled, links: neoForm },
      })
    } else {
      await adminApi('/payments/transfer', {
        method: 'POST',
        body: {
          enabled: cfg.value.transfer.enabled,
          accounts: cfg.value.transfer.accounts,
          instructions: cfg.value.transfer.instructions,
        },
      })
    }
    toast.add({ title: 'Guardado', color: 'primary', icon: 'i-lucide-check' })
    await load()
  } catch (err) {
    toast.add({ title: adminErrorMessage(err), color: 'error' })
  } finally {
    busy.value = null
  }
}

async function clearPaypal() {
  if (!cfg.value || !window.confirm('¿Borrar las credenciales de PayPal guardadas?')) return
  await adminApi('/payments/paypal', {
    method: 'POST',
    body: {
      enabled: false,
      mode: cfg.value.paypal.mode,
      webhookId: cfg.value.paypal.webhookId,
      clearCredentials: true,
    },
  })
  await load()
}

function removeAccount(i: number) {
  cfg.value?.transfer.accounts.splice(i, 1)
}

function addAccount() {
  cfg.value!.transfer.accounts.push({
    bank: '',
    type: { es: 'Monetaria', en: 'Checking' },
    number: '',
    holder: 'Connection And Solutions Company, S.A.',
    currency: 'GTQ',
  })
}
</script>

<template>
  <div>
    <AdminPageHeader
      title="Pagos"
      lead="Activa y configura los métodos que se ofrecen en la página de compra tras enviar la solicitud."
    />
    <div v-if="cfg" class="space-y-6">
      <UAlert
        v-if="cfg.dataKey.origin !== 'file'"
        :color="cfg.dataKey.origin === 'missing' ? 'error' : 'neutral'"
        variant="subtle"
        icon="i-lucide-key-round"
        :title="
          cfg.dataKey.origin === 'missing'
            ? 'Falta la clave de datos (DATA_KEY_FILE)'
            : 'Clave de datos de desarrollo'
        "
        :description="
          cfg.dataKey.origin === 'missing'
            ? 'Sin ella no se pueden guardar ni usar las credenciales de PayPal. Define DATA_KEY_FILE en el servidor (README › Variables).'
            : 'Se usa una clave generada en el volumen de datos. En producción, define DATA_KEY_FILE.'
        "
      />

      <!-- PayPal -->
      <section class="surface rounded-2xl p-5" aria-labelledby="pp-title">
        <form class="space-y-4" @submit.prevent="save('paypal')">
          <div class="flex flex-wrap items-center gap-3">
            <h2 id="pp-title" class="text-lg font-semibold text-highlighted">PayPal</h2>
            <span class="text-sm text-muted">Cobra en USD (PayPal no admite GTQ).</span>
          </div>
          <label for="pp-enabled" class="flex min-h-11 items-center gap-3">
            <input
              id="pp-enabled"
              v-model="cfg.paypal.enabled"
              type="checkbox"
              class="size-5 accent-(--ui-primary)"
            />
            Ofrecer PayPal en la compra
          </label>
          <SegmentedControl
            v-model="cfg.paypal.mode"
            solid
            name="pp-mode"
            legend="Modo"
            :options="[
              { value: 'sandbox', label: 'Sandbox (pruebas)' },
              { value: 'live', label: 'Live (real)' },
            ]"
          />
          <p class="text-[0.95rem]" data-testid="paypal-credentials">
            Credenciales:
            <strong v-if="cfg.paypal.hasCredentials"
              >guardadas (client id …{{ cfg.paypal.clientIdHint }})</strong
            >
            <strong v-else>sin guardar</strong>
            <span v-if="cfg.paypal.credentialsError" class="text-error">
              · {{ cfg.paypal.credentialsError }}</span
            >
          </p>
          <div class="grid gap-4 md:grid-cols-2">
            <div>
              <label for="pp-client" class="mb-1 block text-sm font-medium">Client ID</label>
              <UInput
                id="pp-client"
                v-model="paypalForm.clientId"
                autocomplete="off"
                :placeholder="cfg.paypal.hasCredentials ? 'Vacío = conservar el guardado' : ''"
                class="w-full font-mono"
              />
            </div>
            <div>
              <label for="pp-secret" class="mb-1 block text-sm font-medium">Secret</label>
              <UInput
                id="pp-secret"
                v-model="paypalForm.clientSecret"
                type="password"
                autocomplete="new-password"
                :placeholder="cfg.paypal.hasCredentials ? 'Vacío = conservar el guardado' : ''"
                class="w-full font-mono"
              />
            </div>
          </div>
          <p class="text-sm text-muted">
            Solo escritura: se guardan cifrados y nunca se vuelven a mostrar.
          </p>
          <div>
            <label for="pp-webhook" class="mb-1 block text-sm font-medium">Webhook ID</label>
            <UInput
              id="pp-webhook"
              v-model="cfg.paypal.webhookId"
              class="w-full max-w-md font-mono"
            />
            <p class="mt-1 text-sm text-muted">
              En PayPal Developer › tu app › Webhooks, añade
              <code class="font-mono text-highlighted">{{ cfg.webhookUrl }}</code>
              con el evento PAYMENT.CAPTURE.COMPLETED y pega aquí su ID.
            </p>
          </div>
          <div class="flex flex-wrap gap-2">
            <UButton
              type="submit"
              class="min-h-11"
              label="Guardar PayPal"
              :loading="busy === 'paypal'"
            />
            <UButton
              v-if="cfg.paypal.hasCredentials"
              color="neutral"
              variant="ghost"
              class="min-h-11"
              label="Borrar credenciales"
              @click="clearPaypal"
            />
          </div>
        </form>
      </section>

      <!-- Neo -->
      <section class="surface rounded-2xl p-5" aria-labelledby="neo-title">
        <form class="space-y-4" @submit.prevent="save('neo')">
          <h2 id="neo-title" class="text-lg font-semibold text-highlighted">Link de pago Neo</h2>
          <label for="neo-enabled" class="flex min-h-11 items-center gap-3">
            <input
              id="neo-enabled"
              v-model="cfg.neo.enabled"
              type="checkbox"
              class="size-5 accent-(--ui-primary)"
            />
            Ofrecer link de pago Neo en la compra
          </label>
          <p class="text-sm text-muted">
            Pega el link de pago generado en Neo para cada plan y periodo (https://…). El cliente
            verá el botón y su referencia; la solicitud queda «Pendiente de pago» hasta que la
            marques como pagada.
          </p>
          <div class="overflow-x-auto">
            <table class="w-full text-left">
              <thead class="text-sm text-muted">
                <tr>
                  <th scope="col" class="py-1 pr-3 font-medium">Plan</th>
                  <th v-for="p in PERIODS" :key="p.id" scope="col" class="py-1 pr-3 font-medium">
                    {{ p.label }}
                  </th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="plan in buyablePlans" :key="plan.id">
                  <th scope="row" class="py-1 pr-3 font-medium whitespace-nowrap">
                    {{ plan.name }}
                  </th>
                  <td v-for="per in PERIODS" :key="per.id" class="min-w-64 py-1 pr-3">
                    <AdminNeoInput
                      :id="`neo-${plan.id}-${per.id}`"
                      v-model="neoForm[plan.id]![per.id]"
                      :label="`Link Neo ${plan.name} ${per.label}`"
                    />
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <UButton type="submit" class="min-h-11" label="Guardar Neo" :loading="busy === 'neo'" />
        </form>
      </section>

      <!-- Transferencia -->
      <section class="surface rounded-2xl p-5" aria-labelledby="tr-title">
        <form class="space-y-4" @submit.prevent="save('transfer')">
          <h2 id="tr-title" class="text-lg font-semibold text-highlighted">
            Transferencia bancaria
          </h2>
          <label for="tr-enabled" class="flex min-h-11 items-center gap-3">
            <input
              id="tr-enabled"
              v-model="cfg.transfer.enabled"
              type="checkbox"
              class="size-5 accent-(--ui-primary)"
            />
            Ofrecer transferencia bancaria en la compra
          </label>
          <div
            v-for="(a, i) in cfg.transfer.accounts"
            :key="i"
            class="rounded-xl border border-default p-4"
          >
            <div class="flex items-center justify-between gap-2">
              <h3 class="font-semibold">Cuenta {{ i + 1 }}</h3>
              <UButton
                color="neutral"
                variant="ghost"
                icon="i-lucide-trash-2"
                class="min-h-11 min-w-11"
                :aria-label="`Quitar cuenta ${i + 1}`"
                @click="removeAccount(i)"
              />
            </div>
            <div class="mt-2 grid gap-4 md:grid-cols-2">
              <div>
                <label :for="`tr-bank-${i}`" class="mb-1 block text-sm font-medium">Banco</label>
                <UInput :id="`tr-bank-${i}`" v-model="a.bank" required class="w-full" />
              </div>
              <div>
                <label :for="`tr-num-${i}`" class="mb-1 block text-sm font-medium"
                  >Número de cuenta</label
                >
                <UInput :id="`tr-num-${i}`" v-model="a.number" required class="w-full font-mono" />
              </div>
              <div>
                <label :for="`tr-holder-${i}`" class="mb-1 block text-sm font-medium"
                  >Titular</label
                >
                <UInput :id="`tr-holder-${i}`" v-model="a.holder" required class="w-full" />
              </div>
              <div>
                <label :for="`tr-cur-${i}`" class="mb-1 block text-sm font-medium">Moneda</label>
                <select
                  :id="`tr-cur-${i}`"
                  v-model="a.currency"
                  class="min-h-11 w-full rounded-lg border border-default bg-(--surface) px-3"
                >
                  <option value="GTQ">GTQ</option>
                  <option value="USD">USD</option>
                </select>
              </div>
              <AdminLocalizedInput
                :id="`tr-type-${i}`"
                v-model="a.type"
                label="Tipo de cuenta"
                class="md:col-span-2"
              />
            </div>
          </div>
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-plus"
            class="min-h-11"
            label="Añadir cuenta"
            @click="addAccount"
          />
          <AdminLocalizedInput
            id="tr-instr"
            v-model="cfg.transfer.instructions"
            label="Instrucciones para el cliente"
            multiline
          />
          <UButton
            type="submit"
            class="min-h-11"
            label="Guardar transferencia"
            :loading="busy === 'transfer'"
          />
        </form>
      </section>
    </div>
  </div>
</template>
