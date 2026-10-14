<script setup lang="ts">
definePageMeta({ layout: 'admin', middleware: 'admin' })

interface Detail {
  id: number
  reference: string
  kind: 'demo' | 'contact' | 'purchase'
  status: string
  createdAt: string
  name: string
  company: string
  email: string
  country: string
  plan: string | null
  period: string | null
  currency: string | null
  amount: number | null
  amountUsd: number | null
  paymentMethod: string | null
  locale: string
  data: Record<string, unknown>
  paypalOrderId: string | null
  paypalCaptureId: string | null
  paidAt: string | null
  paidAmount: number | null
  paidCurrency: string | null
  events: { at: string; actor: string; from: string | null; to: string | null; note: string }[]
}

const route = useRoute()
const toast = useToast()
const detail = ref<Detail | null>(null)
const error = ref<string | null>(null)
const nextStatus = ref('')
const note = ref('')
const notify = ref(true)
const saving = ref(false)

useHead({ title: () => detail.value?.reference ?? 'Solicitud' })

async function load() {
  try {
    detail.value = await adminApi<Detail>(`/requests/${route.params.id}`)
    nextStatus.value = detail.value.status
  } catch (err) {
    error.value = adminErrorMessage(err)
  }
}
onMounted(load)

const FIELDS: [string, string][] = [
  ['legalName', 'Razón social'],
  ['nit', 'NIT'],
  ['address', 'Dirección de facturación'],
  ['contactName', 'Contacto'],
  ['name', 'Nombre'],
  ['company', 'Empresa / ISP'],
  ['email', 'Correo'],
  ['phone', 'Teléfono / WhatsApp'],
  ['country', 'País'],
  ['clients', 'Clientes'],
  ['routers', 'Routers MikroTik'],
  ['message', 'Mensaje'],
  ['notes', 'Comentarios'],
  ['locale', 'Idioma'],
]
const rows = computed(() =>
  detail.value
    ? FIELDS.filter(
        ([k]) => detail.value!.data[k] !== undefined && detail.value!.data[k] !== '',
      ).map(([k, label]) => [label, String(detail.value!.data[k])])
    : [],
)
const statusOptions = computed(() =>
  Object.entries(STATUS_LABELS)
    .filter(([v]) => detail.value?.kind === 'purchase' || !['paid', 'pending_payment'].includes(v))
    .map(([value, label]) => ({ value, label })),
)

