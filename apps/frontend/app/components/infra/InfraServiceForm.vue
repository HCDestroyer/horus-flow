<script setup lang="ts">
import type { InfraCheck, InfraCheckType, InfraService, Router } from '~~/types/api'

/**
 * Alta / edición de un servicio de infraestructura con su constructor de chequeos: ping,
 * puerto TCP, página web (HTTP/HTTPS con rango de códigos y verificación del certificado),
 * sin tráfico (ventana y mínimo de bytes) y ataque entrante (umbrales pps/bps). Para
 * servidores privados, «sondear a través del router» pide el router del nodo y un puerto de
 * reenvío por chequeo TCP/HTTP (el script lo pega el usuario; Horus nunca escribe en routers).
 */
const props = defineProps<{ service?: InfraService | null }>()
const emit = defineEmits<{ saved: [InfraService] }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { sites } = useSites()
const routers = ref<Router[]>([])

const form = reactive({
  name: '',
  target: '',
  kind: 'iptv',
  siteId: 'none',
  interval: 60,
  failures: 3,
  recoveries: 2,
  enabled: true,
  viaRouter: false,
  routerId: '',
  checks: [] as InfraCheck[],
})
const errors = ref<Record<string, string>>({})
const saving = ref(false)
const editing = computed(() => !!props.service)

watch(open, async (o) => {
  if (!o) return
  errors.value = {}
  const s = props.service
  Object.assign(form, {
    name: s?.name ?? '',
    target: s?.target ?? '',
    kind: s?.kind ?? 'iptv',
    siteId: s?.site_id ?? 'none',
    interval: s?.interval_seconds ?? 60,
    failures: s?.failures_before_down ?? 3,
    recoveries: s?.recoveries_before_up ?? 2,
    enabled: s?.enabled ?? true,
    viaRouter: !!s?.probe_via_router_id,
    routerId: s?.probe_via_router_id ?? '',
    checks: s ? s.checks.map((c) => ({ ...c })) : [blankCheck('icmp')],
  })
  try {
    routers.value = (await unwrap($api.GET('/routers', { params: { query: { limit: 200 } } }))).data
  } catch {
    routers.value = []
  }
})

const privateTarget = computed(() => isPrivateIp(form.target.trim()))
const kindItems = computed(() => [
  ...INFRA_KINDS.map((k) => ({ label: t(`infra.kind.${k}`), value: k as string })),
  ...(INFRA_KINDS.includes(form.kind as (typeof INFRA_KINDS)[number])
    ? []
    : [{ label: form.kind, value: form.kind }]),
])
const siteItems = computed(() => [
  { label: t('infra.form.noSite'), value: 'none' },
  ...(sites.value ?? []).map((s) => ({ label: s.name, value: s.id })),
])
const routerItems = computed(() =>
  routers.value
    .filter((r) => form.siteId === 'none' || r.site_id === form.siteId)
    .map((r) => ({ label: r.name, value: r.id })),
)
const typeItems = computed(() =>
  INFRA_CHECK_TYPES.map((c) => ({ label: t(`infra.checkType.${c}`), value: c })),
)

function addCheck(type: InfraCheckType = 'tcp') {
  form.checks.push(blankCheck(type))
}
function setType(i: number, type: InfraCheckType) {
  form.checks[i] = blankCheck(type)
}
function num(v: unknown): number | null {
  if (v === '' || v === null || v === undefined) return null
  const n = Number(v)
  return Number.isFinite(n) ? n : null
}
function err(i: number, field: string) {
  return errors.value[`checks[${i}].${field}`]
}

