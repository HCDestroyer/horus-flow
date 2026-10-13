<script setup lang="ts">
import type { AsnInfo, AsnRule } from '~~/types/api'

/**
 * Administración › ASN propios: el ISP nombra un ASN (nombre visible, categoría y, opcional, un
 * servicio del catálogo) y esa regla gana al catálogo de plataforma al clasificar el tráfico que
 * llega desde entonces (`/asn-rules`, `local_override`). Al escribir el ASN se muestra cómo lo
 * nombra hoy el dataset público (`GET /asn/{asn}`). `?asn=NNN` abre el formulario con ese ASN
 * (enlace "Nombrar este ASN" del desglose de Tráfico).
 */
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const { $api } = useNuxtApp()
const toast = useToast()
const can = useCan()
const slug = computed(() => String(route.params.slug))
useSectionGuard()(findSection('tenant', 'admin/asn'), slug.value)
const canManage = computed(() => can('settings.manage'))
const canTraffic = computed(() => can('traffic.read'))

const {
  data: rules,
  error: rulesError,
  status: rulesStatus,
  refresh,
} = useTenantQuery('asn-rules', () => unwrap($api.GET('/asn-rules')).then((r) => r.data))
const { data: catalog } = useTenantQuery('asn-rules-catalog', () =>
  unwrap($api.GET('/asn-rules/catalog')),
)

const categoryName = (slug: string) =>
  catalog.value?.categories.find((c) => c.slug === slug)?.name ?? slug
const serviceName = (slug?: string | null) =>
  slug ? (catalog.value?.services.find((s) => s.slug === slug)?.name ?? slug) : null

// --- Búsqueda en la lista ----------------------------------------------------------------
const search = ref('')
const filtered = computed(() => {
  const q = search.value.trim().toLowerCase().replace(/^as/, '')
  const list = rules.value ?? []
  if (!q) return list
  return list.filter(
    (r) =>
      String(r.asn).includes(q) ||
      r.name.toLowerCase().includes(q) ||
      categoryName(r.category).toLowerCase().includes(q),
  )
})

// --- Formulario (alta y edición) ---------------------------------------------------------
/** Valor del selector de servicio cuando la regla no usa uno del catálogo. */
const OWN = '__own__'
const formOpen = ref(false)
const editing = ref<AsnRule | null>(null)
const form = reactive({ asn: '', name: '', category: '', service: OWN, note: '' })
const formErrors = ref<Record<string, string>>({})
const saving = ref(false)

const serviceItems = computed(() => [
  { label: t('asnRules.form.ownService'), value: OWN },
  ...(catalog.value?.services ?? []).map((s) => ({
    label: `${s.name} · ${categoryName(s.category)}`,
    value: s.slug,
  })),
])
const categoryItems = computed(() =>
  (catalog.value?.categories ?? []).map((c) => ({ label: c.name, value: c.slug })),
)
const serviceCategory = computed(() =>
  form.service === OWN
    ? null
    : (catalog.value?.services.find((s) => s.slug === form.service)?.category ?? null),
)
// Con un servicio del catálogo, la categoría es la del servicio (contrato AsnRuleInput).
watch(serviceCategory, (c) => {
  if (c) form.category = c
})

function parseAsn(v: string) {
  const n = Number(v.trim().replace(/^AS/i, ''))
  return Number.isInteger(n) && n >= 1 && n <= 4294967295 ? n : null
}

