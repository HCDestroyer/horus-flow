<script setup lang="ts">
// Precios y planes: borrador editable → vista previa (el mismo componente de la web) →
// publicar (versión nueva en el historial; la web lo muestra en la siguiente carga). Cualquier
// versión anterior se puede restaurar.
import type { Catalog, CatalogPlan, Currency, Localized, Period } from '#shared/catalog'

definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: 'Precios y planes' })

interface Version {
  id: number
  createdAt: string
  adminEmail: string | null
  note: string
  restoredFrom: number | null
}

const toast = useToast()
const published = ref<Catalog | null>(null)
const draft = ref<Catalog | null>(null)
const versions = ref<Version[]>([])
const note = ref('')
const saving = ref(false)
const issues = ref<{ path: string; message: string }[]>([])
const preview = ref(false)

const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v))
const dirty = computed(() => JSON.stringify(draft.value) !== JSON.stringify(published.value))

async function load() {
  const r = await adminApi<{ catalog: Catalog; versions: Version[] }>('/pricing')
  published.value = r.catalog
  draft.value = clone(r.catalog)
  versions.value = r.versions
}
onMounted(load)

const CURRENCIES: Currency[] = ['USD', 'GTQ']
const PERIODS: { id: Period; label: string }[] = [
  { id: 'monthly', label: 'Mensual' },
  { id: 'annual', label: 'Anual' },
]

function move(i: number, d: -1 | 1) {
  const plans = draft.value!.plans
  const j = i + d
  if (j < 0 || j >= plans.length) return
  ;[plans[i], plans[j]] = [plans[j]!, plans[i]!]
}
function setHighlighted(plan: CatalogPlan) {
  for (const p of draft.value!.plans) p.highlighted = p.id === plan.id ? !plan.highlighted : false
}
function addInclude(list: Localized[]) {
  list.push({ es: '', en: '' })
}
function removeInclude(list: Localized[], i: number) {
  list.splice(i, 1)
}

async function publish() {
  if (!draft.value) return
  saving.value = true
  issues.value = []
  try {
    await adminApi('/pricing', { method: 'POST', body: { catalog: draft.value, note: note.value } })
    note.value = ''
    toast.add({
      title: 'Precios publicados. La web ya muestra los nuevos.',
      color: 'primary',
      icon: 'i-lucide-check',
    })
    preview.value = false
    await load()
  } catch (err) {
    issues.value = toAdminError(err).issues ?? []
    toast.add({ title: adminErrorMessage(err), color: 'error' })
  } finally {
    saving.value = false
  }
}

async function viewVersion(id: number) {
  const r = await adminApi<{ catalog: Catalog }>(`/pricing/versions/${id}`)
  draft.value = clone(r.catalog)
  preview.value = true
  toast.add({ title: `Versión ${id} cargada en el borrador (sin publicar).`, color: 'neutral' })
}

async function restore(id: number) {
  if (!window.confirm(`¿Publicar de nuevo la versión ${id}? La web mostrará esos precios.`)) return
  try {
    await adminApi(`/pricing/versions/${id}/restore`, { method: 'POST' })
    toast.add({
      title: `Versión ${id} restaurada y publicada.`,
      color: 'primary',
      icon: 'i-lucide-check',
    })
    await load()
  } catch (err) {
    toast.add({ title: adminErrorMessage(err), color: 'error' })
  }
}

function discard() {
  draft.value = clone(published.value)
  issues.value = []
}

const ISSUE_TEXT: Record<string, string> = {
  twoDecimals: 'como mucho 2 decimales',
  oneHighlighted: 'solo un plan destacado',
  defaultCurrency: 'la moneda por defecto debe estar activa',
}
function issueText(i: { path: string; message: string }) {
  return `${i.path || 'catálogo'}: ${ISSUE_TEXT[i.message] ?? i.message}`
}
</script>

