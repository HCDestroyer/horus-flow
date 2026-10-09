<script setup lang="ts">
import type {
  ClientPrefixInput,
  ClientPrefixRole,
  PrefixImportItem,
  PrefixImportPreview,
} from '~~/types/api'

/**
 * Importar del MikroTik (I1-19 criterio 4, I1-28, E-IPv6-1): Horus lee por la API de
 * RouterOS (solo lectura) los pools IPv4/IPv6 y las redes de las interfaces y muestra una
 * lista para revisar con casillas, rol propuesto y diferencias con lo existente. En IPv6 se
 * ve el tamaño que el router delega a cada cliente y el uso del pool; el tamaño del cliente
 * (`ipv6_client_len`) se propone y se puede cambiar. Nada se aplica sin "Confirmar".
 */
const props = defineProps<{ routerId: string; siteId: string }>()
const emit = defineEmits<{ applied: [count: number] }>()
const open = defineModel<boolean>('open', { default: false })

const { t, d } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()

interface Row {
  item: PrefixImportItem
  selected: boolean
  role: ClientPrefixRole
  assignment: 'static' | 'dynamic' | 'unknown'
  clientLen: 48 | 56 | 60 | 64 | null
}

const preview = ref<PrefixImportPreview | null>(null)
const rows = ref<Row[]>([])
const loading = ref(false)
const saving = ref(false)
const error = shallowRef<unknown>(null)
const saveError = ref<string | null>(null)

async function load() {
  loading.value = true
  error.value = null
  saveError.value = null
  try {
    preview.value = await unwrap(
      $api.POST('/routers/{router_id}/prefix-import-preview', {
        params: { path: { router_id: props.routerId } },
        body: {},
      }),
    )
    rows.value = preview.value.items.map((item) => ({
      item,
      selected: item.diff === 'new' && item.suggested_role !== 'excluded',
      role: item.suggested_role,
      assignment: item.suggested_assignment_mode ?? 'unknown',
      clientLen: item.suggested_ipv6_client_len ?? null,
    }))
  } catch (e) {
    error.value = e
  } finally {
    loading.value = false
  }
}
watch(open, (o) => o && load())

const isV6 = (r: Row) => r.item.prefix.includes(':')
const needsLen = (r: Row) => r.selected && isV6(r) && r.role === 'customers' && !r.clientLen
const selected = computed(() => rows.value.filter((r) => r.selected))
const invalid = computed(() => selected.value.some(needsLen))

