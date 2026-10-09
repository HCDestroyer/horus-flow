<script setup lang="ts">
import type {
  ClientPrefix,
  ClientPrefixInput,
  ClientPrefixRole,
  PrefixProposal,
} from '~~/types/api'

/**
 * Prefijos de clientes de un nodo (I1-19, frontend.md §8.4; traffic-model.md §4.1): tabla con
 * prefijo, rol, asignación, origen y clientes; alta manual con solapes en línea
 * (`CLIENT_PREFIX_OVERLAP`); importación desde el MikroTik; modo descubrimiento con
 * propuestas. Quitar un prefijo avisa de cuántos clientes pasarán a Inactivo.
 */
const props = defineProps<{ siteId: string; routerId?: string | null; routerReady?: boolean }>()
const { t } = useI18n()
const { $api } = useNuxtApp()
const can = useCan()
const toast = useToast()
const canEdit = computed(() => can('sites.update'))

const {
  data: prefixes,
  refresh,
  status,
  error,
} = useTenantQuery(
  () => `prefixes:${props.siteId}`,
  () =>
    unwrap(
      $api.GET('/sites/{site_id}/client-prefixes', { params: { path: { site_id: props.siteId } } }),
    ).then((r) => r.data),
)
const customersPrefixes = computed(() =>
  (prefixes.value ?? []).filter((p) => p.role === 'customers'),
)
const discovery = computed(() => status.value === 'success' && customersPrefixes.value.length === 0)

const { data: proposals, refresh: refreshProposals } = useTenantQuery(
  () => `proposals:${props.siteId}`,
  () =>
    unwrap(
      $api.GET('/sites/{site_id}/prefix-proposals', {
        params: { path: { site_id: props.siteId }, query: { range: '7d' } },
      }),
    )
      .then((r) => r.data)
      .catch(() => [] as PrefixProposal[]),
)

// --- Alta manual -------------------------------------------------------------------------
const adding = ref(false)
const form = reactive({
  prefix: '',
  role: 'customers' as ClientPrefixRole,
  assignment: 'unknown' as 'static' | 'dynamic' | 'unknown',
  clientLen: 64 as 48 | 56 | 60 | 64,
  note: '',
})
const formError = ref<string | null>(null)
const saving = ref(false)
const isV6 = computed(() => form.prefix.includes(':'))

function startAdd() {
  Object.assign(form, {
    prefix: '',
    role: 'customers',
    assignment: 'unknown',
    clientLen: 64,
    note: '',
  })
  formError.value = null
  adding.value = true
}

async function create(items: ClientPrefixInput[], single: boolean) {
  if (single) {
    return [
      await unwrap(
        $api.POST('/sites/{site_id}/client-prefixes', {
          params: { path: { site_id: props.siteId } },
          body: items[0]!,
        }),
      ),
    ]
  }
  return (
    await unwrap(
      $api.POST('/sites/{site_id}/client-prefixes/batch', {
        params: {
          path: { site_id: props.siteId },
          header: { 'Idempotency-Key': idempotencyKey() },
        },
        body: { items },
      }),
    )
  ).data
}

async function save() {
  formError.value = null
  if (!form.prefix.trim()) {
    formError.value = t('prefixes.form.required')
    return
  }
  saving.value = true
  try {
    await create(
      [
        {
          prefix: form.prefix.trim(),
          role: form.role,
          assignment_mode: form.assignment,
          ipv6_client_len: isV6.value ? form.clientLen : null,
          note: form.note.trim() || null,
          source: 'manual',
        },
      ],
      true,
    )
    adding.value = false
    toast.add({ title: t('prefixes.form.done'), icon: 'i-lucide-check', color: 'success' })
    refresh()
    refreshProposals()
  } catch (e) {
    formError.value =
      e instanceof ApiError
        ? (e.problem.errors?.[0]?.message ?? e.problem.title)
        : t('errors.generic.title')
  } finally {
    saving.value = false
  }
}

// --- Propuestas del modo descubrimiento -----------------------------------------------------
const accepting = ref<string | null>(null)
async function accept(p: PrefixProposal, role: ClientPrefixRole) {
  accepting.value = p.prefix
  try {
    await create(
      [
        {
          prefix: p.prefix,
          role,
          source: 'discovery_confirmed',
          assignment_mode: 'unknown',
          ipv6_client_len: p.prefix.includes(':') ? 64 : null,
        },
      ],
      false,
    )
    toast.add({
      title: t('prefixes.discovery.accepted', {
        prefix: p.prefix,
        role: t(`prefixes.role.${role}`),
      }),
      icon: 'i-lucide-check',
      color: 'success',
    })
    refresh()
    refreshProposals()
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  } finally {
    accepting.value = null
  }
}