<template>
  <div>
    <AdminPageHeader
      title="Precios y planes"
      lead="Edita importes, planes y lo que incluye cada uno. Revisa la vista previa y publica: la web lo muestra en menos de un minuto, sin redesplegar."
    >
      <template #actions>
        <UButton
          color="neutral"
          variant="outline"
          class="min-h-11"
          :icon="preview ? 'i-lucide-pencil' : 'i-lucide-eye'"
          :label="preview ? 'Volver a editar' : 'Vista previa'"
          @click="preview = !preview"
        />
      </template>
    </AdminPageHeader>

    <template v-if="draft">
      <div
        class="sticky top-20 z-30 mb-6 flex flex-wrap items-center gap-3 rounded-2xl px-4 py-3 lg:top-3"
        :class="dirty ? 'glass' : 'surface'"
        role="region"
        aria-label="Publicación"
      >
        <p
          class="text-[0.95rem]"
          :class="dirty ? 'font-medium text-highlighted' : 'text-muted'"
          aria-live="polite"
        >
          {{ dirty ? 'Hay cambios sin publicar.' : 'El borrador coincide con lo publicado.' }}
        </p>
        <div class="ml-auto flex flex-wrap items-end gap-2">
          <label for="pub-note" class="sr-only">Nota de la versión</label>
          <UInput id="pub-note" v-model="note" placeholder="Nota (opcional)" class="w-56" />
          <UButton
            color="neutral"
            variant="ghost"
            class="min-h-11"
            label="Descartar"
            :disabled="!dirty"
            @click="discard"
          />
          <UButton
            class="min-h-11"
            icon="i-lucide-upload"
            label="Publicar"
            :loading="saving"
            :disabled="!dirty"
            @click="publish"
          />
        </div>
      </div>

      <UAlert
        v-if="issues.length"
        color="error"
        variant="subtle"
        title="No se pudo publicar"
        class="mb-6"
        role="alert"
      >
        <template #description>
          <ul class="list-disc pl-5">
            <li v-for="(i, k) in issues" :key="k">{{ issueText(i) }}</li>
          </ul>
        </template>
      </UAlert>

      <section
        v-if="preview"
        aria-label="Vista previa"
        class="rounded-2xl border border-dashed border-(--ui-border-accented) bg-default"
      >
        <p class="px-6 pt-5 text-sm font-medium text-muted">
          Vista previa (español). Así se verá la sección de precios en la web.
        </p>
        <PricingSection :catalog="draft" preview />
      </section>

      <div v-else class="space-y-6">
        <section class="surface rounded-2xl p-5" aria-labelledby="p-general">
          <h2 id="p-general" class="text-lg font-semibold text-highlighted">General</h2>
          <div class="mt-4 space-y-5">
            <label for="p-confirmed" class="flex min-h-11 items-center gap-3">
              <input
                id="p-confirmed"
                v-model="draft.confirmed"
                type="checkbox"
                class="size-5 accent-(--ui-primary)"
              />
              <span>
                <span class="font-medium">Mostrar importes en la web</span>
                <span class="block text-sm text-muted"
                  >Si se desactiva, la web muestra «Precio de lanzamiento: solicita cotización» y no
                  permite pagar.</span
                >
              </span>
            </label>
            <div class="flex flex-wrap gap-6">
              <div>
                <label for="p-defcur" class="mb-1 block text-sm font-medium"
                  >Moneda por defecto</label
                >
                <select
                  id="p-defcur"
                  v-model="draft.defaultCurrency"
                  class="min-h-11 rounded-lg border border-default bg-(--surface) px-3"
                >
                  <option v-for="c in draft.currencies" :key="c" :value="c">{{ c }}</option>
                </select>
              </div>
              <div>
                <label for="p-defper" class="mb-1 block text-sm font-medium"
                  >Periodo por defecto</label
                >
                <select
                  id="p-defper"
                  v-model="draft.defaultPeriod"
                  class="min-h-11 rounded-lg border border-default bg-(--surface) px-3"
                >
                  <option v-for="p in PERIODS" :key="p.id" :value="p.id">{{ p.label }}</option>
                </select>
              </div>
            </div>
            <AdminLocalizedInput
              id="p-annual"
              v-model="draft.annualNote"
              label="Nota del pago anual"
            />
            <fieldset>
              <legend class="mb-2 text-sm font-medium">Todas las licencias incluyen</legend>
              <ul class="space-y-2">
                <li
                  v-for="(inc, i) in draft.allPlansInclude"
                  :key="i"
                  class="flex items-start gap-2"
                >
                  <AdminLocalizedInput
                    :id="`p-all-${i}`"
                    v-model="draft.allPlansInclude[i]!"
                    :label="`Elemento ${i + 1}`"
                    class="flex-1"
                    required
                  />
                  <UButton
                    color="neutral"
                    variant="ghost"
                    icon="i-lucide-trash-2"
                    class="mt-6 min-h-11 min-w-11"
                    :aria-label="`Quitar elemento ${i + 1}`"
                    @click="removeInclude(draft.allPlansInclude, i)"
                  />
                </li>
              </ul>
              <UButton
                color="neutral"
                variant="outline"
                icon="i-lucide-plus"
                class="mt-2 min-h-11"
                label="Añadir"
                @click="addInclude(draft.allPlansInclude)"
              />
            </fieldset>
          </div>
        </section>

        <section
          v-for="(plan, i) in draft.plans"
          :key="plan.id"
          class="surface rounded-2xl p-5"
          :aria-labelledby="`plan-${plan.id}`"
          :data-plan-editor="plan.id"
        >
          <div class="flex flex-wrap items-center gap-3">
            <h2 :id="`plan-${plan.id}`" class="text-lg font-semibold text-highlighted">
              {{ plan.name.es || plan.id }}
              <span class="ml-1 font-mono text-sm font-normal text-muted">{{ plan.id }}</span>
            </h2>
            <span
              v-if="!plan.visible"
              class="rounded-full border border-default px-2 text-sm text-muted"
              >Oculto</span
            >
            <span
              v-if="plan.highlighted"
              class="rounded-full bg-(--ui-primary) px-2 text-sm text-(--ui-bg)"
              >Destacado</span
            >
            <div class="ml-auto flex gap-1">
              <UButton
                color="neutral"
                variant="ghost"
                icon="i-lucide-arrow-up"
                class="min-h-11 min-w-11"
                :aria-label="`Subir ${plan.name.es}`"
                :disabled="i === 0"
                @click="move(i, -1)"
              />
              <UButton
                color="neutral"
                variant="ghost"
                icon="i-lucide-arrow-down"
                class="min-h-11 min-w-11"
                :aria-label="`Bajar ${plan.name.es}`"
                :disabled="i === draft.plans.length - 1"
                @click="move(i, 1)"
              />
            </div>
          </div>

          <div class="mt-3 flex flex-wrap gap-x-6">
            <label :for="`vis-${plan.id}`" class="flex min-h-11 items-center gap-2">
              <input
                :id="`vis-${plan.id}`"
                v-model="plan.visible"
                type="checkbox"
                class="size-5 accent-(--ui-primary)"
              />
              Visible en la web
            </label>
            <label :for="`hl-${plan.id}`" class="flex min-h-11 items-center gap-2">
              <input
                :id="`hl-${plan.id}`"
                :checked="plan.highlighted"
                type="checkbox"
                class="size-5 accent-(--ui-primary)"
                @change="setHighlighted(plan)"
              />
              Plan destacado
            </label>
          </div>

          <div class="mt-4 grid gap-5">
            <AdminLocalizedInput
              :id="`name-${plan.id}`"
              v-model="plan.name"
              label="Nombre"
              required
            />
            <AdminLocalizedInput
              :id="`sum-${plan.id}`"
              v-model="plan.summary"
              label="Descripción"
              multiline
              required
            />

            <div v-if="plan.prices" class="overflow-x-auto">
              <table class="w-full max-w-xl text-left">
                <caption class="mb-2 text-left text-sm font-medium">
                  Importes
                </caption>
                <thead class="text-sm text-muted">
                  <tr>
                    <th scope="col" class="py-1 pr-3 font-medium">Moneda</th>
                    <th v-for="p in PERIODS" :key="p.id" scope="col" class="py-1 pr-3 font-medium">
                      {{ p.label }}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="c in CURRENCIES" :key="c">
                    <th scope="row" class="py-1 pr-3 font-mono font-medium">{{ c }}</th>
                    <td v-for="p in PERIODS" :key="p.id" class="py-1 pr-3">
                      <label :for="`price-${plan.id}-${c}-${p.id}`" class="sr-only"
                        >{{ plan.name.es }}, {{ c }}, {{ p.label }}</label
                      >
                      <UInput
                        :id="`price-${plan.id}-${c}-${p.id}`"
                        v-model.number="plan.prices[c][p.id]"
                        type="number"
                        min="0.01"
                        step="0.01"
                        inputmode="decimal"
                        class="w-36 tabular"
                      />
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
            <p v-else class="text-muted">
              Plan a medida: sin importe ni compra en la web (botón «Hablar con ventas»).
            </p>

            <fieldset>
              <legend class="mb-2 text-sm font-medium">Qué incluye</legend>
              <ul class="space-y-2">
                <li v-for="(inc, k) in plan.includes" :key="k" class="flex items-start gap-2">
                  <AdminLocalizedInput
                    :id="`inc-${plan.id}-${k}`"
                    v-model="plan.includes[k]!"
                    :label="`Elemento ${k + 1}`"
                    class="flex-1"
                    required
                  />
                  <UButton
                    color="neutral"
                    variant="ghost"
                    icon="i-lucide-trash-2"
                    class="mt-6 min-h-11 min-w-11"
                    :aria-label="`Quitar elemento ${k + 1} de ${plan.name.es}`"
                    @click="removeInclude(plan.includes, k)"
                  />
                </li>
              </ul>
              <UButton
                color="neutral"
                variant="outline"
                icon="i-lucide-plus"
                class="mt-2 min-h-11"
                label="Añadir"
                @click="addInclude(plan.includes)"
              />
            </fieldset>
            <AdminLocalizedInput
              v-if="plan.server"
              :id="`srv-${plan.id}`"
              v-model="plan.server"
              label="Servidor recomendado"
            />
          </div>
        </section>
      </div>

      <section class="surface mt-8 rounded-2xl" aria-labelledby="p-history">
        <h2 id="p-history" class="px-5 pt-5 text-lg font-semibold text-highlighted">
          Historial de versiones
        </h2>
        <div class="mt-3 overflow-x-auto">
          <table class="w-full text-left text-[0.95rem]">
            <thead class="border-y border-default bg-muted text-sm text-muted">
              <tr>
                <th scope="col" class="px-5 py-2 font-medium">Versión</th>
                <th scope="col" class="px-5 py-2 font-medium">Fecha</th>
                <th scope="col" class="px-5 py-2 font-medium">Quién</th>
                <th scope="col" class="px-5 py-2 font-medium">Nota</th>
                <th scope="col" class="px-5 py-2 font-medium">
                  <span class="sr-only">Acciones</span>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="(v, k) in versions"
                :key="v.id"
                class="border-b border-default last:border-0"
              >
                <td class="px-5 py-2 font-mono">
                  {{ v.id }}
                  <span
                    v-if="k === 0"
                    class="ml-1 rounded-full bg-(--ui-primary)/12 px-2 font-sans text-sm text-highlighted"
                    >Publicada</span
                  >
                </td>
                <td class="px-5 py-2 whitespace-nowrap text-muted">
                  {{ formatDate(v.createdAt) }}
                </td>
                <td class="px-5 py-2">{{ v.adminEmail ?? '—' }}</td>
                <td class="px-5 py-2 text-toned">
                  {{ v.note || (v.restoredFrom ? `Restaurada de la ${v.restoredFrom}` : '—') }}
                </td>
                <td class="px-5 py-2 whitespace-nowrap text-right">
                  <UButton
                    color="neutral"
                    variant="ghost"
                    class="min-h-11"
                    label="Ver"
                    :aria-label="`Ver versión ${v.id}`"
                    @click="viewVersion(v.id)"
                  />
                  <UButton
                    v-if="k > 0"
                    color="neutral"
                    variant="outline"
                    class="min-h-11"
                    label="Restaurar"
                    :aria-label="`Restaurar versión ${v.id}`"
                    @click="restore(v.id)"
                  />
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </template>
  </div>
</template>