// Cómo se llama hoy el ASN (dataset público, catálogo y regla del ISP).
const lookup = ref<AsnInfo | null>(null)
const lookupLoading = ref(false)
let lookupTimer: ReturnType<typeof setTimeout> | undefined
let lookupSeq = 0
watch(
  () => form.asn,
  (v) => {
    clearTimeout(lookupTimer)
    lookup.value = null
    const n = parseAsn(v)
    if (!n || !formOpen.value || !canTraffic.value) return
    lookupTimer = setTimeout(async () => {
      const seq = ++lookupSeq
      lookupLoading.value = true
      try {
        const info = await unwrap($api.GET('/asn/{asn}', { params: { path: { asn: n } } }))
        if (seq !== lookupSeq) return
        lookup.value = info
        // Al nombrar un ASN nuevo se propone el nombre que ya tiene en el dataset.
        if (!editing.value && !form.name.trim() && info.name) form.name = info.name
      } catch {
        if (seq === lookupSeq) lookup.value = null
      } finally {
        if (seq === lookupSeq) lookupLoading.value = false
      }
    }, 300)
  },
)
const existingRule = computed(() => {
  const n = parseAsn(form.asn)
  return n && !editing.value ? (rules.value ?? []).find((r) => r.asn === n) : undefined
})

function openForm(rule?: AsnRule, asn?: string) {
  editing.value = rule ?? null
  formErrors.value = {}
  lookup.value = null
  Object.assign(form, {
    asn: rule ? String(rule.asn) : (asn ?? ''),
    name: rule?.name ?? '',
    category: rule?.category ?? '',
    service: rule?.service ?? OWN,
    note: rule?.note ?? '',
  })
  formOpen.value = true
  // El watcher de form.asn no se dispara si el valor no cambia: fuerza la búsqueda.
  if (form.asn) {
    const v = form.asn
    form.asn = ''
    nextTick(() => (form.asn = v))
  }
}

async function save() {
  formErrors.value = {}
  const errors: Record<string, string> = {}
  const asn = parseAsn(form.asn)
  if (!editing.value && !asn) errors.asn = t('asnRules.form.asnInvalid')
  if (!form.name.trim()) errors.name = t('asnRules.form.nameRequired')
  if (form.service === OWN && !form.category) errors.category = t('asnRules.form.categoryRequired')
  if (Object.keys(errors).length) {
    formErrors.value = errors
    return
  }
  const service = form.service === OWN ? null : form.service
  const note = form.note.trim() || null
  saving.value = true
  try {
    if (editing.value) {
      await unwrap(
        $api.PATCH('/asn-rules/{asn_rule_id}', {
          params: {
            path: { asn_rule_id: editing.value.id },
            header: { 'If-Match': ifMatch(editing.value.version) },
          },
          body: {
            name: form.name.trim(),
            service,
            note,
            ...(service ? {} : { category: form.category }),
          },
        }),
      )
      toast.add({ title: t('asnRules.saved'), icon: 'i-lucide-check', color: 'success' })
    } else {
      await unwrap(
        $api.POST('/asn-rules', {
          body: {
            asn: asn!,
            name: form.name.trim(),
            service,
            note,
            ...(service ? {} : { category: form.category }),
          },
        }),
      )
      toast.add({ title: t('asnRules.created'), icon: 'i-lucide-check', color: 'success' })
    }
    formOpen.value = false
    refresh()
  } catch (e) {
    if (e instanceof ApiError && e.problem.errors?.length) {
      formErrors.value = Object.fromEntries(e.problem.errors.map((x) => [x.field, x.message]))
    } else if (e instanceof ApiError && e.status === 412) {
      formErrors.value = { _: t('asnRules.conflict') }
      refresh()
    } else formErrors.value = { _: t(describeError(e).messageKey) }
  } finally {
    saving.value = false
  }
}

// --- Borrar ------------------------------------------------------------------------------
const removing = ref<AsnRule | null>(null)
const removeOpen = computed({
  get: () => !!removing.value,
  set: (v) => {
    if (!v) removing.value = null
  },
})
async function remove() {
  const r = removing.value
  if (!r) return
  try {
    await unwrap(
      $api.DELETE('/asn-rules/{asn_rule_id}', {
        params: { path: { asn_rule_id: r.id }, header: { 'If-Match': ifMatch(r.version) } },
      }),
    )
    toast.add({ title: t('asnRules.removed'), icon: 'i-lucide-check', color: 'success' })
  } catch (e) {
    toast.add({
      title:
        e instanceof ApiError && e.status === 412
          ? t('asnRules.conflict')
          : t(describeError(e).messageKey),
      color: 'error',
    })
  }
  removing.value = null
  refresh()
}

