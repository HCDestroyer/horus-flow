<script setup lang="ts">
definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: 'Solicitudes' })

interface Item {
  id: number
  reference: string
  kind: 'demo' | 'contact' | 'purchase'
  status: string
  createdAt: string
  name: string
  company: string
  email: string
  plan: string | null
  period: string | null
  currency: string | null
  amount: number | null
  paymentMethod: string | null
}

const route = useRoute()
const kind = ref<'all' | 'demo' | 'purchase'>((route.query.kind as 'demo') ?? 'all')
const status = ref<string>((route.query.status as string) ?? 'all')
const q = ref<string>((route.query.q as string) ?? '')
const page = ref(1)
const data = ref<{ items: Item[]; total: number; pageSize: number } | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

const kindOptions = [
  { value: 'all' as const, label: 'Todas' },
  { value: 'demo' as const, label: 'Demos' },
  { value: 'purchase' as const, label: 'Compras' },
]
const statusOptions = [
  { value: 'all', label: 'Todos los estados' },
  ...Object.entries(STATUS_LABELS).map(([value, label]) => ({ value, label })),
]
const KIND: Record<string, string> = { demo: 'Demo', contact: 'Contacto', purchase: 'Compra' }

async function load() {
  loading.value = true
  error.value = null
  try {
    data.value = await adminApi('/requests', {
      query: { kind: kind.value, status: status.value, q: q.value, page: page.value },
    })
  } catch (err) {
    error.value = adminErrorMessage(err)
  } finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
watch([kind, status], () => {
  page.value = 1
  load()
})
watch(q, () => {
  clearTimeout(timer)
  timer = setTimeout(() => {
    page.value = 1
    load()
  }, 300)
})
watch(page, load)
onMounted(load)

const exportUrl = computed(
  () =>
    `/api/admin/requests/export?${new URLSearchParams({ kind: kind.value, status: status.value, q: q.value }).toString()}`,
)
const pages = computed(() =>
  data.value ? Math.max(1, Math.ceil(data.value.total / data.value.pageSize)) : 1,
)
</script>

<template>
  <div>
    <AdminPageHeader
      title="Solicitudes"
      lead="Demos, contactos y compras recibidos desde la web. Solo los ve un administrador con sesión."
    >
      <template #actions>
        <UButton
          :to="exportUrl"
          external
          download
          color="neutral"
          variant="outline"
          icon="i-lucide-download"
          class="min-h-11"
          label="Exportar CSV"
        />
      </template>
    </AdminPageHeader>

    <div class="mb-4 flex flex-wrap items-end gap-3">
      <SegmentedControl
        v-model="kind"
        solid
        name="req-kind"
        legend="Tipo de solicitud"
        :options="kindOptions"
      />
      <div>
        <label for="req-status" class="mb-1 block text-sm font-medium">Estado</label>
        <select
          id="req-status"
          v-model="status"
          class="min-h-11 rounded-lg border border-default bg-(--surface) px-3 text-default"
        >
          <option v-for="o in statusOptions" :key="o.value" :value="o.value">{{ o.label }}</option>
        </select>
      </div>
      <div class="min-w-56 flex-1">
        <label for="req-q" class="mb-1 block text-sm font-medium">Buscar</label>
        <UInput
          id="req-q"
          v-model="q"
          type="search"
          placeholder="Referencia, nombre, empresa o correo"
          icon="i-lucide-search"
          class="w-full"
        />
      </div>
    </div>

    <UAlert v-if="error" color="error" variant="subtle" :title="error" role="alert" class="mb-4" />

    <div class="surface overflow-hidden rounded-2xl">
      <p class="sr-only" aria-live="polite">
        {{ loading ? 'Cargando' : `${data?.total ?? 0} solicitudes` }}
      </p>
      <!-- Tabla (pantallas medianas y grandes) -->
      <div class="hidden overflow-x-auto md:block">
        <table class="w-full text-left text-[0.95rem]">
          <caption class="sr-only">
            Solicitudes
          </caption>
          <thead class="border-b border-default bg-muted text-sm text-muted">
            <tr>
              <th scope="col" class="px-4 py-3 font-medium">Fecha</th>
              <th scope="col" class="px-4 py-3 font-medium">Referencia</th>
              <th scope="col" class="px-4 py-3 font-medium">Tipo</th>
              <th scope="col" class="px-4 py-3 font-medium">Cliente</th>
              <th scope="col" class="px-4 py-3 font-medium">Plan</th>
              <th scope="col" class="px-4 py-3 text-right font-medium">Importe</th>
              <th scope="col" class="px-4 py-3 font-medium">Pago</th>
              <th scope="col" class="px-4 py-3 font-medium">Estado</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="r in data?.items ?? []"
              :key="r.id"
              class="border-b border-default last:border-0 hover:bg-elevated"
            >
              <td class="px-4 py-2.5 whitespace-nowrap text-muted tabular">
                {{ formatDate(r.createdAt) }}
              </td>
              <td class="px-4 py-2.5">
                <NuxtLink
                  :to="`/admin/solicitudes/${r.id}`"
                  class="inline-flex min-h-11 items-center font-mono font-medium text-primary underline-offset-4 hover:underline"
                >
                  {{ r.reference }}
                </NuxtLink>
              </td>
              <td class="px-4 py-2.5">{{ KIND[r.kind] }}</td>
              <td class="max-w-64 px-4 py-2.5">
                <span class="block truncate font-medium text-highlighted">{{ r.company }}</span>
                <span class="block truncate text-sm text-muted">{{ r.name }} · {{ r.email }}</span>
              </td>
              <td class="px-4 py-2.5 whitespace-nowrap">
                {{ r.plan ? `${r.plan} · ${r.period === 'annual' ? 'anual' : 'mensual'}` : '—' }}
              </td>
              <td class="px-4 py-2.5 text-right whitespace-nowrap tabular">
                {{ formatMoney(r.amount, r.currency) }}
              </td>
              <td class="px-4 py-2.5">
                {{ r.paymentMethod ? METHOD_LABELS[r.paymentMethod] : '—' }}
              </td>
              <td class="px-4 py-2.5"><AdminStatus :status="r.status" /></td>
            </tr>
          </tbody>
        </table>
      </div>
      <!-- Lista (móvil) -->
      <ul class="divide-y divide-(--ui-border) md:hidden">
        <li v-for="r in data?.items ?? []" :key="r.id">
          <NuxtLink
            :to="`/admin/solicitudes/${r.id}`"
            class="block min-h-11 px-4 py-3 hover:bg-elevated"
          >
            <span class="flex items-center justify-between gap-2">
              <span class="font-mono text-sm font-medium text-primary">{{ r.reference }}</span>
              <AdminStatus :status="r.status" />
            </span>
            <span class="mt-1 block font-medium text-highlighted">{{ r.company }}</span>
            <span class="block text-sm text-muted">
              {{ KIND[r.kind] }} · {{ formatDate(r.createdAt) }}
              <template v-if="r.amount !== null">
                · {{ formatMoney(r.amount, r.currency) }}</template
              >
            </span>
          </NuxtLink>
        </li>
      </ul>
      <p v-if="data && data.items.length === 0" class="px-4 py-10 text-center text-muted">
        No hay solicitudes con estos filtros.
      </p>
    </div>

    <nav
      v-if="pages > 1"
      aria-label="Paginación"
      class="mt-4 flex items-center justify-between gap-3"
    >
      <UButton
        color="neutral"
        variant="outline"
        class="min-h-11"
        icon="i-lucide-chevron-left"
        label="Anterior"
        :disabled="page <= 1"
        @click="page--"
      />
      <span class="text-sm text-muted"
        >Página {{ page }} de {{ pages }} · {{ data?.total }} solicitudes</span
      >
      <UButton
        color="neutral"
        variant="outline"
        class="min-h-11"
        trailing-icon="i-lucide-chevron-right"
        label="Siguiente"
        :disabled="page >= pages"
        @click="page++"
      />
    </nav>
  </div>
</template>