async function confirm() {
  if (!selected.value.length || invalid.value) return
  saving.value = true
  saveError.value = null
  const items: ClientPrefixInput[] = selected.value.map((r) => ({
    prefix: r.item.prefix,
    role: r.role,
    assignment_mode: r.assignment,
    source: 'routeros_api',
    note: r.item.origin_name ?? null,
    ipv6_client_len: isV6(r) ? r.clientLen : null,
  }))
  try {
    const res = await unwrap(
      $api.POST('/sites/{site_id}/client-prefixes/batch', {
        params: {
          path: { site_id: props.siteId },
          header: { 'Idempotency-Key': idempotencyKey() },
        },
        body: { items },
      }),
    )
    open.value = false
    toast.add({
      title: t('prefixes.import.done', { n: res.data.length }),
      icon: 'i-lucide-check',
      color: 'success',
    })
    emit('applied', res.data.length)
  } catch (e) {
    saveError.value =
      e instanceof ApiError
        ? (e.problem.errors?.[0]?.message ?? e.problem.title)
        : t('errors.generic.title')
  } finally {
    saving.value = false
  }
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
const lenItems = computed(() => [
  { label: t('prefixes.import.chooseLen'), value: null },
  ...([48, 56, 60, 64] as const).map((n) => ({ label: `/${n}`, value: n })),
])
const DIFF_COLOR = { new: 'success', exists: 'neutral', overlaps: 'warning' } as const
</script>

<template>
  <UModal
    v-model:open="open"
    :title="t('prefixes.import.title')"
    :description="t('prefixes.import.description')"
    :ui="{ content: 'sm:max-w-5xl' }"
  >
    <template #body>
      <LoadingState v-if="loading" :rows="5" :label="t('prefixes.import.reading')" />
      <ErrorState
        v-else-if="error"
        :error="error"
        :hint="t('prefixes.import.unreachable')"
        @retry="load"
      />
      <div v-else-if="preview" class="flex flex-col gap-4" data-testid="import-preview">
        <p class="text-muted text-sm">
          {{
            t('prefixes.import.readAt', {
              version: preview.routeros_version,
              date: d(new Date(preview.read_at), 'long'),
            })
          }}
          <span v-if="preview.tls_fingerprint_sha256" class="block font-mono text-xs break-all">
            {{ t('prefixes.import.fingerprint') }} {{ preview.tls_fingerprint_sha256 }}
          </span>
        </p>
        <ul class="flex flex-col gap-2">
          <li
            v-for="r in rows"
            :key="r.item.prefix"
            class="ring-default flex flex-col gap-3 rounded-md p-3 ring-1"
            :class="{ 'bg-muted': !r.selected }"
            data-testid="import-row"
            :data-prefix="r.item.prefix"
          >
            <div class="flex flex-wrap items-start justify-between gap-2">
              <UCheckbox
                v-model="r.selected"
                :disabled="r.item.diff !== 'new'"
                :aria-label="t('prefixes.import.select', { prefix: r.item.prefix })"
              >
                <template #label>
                  <span class="text-highlighted font-mono break-all">{{ r.item.prefix }}</span>
                </template>
                <template #description>
                  <span class="text-muted text-xs">
                    {{ t(`prefixes.origin.${r.item.origin}`)
                    }}<template v-if="r.item.origin_name"> · {{ r.item.origin_name }}</template>
                  </span>
                </template>
              </UCheckbox>
              <UBadge
                :color="DIFF_COLOR[r.item.diff]"
                variant="subtle"
                :label="t(`prefixes.import.diff.${r.item.diff}`)"
                data-testid="import-diff"
              />
            </div>

            <div
              v-if="isV6(r)"
              class="text-muted flex flex-wrap gap-x-4 gap-y-1 text-xs"
              data-testid="import-v6"
            >
              <span v-if="r.item.delegated_prefix_length">
                {{ t('prefixes.import.delegated', { len: r.item.delegated_prefix_length }) }}
              </span>
              <span v-if="r.item.ipv6_pool_usage">{{
                t(`prefixes.poolUsage.${r.item.ipv6_pool_usage}`)
              }}</span>
            </div>

            <div v-if="r.selected" class="grid gap-3 sm:grid-cols-3">
              <UFormField :label="t('prefixes.col.role')">
                <USelect v-model="r.role" :items="roleItems" class="w-full" />
              </UFormField>
              <UFormField :label="t('prefixes.col.assignment')">
                <USelect v-model="r.assignment" :items="assignmentItems" class="w-full" />
              </UFormField>
              <UFormField
                v-if="isV6(r) && r.role === 'customers'"
                :label="t('prefixes.col.clientLen')"
                :help="t('prefixes.clientLenHelp')"
                :error="needsLen(r) ? t('prefixes.import.lenRequired') : undefined"
              >
                <USelect
                  v-model="r.clientLen"
                  :items="lenItems"
                  class="w-full"
                  data-testid="import-client-len"
                />
              </UFormField>
            </div>
          </li>
        </ul>
        <UAlert
          v-if="saveError"
          color="error"
          variant="subtle"
          icon="i-lucide-circle-alert"
          :title="saveError"
        />
      </div>
    </template>
    <template #footer>
      <div class="flex w-full flex-wrap items-center justify-between gap-2">
        <p class="text-muted text-sm">
          {{ t('prefixes.import.selected', { n: selected.length }) }}
        </p>
        <div class="flex gap-2">
          <UButton
            color="neutral"
            variant="ghost"
            :label="t('common.cancel')"
            @click="open = false"
          />
          <UButton
            :disabled="!selected.length || invalid"
            :loading="saving"
            icon="i-lucide-check"
            :label="t('prefixes.import.confirm')"
            data-testid="import-confirm"
            @click="confirm"
          />
        </div>
      </div>
    </template>
  </UModal>
</template>