// ?asn=NNN (desde Tráfico): abre el alta con ese ASN, o la edición si ya tiene regla.
const prefillDone = ref(false)
watch(
  [() => route.query.asn, rules, canManage],
  ([q, list]) => {
    if (prefillDone.value || !q || !canManage.value || !list) return
    prefillDone.value = true
    const asn = parseAsn(String(q))
    const existing = asn ? list.find((r) => r.asn === asn) : undefined
    openForm(existing, asn ? String(asn) : undefined)
    const { asn: _drop, ...rest } = route.query
    router.replace({ query: rest })
  },
  { immediate: true },
)
</script>

<template>
  <AppPage :title="t('nav.items.asn')" panel-id="asn">
    <template #toolbar-left>
      <p class="text-muted text-sm">{{ t('asnRules.intro') }}</p>
    </template>

    <UAlert
      color="info"
      variant="subtle"
      icon="i-lucide-info"
      :title="t('asnRules.scope.title')"
      :description="t('asnRules.scope.body')"
      data-testid="asn-scope"
    />

    <section class="flex flex-col gap-3" data-testid="asn-rules">
      <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <UInput
          v-model="search"
          icon="i-lucide-search"
          :placeholder="t('asnRules.search')"
          :aria-label="t('asnRules.search')"
          class="w-full sm:w-72"
          data-testid="asn-search"
        />
        <UButton
          v-if="canManage"
          icon="i-lucide-plus"
          :label="t('asnRules.add')"
          class="self-start sm:self-auto"
          data-testid="asn-add"
          @click="openForm()"
        />
      </div>

      <LoadingState v-if="rulesStatus === 'pending' && !rules" :rows="3" />
      <ErrorState v-else-if="rulesError" :error="rulesError" @retry="refresh()" />
      <EmptyState
        v-else-if="!rules?.length"
        icon="i-lucide-network"
        :title="t('asnRules.empty.title')"
        :description="t('asnRules.empty.body')"
      />
      <p v-else-if="!filtered.length" class="text-muted text-sm" data-testid="asn-no-match">
        {{ t('asnRules.noMatch') }}
      </p>
      <ul v-else class="divide-default ring-default divide-y rounded-md ring-1">
        <li
          v-for="r in filtered"
          :key="r.id"
          class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 px-3 py-2.5"
          :data-asn-rule="r.asn"
        >
          <div class="min-w-0">
            <p class="flex flex-wrap items-baseline gap-x-2">
              <span class="text-highlighted font-mono font-medium">AS{{ r.asn }}</span>
              <span class="text-highlighted" data-testid="asn-rule-name">{{ r.name }}</span>
            </p>
            <p class="text-muted text-sm">
              {{ categoryName(r.category) }} ·
              {{
                serviceName(r.service)
                  ? t('asnRules.serviceCatalog', { name: serviceName(r.service) })
                  : t('asnRules.serviceOwn')
              }}
            </p>
            <p v-if="r.note" class="text-dimmed text-xs break-words">{{ r.note }}</p>
          </div>
          <span v-if="canManage" class="flex shrink-0 gap-1">
            <UButton
              size="sm"
              color="neutral"
              variant="ghost"
              icon="i-lucide-pencil"
              :label="t('asnRules.edit')"
              data-testid="asn-edit"
              @click="openForm(r)"
            />
            <UButton
              size="sm"
              color="neutral"
              variant="ghost"
              icon="i-lucide-trash-2"
              :label="t('asnRules.remove')"
              data-testid="asn-remove"
              @click="removing = r"
            />
          </span>
        </li>
      </ul>
    </section>

    <USlideover
      v-model:open="formOpen"
      :title="
        editing ? t('asnRules.form.editTitle', { asn: editing.asn }) : t('asnRules.form.title')
      "
      :description="t('asnRules.form.body')"
    >
      <template #body>
        <form id="asn-form" class="flex flex-col gap-4" @submit.prevent="save">
          <UFormField
            :label="t('asnRules.form.asn')"
            :error="formErrors.asn"
            :help="editing ? t('asnRules.form.asnFixed') : undefined"
            required
          >
            <UInput
              v-model="form.asn"
              class="w-full"
              :ui="{ base: 'font-mono' }"
              placeholder="AS263237"
              inputmode="numeric"
              :disabled="!!editing"
              data-testid="asn-input"
            />
          </UFormField>
          <div
            v-if="lookup || lookupLoading"
            class="bg-elevated rounded-md px-3 py-2 text-sm"
            data-testid="asn-lookup"
          >
            <p v-if="lookupLoading && !lookup" class="text-muted">
              {{ t('asnRules.lookup.loading') }}
            </p>
            <template v-else-if="lookup">
              <p class="text-muted">
                {{ t('asnRules.lookup.dataset') }}
                <span class="text-highlighted" data-testid="asn-lookup-name">{{
                  lookup.name ?? t('asnRules.lookup.unknown')
                }}</span>
                <span v-if="lookup.country"> · {{ lookup.country }}</span>
              </p>
              <p v-if="lookup.catalog_organization" class="text-muted">
                {{ t('asnRules.lookup.catalog', { name: lookup.catalog_organization.name }) }}
              </p>
            </template>
          </div>
          <UAlert
            v-if="existingRule"
            color="warning"
            variant="subtle"
            icon="i-lucide-triangle-alert"
            :title="t('asnRules.form.exists', { name: existingRule.name })"
            data-testid="asn-exists"
          />
          <UFormField
            :label="t('asnRules.form.name')"
            :help="t('asnRules.form.nameHelp')"
            :error="formErrors.name"
            required
          >
            <UInput v-model="form.name" class="w-full" maxlength="80" data-testid="asn-name" />
          </UFormField>
          <UFormField
            :label="t('asnRules.form.service')"
            :help="t('asnRules.form.serviceHelp')"
            :error="formErrors.service"
          >
            <USelectMenu
              v-model="form.service"
              :items="serviceItems"
              value-key="value"
              class="w-full"
              :search-input="{ placeholder: t('asnRules.form.serviceSearch') }"
              data-testid="asn-service"
            />
          </UFormField>
          <UFormField
            :label="t('asnRules.form.category')"
            :help="serviceCategory ? t('asnRules.form.categoryFromService') : undefined"
            :error="formErrors.category"
            required
          >
            <USelect
              v-model="form.category"
              :items="categoryItems"
              :placeholder="t('asnRules.form.categoryPlaceholder')"
              :disabled="!!serviceCategory"
              class="w-full"
              data-testid="asn-category"
            />
          </UFormField>
          <UFormField :label="t('asnRules.form.note')" :error="formErrors.note">
            <UTextarea
              v-model="form.note"
              :rows="2"
              class="w-full"
              maxlength="300"
              data-testid="asn-note"
            />
          </UFormField>
          <p class="text-muted text-sm">{{ t('asnRules.form.newTrafficOnly') }}</p>
          <UAlert v-if="formErrors._" color="error" variant="subtle" :title="formErrors._" />
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="ghost"
            :label="t('common.cancel')"
            @click="formOpen = false"
          />
          <UButton
            type="submit"
            form="asn-form"
            :loading="saving"
            :label="editing ? t('common.save') : t('asnRules.form.submit')"
            data-testid="asn-submit"
          />
        </div>
      </template>
    </USlideover>

    <UModal
      v-model:open="removeOpen"
      :title="t('asnRules.removeTitle', { asn: removing?.asn ?? '' })"
      :description="t('asnRules.removeBody')"
    >
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            :label="t('common.cancel')"
            autofocus
            @click="removing = null"
          />
          <UButton
            color="error"
            icon="i-lucide-trash-2"
            :label="t('asnRules.remove')"
            data-testid="asn-remove-confirm"
            @click="remove"
          />
        </div>
      </template>
    </UModal>
  </AppPage>
</template>