async function save() {
  errors.value = {}
  if (!isIpAddress(form.target.trim())) {
    errors.value = { target: t('infra.form.invalidIp') }
    return
  }
  if (!form.checks.length) {
    errors.value = { checks: t('infra.form.needCheck') }
    return
  }
  saving.value = true
  const via = form.viaRouter && !!form.routerId
  const body = {
    name: form.name.trim(),
    target: form.target.trim(),
    kind: form.kind,
    site_id: form.siteId === 'none' ? null : form.siteId,
    interval_seconds: Number(form.interval),
    failures_before_down: Number(form.failures),
    recoveries_before_up: Number(form.recoveries),
    enabled: form.enabled,
    probe_via_router_id: via ? form.routerId : null,
    checks: form.checks.map((c) => checkPayload(c, via)),
  }
  try {
    const saved = props.service
      ? await unwrap(
          $api.PATCH('/infra-services/{service_id}', {
            params: {
              path: { service_id: props.service.id },
              header: { 'If-Match': ifMatch(props.service.version) },
            },
            body,
          }),
        )
      : await unwrap(
          $api.POST('/infra-services', {
            params: { header: { 'Idempotency-Key': idempotencyKey() } },
            body,
          }),
        )
    open.value = false
    toast.add({
      title: editing.value ? t('infra.form.updated') : t('infra.form.created'),
      color: 'success',
      icon: 'i-lucide-check',
    })
    emit('saved', saved)
  } catch (e) {
    if (e instanceof ApiError && e.problem.errors?.length) {
      errors.value = Object.fromEntries(e.problem.errors.map((x) => [x.field, x.message]))
    } else {
      errors.value = { _: t(describeError(e).messageKey) }
    }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <USlideover
    v-model:open="open"
    :title="editing ? t('infra.form.editTitle') : t('infra.form.title')"
    :description="t('infra.form.description')"
    :ui="{ content: 'sm:max-w-xl' }"
  >
    <template #body>
      <form id="infra-form" class="flex flex-col gap-4" @submit.prevent="save">
        <UFormField :label="t('infra.form.name')" :error="errors.name" required>
          <UInput v-model="form.name" class="w-full" data-testid="infra-name" />
        </UFormField>
        <div class="grid gap-3 sm:grid-cols-2">
          <UFormField
            :label="t('infra.form.target')"
            :help="t('infra.form.targetHelp')"
            :error="errors.target"
            required
          >
            <UInput
              v-model="form.target"
              class="w-full"
              :ui="{ base: 'font-mono' }"
              inputmode="text"
              autocomplete="off"
              data-testid="infra-target"
            />
          </UFormField>
          <UFormField :label="t('infra.form.kind')" :error="errors.kind">
            <USelect
              v-model="form.kind"
              :items="kindItems"
              class="w-full"
              data-testid="infra-kind"
            />
          </UFormField>
        </div>
        <UFormField :label="t('infra.form.site')" :error="errors.site_id">
          <USelect v-model="form.siteId" :items="siteItems" class="w-full" />
        </UFormField>
        <div class="grid gap-3 sm:grid-cols-3">
          <UFormField
            :label="t('infra.form.interval')"
            :help="t('infra.form.intervalHelp')"
            :error="errors.interval_seconds"
          >
            <UInput
              v-model.number="form.interval"
              type="number"
              min="30"
              max="3600"
              class="w-full"
            />
          </UFormField>
          <UFormField :label="t('infra.form.failures')" :error="errors.failures_before_down">
            <UInput v-model.number="form.failures" type="number" min="1" max="20" class="w-full" />
          </UFormField>
          <UFormField :label="t('infra.form.recoveries')" :error="errors.recoveries_before_up">
            <UInput
              v-model.number="form.recoveries"
              type="number"
              min="1"
              max="20"
              class="w-full"
            />
          </UFormField>
        </div>

        <UAlert
          v-if="privateTarget"
          color="warning"
          variant="subtle"
          icon="i-lucide-lock"
          :title="t('infra.private')"
          :description="t('infra.privateHint')"
          data-testid="infra-private-hint"
        />
        <USwitch
          v-model="form.viaRouter"
          :label="t('infra.form.viaRouter')"
          :description="t('infra.form.viaRouterHelp')"
          data-testid="infra-via-router"
        />
        <UFormField
          v-if="form.viaRouter"
          :label="t('infra.form.router')"
          :error="errors.probe_via_router_id"
          required
        >
          <USelect
            v-model="form.routerId"
            :items="routerItems"
            class="w-full"
            data-testid="infra-router"
          />
        </UFormField>

        <fieldset class="flex flex-col gap-3">
          <legend class="text-highlighted mb-1 text-sm font-medium">
            {{ t('infra.form.checks') }}
          </legend>
          <p v-if="errors.checks" class="text-error text-sm">{{ errors.checks }}</p>
          <div
            v-for="(c, i) in form.checks"
            :key="i"
            class="ring-default flex flex-col gap-3 rounded-md p-3 ring-1"
            data-testid="infra-check"
            :data-check-type="c.type"
          >
            <div class="flex items-end gap-2">
              <UFormField :label="t('infra.form.checkN', { n: i + 1 })" class="min-w-0 flex-1">
                <USelect
                  :model-value="c.type"
                  :items="typeItems"
                  class="w-full"
                  data-testid="infra-check-type"
                  @update:model-value="setType(i, $event as InfraCheckType)"
                />
              </UFormField>
              <UButton
                color="neutral"
                variant="ghost"
                icon="i-lucide-trash-2"
                :aria-label="t('infra.form.removeCheck', { n: i + 1 })"
                @click="form.checks.splice(i, 1)"
              />
            </div>

            <div v-if="c.type === 'tcp' || c.type === 'http'" class="grid gap-3 sm:grid-cols-2">
              <UFormField :label="t('infra.form.port')" :error="err(i, 'port')">
                <UInput
                  :model-value="c.port ?? undefined"
                  type="number"
                  min="1"
                  max="65535"
                  class="w-full"
                  data-testid="infra-check-port"
                  @update:model-value="c.port = num($event)"
                />
              </UFormField>
              <UFormField
                v-if="form.viaRouter"
                :label="t('infra.form.forwardPort')"
                :error="err(i, 'forward_port')"
                required
              >
                <UInput
                  :model-value="c.forward_port ?? undefined"
                  type="number"
                  min="1"
                  max="65535"
                  class="w-full"
                  data-testid="infra-check-forward-port"
                  @update:model-value="c.forward_port = num($event)"
                />
              </UFormField>
            </div>

            <template v-if="c.type === 'http'">
              <UFormField :label="t('infra.form.path')" :error="err(i, 'path')">
                <UInput
                  :model-value="c.path ?? '/'"
                  class="w-full"
                  :ui="{ base: 'font-mono' }"
                  data-testid="infra-check-path"
                  @update:model-value="c.path = String($event)"
                />
              </UFormField>
              <div class="grid gap-3 sm:grid-cols-2">
                <UFormField
                  :label="t('infra.form.statusMin')"
                  :error="err(i, 'expected_status_min')"
                >
                  <UInput
                    :model-value="c.expected_status_min ?? 200"
                    type="number"
                    min="100"
                    max="599"
                    class="w-full"
                    @update:model-value="c.expected_status_min = num($event)"
                  />
                </UFormField>
                <UFormField :label="t('infra.form.statusMax')">
                  <UInput
                    :model-value="c.expected_status_max ?? 399"
                    type="number"
                    min="100"
                    max="599"
                    class="w-full"
                    @update:model-value="c.expected_status_max = num($event)"
                  />
                </UFormField>
              </div>
              <div class="flex flex-wrap gap-x-6 gap-y-2">
                <USwitch
                  :model-value="!!c.https"
                  :label="t('infra.form.https')"
                  data-testid="infra-check-https"
                  @update:model-value="
                    (v: boolean) => {
                      c.https = v
                      if (c.port === 80 && v) c.port = 443
                      else if (c.port === 443 && !v) c.port = 80
                    }
                  "
                />
                <USwitch
                  v-if="c.https"
                  :model-value="c.tls_verify !== false"
                  :label="t('infra.form.tlsVerify')"
                  :description="c.tls_verify === false ? t('infra.form.tlsVerifyOff') : undefined"
                  @update:model-value="c.tls_verify = $event"
                />
              </div>
            </template>

            <template v-if="c.type === 'no_traffic'">
              <div class="grid gap-3 sm:grid-cols-2">
                <UFormField :label="t('infra.form.window')" :error="err(i, 'window_minutes')">
                  <UInput
                    :model-value="c.window_minutes ?? 15"
                    type="number"
                    min="5"
                    max="1440"
                    class="w-full"
                    @update:model-value="c.window_minutes = num($event)"
                  />
                </UFormField>
                <UFormField :label="t('infra.form.minBytes')" :error="err(i, 'min_bytes')">
                  <UInput
                    :model-value="c.min_bytes ?? 1"
                    type="number"
                    min="1"
                    class="w-full"
                    @update:model-value="c.min_bytes = num($event)"
                  />
                </UFormField>
              </div>
              <p class="text-muted text-xs">{{ t('infra.form.noTrafficHelp') }}</p>
            </template>

            <template v-if="c.type === 'ddos_inbound'">
              <div class="grid gap-3 sm:grid-cols-2">
                <UFormField :label="t('infra.form.pps')" :error="err(i, 'pps_threshold')">
                  <UInput
                    :model-value="c.pps_threshold ?? undefined"
                    type="number"
                    min="1"
                    class="w-full"
                    data-testid="infra-check-pps"
                    @update:model-value="c.pps_threshold = num($event)"
                  />
                </UFormField>
                <UFormField :label="t('infra.form.bps')" :error="err(i, 'bps_threshold')">
                  <UInput
                    :model-value="c.bps_threshold ?? undefined"
                    type="number"
                    min="1"
                    class="w-full"
                    @update:model-value="c.bps_threshold = num($event)"
                  />
                </UFormField>
              </div>
              <p class="text-muted text-xs">{{ t('infra.form.ddosHelp') }}</p>
            </template>
          </div>
          <UButton
            v-if="form.checks.length < 10"
            color="neutral"
            variant="outline"
            icon="i-lucide-plus"
            class="self-start"
            :label="t('infra.form.addCheck')"
            data-testid="infra-add-check"
            @click="addCheck()"
          />
        </fieldset>

        <USwitch v-model="form.enabled" :label="t('infra.form.enabled')" />
        <UAlert v-if="errors._" color="error" variant="subtle" :title="errors._" />
      </form>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton
          color="neutral"
          variant="ghost"
          :label="t('common.cancel')"
          @click="open = false"
        />
        <UButton
          type="submit"
          form="infra-form"
          :loading="saving"
          :label="t('infra.form.submit')"
          data-testid="infra-submit"
        />
      </div>
    </template>
  </USlideover>
</template>