async function saveStatus() {
  if (!detail.value) return
  saving.value = true
  try {
    await adminApi(`/requests/${detail.value.id}/status`, {
      method: 'POST',
      body: { status: nextStatus.value, note: note.value, notify: notify.value },
    })
    note.value = ''
    toast.add({
      title: `Estado: ${STATUS_LABELS[nextStatus.value]}`,
      color: 'primary',
      icon: 'i-lucide-check',
    })
    await load()
  } catch (err) {
    toast.add({ title: adminErrorMessage(err), color: 'error' })
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div>
    <NuxtLink
      to="/admin/solicitudes"
      class="mb-3 inline-flex min-h-11 items-center gap-1 text-sm text-muted hover:text-highlighted"
    >
      <UIcon name="i-lucide-chevron-left" class="size-4" aria-hidden="true" /> Solicitudes
    </NuxtLink>
    <UAlert v-if="error" color="error" variant="subtle" :title="error" role="alert" />
    <template v-if="detail">
      <AdminPageHeader
        :title="detail.reference"
        :lead="`${detail.company} · recibida el ${formatDate(detail.createdAt)}`"
      >
        <template #actions>
          <AdminStatus :status="detail.status" />
        </template>
      </AdminPageHeader>

      <div class="grid gap-6 xl:grid-cols-[1.4fr_1fr]">
        <div class="space-y-6">
          <section
            v-if="detail.kind === 'purchase'"
            class="surface rounded-2xl p-5"
            aria-labelledby="d-buy"
          >
            <h2 id="d-buy" class="text-lg font-semibold text-highlighted">Compra</h2>
            <dl class="mt-3 grid gap-x-6 gap-y-2 sm:grid-cols-[12rem_1fr]">
              <dt class="text-muted">Plan</dt>
              <dd>{{ detail.plan }} · {{ detail.period === 'annual' ? 'anual' : 'mensual' }}</dd>
              <dt class="text-muted">Importe</dt>
              <dd class="tabular">
                {{ formatMoney(detail.amount, detail.currency) }}
                <span v-if="detail.currency !== 'USD' && detail.amountUsd" class="text-muted">
                  (PayPal: {{ formatMoney(detail.amountUsd, 'USD') }})
                </span>
              </dd>
              <dt class="text-muted">Método de pago</dt>
              <dd>
                {{ detail.paymentMethod ? METHOD_LABELS[detail.paymentMethod] : 'Aún no elegido' }}
              </dd>
              <template v-if="detail.paidAt">
                <dt class="text-muted">Pagada el</dt>
                <dd>
                  {{ formatDate(detail.paidAt)
                  }}<template v-if="detail.paidAmount">
                    · {{ formatMoney(detail.paidAmount, detail.paidCurrency) }}</template
                  >
                </dd>
              </template>
              <template v-if="detail.paypalCaptureId">
                <dt class="text-muted">Captura PayPal</dt>
                <dd class="font-mono text-sm">{{ detail.paypalCaptureId }}</dd>
              </template>
              <template v-if="detail.paypalOrderId">
                <dt class="text-muted">Orden PayPal</dt>
                <dd class="font-mono text-sm">{{ detail.paypalOrderId }}</dd>
              </template>
            </dl>
          </section>

          <section class="surface rounded-2xl p-5" aria-labelledby="d-data">
            <h2 id="d-data" class="text-lg font-semibold text-highlighted">Datos del cliente</h2>
            <dl class="mt-3 grid gap-x-6 gap-y-2 sm:grid-cols-[12rem_1fr]">
              <template v-for="[label, value] in rows" :key="label">
                <dt class="text-muted">{{ label }}</dt>
                <dd class="break-words whitespace-pre-line text-highlighted">
                  <a
                    v-if="label === 'Correo'"
                    :href="`mailto:${value}`"
                    class="text-primary underline underline-offset-4"
                    >{{ value }}</a
                  >
                  <template v-else>{{ value }}</template>
                </dd>
              </template>
            </dl>
          </section>
        </div>

        <div class="space-y-6">
          <section class="surface rounded-2xl p-5" aria-labelledby="d-status">
            <h2 id="d-status" class="text-lg font-semibold text-highlighted">Cambiar estado</h2>
            <form class="mt-3 space-y-4" @submit.prevent="saveStatus">
              <div>
                <label for="d-next" class="mb-1 block text-sm font-medium">Estado</label>
                <select
                  id="d-next"
                  v-model="nextStatus"
                  class="min-h-11 w-full rounded-lg border border-default bg-(--surface) px-3"
                >
                  <option v-for="o in statusOptions" :key="o.value" :value="o.value">
                    {{ o.label }}
                  </option>
                </select>
              </div>
              <div>
                <label for="d-note" class="mb-1 block text-sm font-medium">Nota</label>
                <UTextarea
                  id="d-note"
                  v-model="note"
                  :rows="3"
                  class="w-full"
                  placeholder="Ej.: transferencia recibida, boleta 12345"
                />
              </div>
              <label
                v-if="nextStatus === 'paid' && detail.status !== 'paid'"
                for="d-notify"
                class="flex min-h-11 items-center gap-3"
              >
                <input
                  id="d-notify"
                  v-model="notify"
                  type="checkbox"
                  class="size-5 accent-(--ui-primary)"
                />
                Avisar por correo al cliente y a ventas
              </label>
              <UButton
                type="submit"
                class="min-h-11"
                :loading="saving"
                label="Guardar estado"
                :disabled="nextStatus === detail.status && !note"
              />
            </form>
          </section>

          <section class="surface rounded-2xl p-5" aria-labelledby="d-history">
            <h2 id="d-history" class="text-lg font-semibold text-highlighted">Historial</h2>
            <ol class="mt-3 space-y-3">
              <li
                v-for="(e, i) in [...detail.events].reverse()"
                :key="i"
                class="border-l-2 border-default pl-3"
              >
                <p class="text-sm text-muted">{{ formatDate(e.at) }} · {{ e.actor }}</p>
                <p v-if="e.to" class="text-highlighted">
                  <template v-if="e.from && e.from !== e.to"
                    >{{ STATUS_LABELS[e.from] }} → </template
                  >{{ STATUS_LABELS[e.to] }}
                </p>
                <p v-if="e.note" class="text-toned">{{ e.note }}</p>
              </li>
            </ol>
          </section>
        </div>
      </div>
    </template>
  </div>
</template>