// --- Quitar --------------------------------------------------------------------------------
const removing = ref<ClientPrefix | null>(null)
const removeOpen = computed({
  get: () => !!removing.value,
  set: (v) => {
    if (!v) removing.value = null
  },
})
async function remove() {
  const p = removing.value
  if (!p) return
  try {
    await unwrap(
      $api.DELETE('/client-prefixes/{client_prefix_id}', {
        params: { path: { client_prefix_id: p.id }, header: { 'If-Match': ifMatch(p.version) } },
      }),
    )
    toast.add({ title: t('prefixes.remove.done'), icon: 'i-lucide-check', color: 'success' })
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  }
  removing.value = null
  refresh()
}

const importOpen = ref(false)
function imported() {
  refresh()
  refreshProposals()
}

const roleItems = computed(() =>
  (['customers', 'infrastructure', 'excluded'] as const).map((r) => ({
    label: t(`prefixes.role.${r}`),
    value: r,
  })),
)
const assignmentItems = computed(() =>
  (['dynamic', 'static', 'unknown'] as const).map((a) => ({
    label: t(`prefixes.assignment.${a}`),
    value: a,
  })),
)
const lenItems = [48, 56, 60, 64].map((n) => ({ label: `/${n}`, value: n }))
</script>

<template>
  <section class="flex flex-col gap-4" data-testid="prefixes-panel">
    <UAlert
      v-if="discovery"
      color="info"
      variant="subtle"
      icon="i-lucide-radar"
      :title="t('prefixes.discovery.title')"
      :description="t('prefixes.discovery.body')"
      data-testid="discovery-banner"
    />

    <div class="flex flex-wrap items-center justify-between gap-2">
      <h2 class="text-highlighted text-base font-semibold">{{ t('prefixes.title') }}</h2>
      <div v-if="canEdit" class="flex flex-wrap gap-2">
        <UTooltip
          :text="routerReady === false ? t('prefixes.import.needsTunnel') : undefined"
          :disabled="routerReady !== false"
        >
          <UButton
            v-if="routerId"
            color="neutral"
            variant="outline"
            icon="i-lucide-download"
            :label="t('prefixes.import.action')"
            :aria-disabled="routerReady === false || undefined"
            :disabled="routerReady === false"
            data-testid="import-prefixes"
            @click="importOpen = true"
          />
        </UTooltip>
        <UButton
          icon="i-lucide-plus"
          :label="t('prefixes.form.action')"
          data-testid="add-prefix"
          @click="startAdd"
        />
      </div>
    </div>

    <UCard v-if="adding" data-testid="prefix-form">
      <form class="flex flex-col gap-4" @submit.prevent="save">
        <div class="grid gap-3 md:grid-cols-[minmax(0,2fr)_minmax(0,1fr)_minmax(0,1fr)]">
          <UFormField :label="t('prefixes.col.prefix')" :error="formError ?? undefined" required>
            <UInput
              v-model="form.prefix"
              class="w-full"
              :ui="{ base: 'font-mono' }"
              :placeholder="t('prefixes.form.placeholder')"
              autocomplete="off"
              data-testid="prefix-input"
            />
          </UFormField>
          <UFormField :label="t('prefixes.col.assignment')">
            <USelect v-model="form.assignment" :items="assignmentItems" class="w-full" />
          </UFormField>
          <UFormField
            v-if="isV6"
            :label="t('prefixes.col.clientLen')"
            :help="t('prefixes.clientLenHelp')"
          >
            <USelect v-model="form.clientLen" :items="lenItems" class="w-full" />
          </UFormField>
        </div>
        <UFormField :label="t('prefixes.col.role')">
          <URadioGroup
            v-model="form.role"
            :items="roleItems"
            orientation="horizontal"
            data-testid="prefix-role"
          />
        </UFormField>
        <UFormField :label="t('prefixes.col.note')">
          <UInput v-model="form.note" class="w-full" maxlength="120" />
        </UFormField>
        <div class="flex justify-end gap-2">
          <UButton
            color="neutral"
            variant="ghost"
            :label="t('common.cancel')"
            @click="adding = false"
          />
          <UButton
            type="submit"
            :loading="saving"
            :label="t('prefixes.form.submit')"
            data-testid="prefix-submit"
          />
        </div>
      </form>
    </UCard>

    <LoadingState v-if="status === 'pending' && !prefixes" :rows="3" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <UCard v-else-if="!prefixes?.length" class="py-4">
      <EmptyState
        icon="i-lucide-list-tree"
        :title="t('prefixes.empty')"
        :description="t('prefixes.emptyHint')"
      />
    </UCard>
    <ul v-else class="flex flex-col gap-2" data-testid="prefixes-list">
      <li
        v-for="p in prefixes"
        :key="p.id"
        class="bg-default ring-default flex flex-col gap-2 rounded-md p-3 ring-1 md:flex-row md:items-center md:justify-between"
        :data-prefix="p.prefix"
      >
        <div class="flex min-w-0 flex-col gap-1">
          <span class="text-highlighted font-mono break-all">{{ p.prefix }}</span>
          <span class="text-muted flex flex-wrap gap-x-3 gap-y-0.5 text-xs">
            <span>{{ t(`prefixes.assignment.${p.assignment_mode ?? 'unknown'}`) }}</span>
            <span>{{ t(`prefixes.source.${p.source ?? 'manual'}`) }}</span>
            <span v-if="p.ipv6_client_len">{{
              t('prefixes.clientLen', { len: p.ipv6_client_len })
            }}</span>
            <span v-if="p.note">{{ p.note }}</span>
          </span>
        </div>
        <div class="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-1">
          <PrefixRoleBadge :role="p.role" />
          <span v-if="p.role === 'customers'" class="text-muted text-sm tabular">
            {{ t('prefixes.customers', { n: formatNumber(p.customers_count ?? 0) }) }}
          </span>
          <UButton
            v-if="canEdit"
            color="neutral"
            variant="ghost"
            size="sm"
            icon="i-lucide-trash-2"
            :aria-label="t('prefixes.remove.action', { prefix: p.prefix })"
            @click="removing = p"
          />
        </div>
      </li>
    </ul>

    <section v-if="proposals?.length" class="flex flex-col gap-2" data-testid="discovery-proposals">
      <h3 class="text-highlighted text-sm font-semibold">
        {{ t('prefixes.discovery.proposals') }}
      </h3>
      <p class="text-muted text-sm">{{ t('prefixes.discovery.proposalsHint') }}</p>
      <ul class="flex flex-col gap-2">
        <li
          v-for="p in proposals"
          :key="p.prefix"
          class="bg-default ring-default flex flex-col gap-2 rounded-md p-3 ring-1 lg:flex-row lg:items-center lg:justify-between"
          data-testid="proposal"
        >
          <div class="min-w-0">
            <span class="text-highlighted font-mono break-all">{{ p.prefix }}</span>
            <span class="text-muted block text-sm">
              {{
                t('prefixes.discovery.stats', {
                  ips: formatNumber(p.distinct_ips),
                  bytes: joinUnit(formatBytes(Number(p.bytes))),
                })
              }}
              ·
              {{
                t('prefixes.discovery.suggested', { role: t(`prefixes.role.${p.suggested_role}`) })
              }}
            </span>
          </div>
          <div v-if="canEdit" class="flex flex-wrap gap-2">
            <UButton
              size="sm"
              :loading="accepting === p.prefix"
              icon="i-lucide-users"
              :label="t('prefixes.discovery.acceptCustomers')"
              @click="accept(p, 'customers')"
            />
            <UButton
              size="sm"
              color="neutral"
              variant="outline"
              :label="t('prefixes.discovery.markInfra')"
              @click="accept(p, 'infrastructure')"
            />
            <UButton
              size="sm"
              color="neutral"
              variant="ghost"
              :label="t('prefixes.discovery.exclude')"
              @click="accept(p, 'excluded')"
            />
          </div>
        </li>
      </ul>
    </section>

    <PrefixImportModal
      v-if="routerId"
      v-model:open="importOpen"
      :router-id="routerId"
      :site-id="siteId"
      @applied="imported"
    />

    <UModal
      v-model:open="removeOpen"
      :title="t('prefixes.remove.title', { prefix: removing?.prefix ?? '' })"
    >
      <template #body>
        <p v-if="removing?.role === 'customers' && removing?.customers_count" class="text-default">
          {{ t('prefixes.remove.inactive', { n: formatNumber(removing.customers_count) }) }}
        </p>
        <p v-else class="text-default">{{ t('prefixes.remove.body') }}</p>
      </template>
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
            :label="t('prefixes.remove.submit')"
            @click="remove"
          />
        </div>
      </template>
    </UModal>
  </section>
</template>
