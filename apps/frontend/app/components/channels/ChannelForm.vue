<script setup lang="ts">
import type {
  NotificationCategory,
  NotificationChannel,
  NotificationChannelInput,
  NotificationChannelKind,
  NotificationEventType,
} from '~~/types/api'

/**
 * Alta / edición de un canal de notificación (D13, D17): email, Telegram, LibreNMS por su
 * API o webhook firmado (ADR-0030, p. ej. ArgosISP), por ISP. Las credenciales (token del bot, contraseña y token de LibreNMS) son
 * write-only: se envían aparte (`PUT …/credentials`) y nunca se vuelven a mostrar; al editar
 * se dejan vacías y solo se reemplazan si se escriben. Los avisos se eligen por categoría
 * (seguridad, routers, infraestructura, exportador y túnel), con la opción de afinar por tipo;
 * las recuperaciones llegan al canal que recibió la alarma aunque la severidad mínima las
 * filtraría (`notify_recovery`).
 */
const props = defineProps<{ channel?: NotificationChannel | null }>()
const emit = defineEmits<{ saved: [] }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { $api } = useNuxtApp()
const { withReauth } = useReauth()
const toast = useToast()
const { sites } = useSites()

type Severity = 'info' | 'low' | 'medium' | 'high' | 'critical'

const form = reactive({
  kind: 'email' as NotificationChannelKind,
  name: '',
  enabled: true,
  includePersonal: false,
  recipients: '',
  chatId: '',
  threadId: null as number | null,
  ownBot: false,
  baseUrl: 'https://',
  username: 'horus',
  tlsVerify: true,
  categories: ['security', 'routers'] as NotificationCategory[],
  events: [] as NotificationEventType[],
  minSeverity: 'high' as Severity,
  siteIds: [] as string[],
  throttle: 15,
  notifyRecovery: true,
  // Credenciales write-only.
  botToken: '',
  password: '',
  apiToken: '',
  webhookUrl: 'https://',
  webhookSecret: '',
})
const errors = ref<Record<string, string>>({})
const saving = ref(false)
const editing = computed(() => !!props.channel)
const expanded = ref<Record<string, boolean>>({})

watch(open, (o) => {
  if (!o) return
  errors.value = {}
  const c = props.channel
  const cfg = (c?.config ?? {}) as Record<string, unknown>
  Object.assign(form, {
    kind: c?.kind ?? 'email',
    name: c?.name ?? '',
    enabled: c?.enabled ?? true,
    includePersonal: c?.include_personal_data ?? false,
    recipients: ((cfg.recipients as string[]) ?? []).join(', '),
    chatId: (cfg.chat_id as string) ?? '',
    threadId: (cfg.message_thread_id as number | undefined) ?? null,
    ownBot: (cfg.uses_own_bot as boolean) ?? false,
    baseUrl: (cfg.base_url as string) ?? 'https://',
    username: (cfg.username as string) ?? 'horus',
    tlsVerify: (cfg.tls_verify as boolean) ?? true,
    categories: c ? (c.subscription.categories ?? []) : ['security', 'routers'],
    events: c?.subscription.event_types ?? [],
    minSeverity: (c?.subscription.min_severity as Severity) ?? 'high',
    siteIds: c?.subscription.site_ids ?? [],
    throttle: c?.subscription.throttle_minutes ?? 15,
    notifyRecovery: c?.subscription.notify_recovery ?? true,
    botToken: '',
    password: '',
    apiToken: '',
    webhookUrl: (cfg.url as string) ?? 'https://',
    webhookSecret: '',
  })
  expanded.value = Object.fromEntries(
    NOTIFICATION_CATEGORIES.map((cat) => [
      cat.key,
      !form.categories.includes(cat.key) && cat.events.some((e) => form.events.includes(e)),
    ]),
  )
})

function config(): NotificationChannelInput['config'] {
  if (form.kind === 'email') {
    return {
      recipients: form.recipients.split(/[,;\s]+/).filter(Boolean),
      language: 'es',
      subject_prefix: '[Horus]',
    }
  }
  if (form.kind === 'webhook') {
    return { url: form.webhookUrl.trim() }
  }
  if (form.kind === 'telegram') {
    return {
      chat_id: form.chatId.trim(),
      language: 'es',
      ...(form.threadId ? { message_thread_id: form.threadId } : {}),
    }
  }
  return {
    base_url: form.baseUrl.trim(),
    username: form.username.trim(),
    tls_verify: form.tlsVerify,
  }
}

function credentials() {
  if (form.kind === 'telegram' && form.ownBot && form.botToken) {
    return { telegram_bot_token: form.botToken.trim() }
  }
  if (form.kind === 'webhook' && form.webhookSecret) {
    return { webhook_secret: form.webhookSecret.trim() }
  }
  if (form.kind === 'librenms' && (form.password || form.apiToken)) {
    return { password: form.password || undefined, api_token: form.apiToken || undefined }
  }
  return null
}

async function save() {
  errors.value = {}
  saving.value = true
  const body = {
    name: form.name.trim(),
    enabled: form.enabled,
    include_personal_data: form.includePersonal,
    config: config(),
    subscription: {
      // Los tipos sueltos de una categoría completa sobran: la categoría ya los incluye.
      event_types: form.events.filter((e) => !form.categories.includes(eventCategory(e)!)),
      categories: form.categories,
      min_severity: form.minSeverity,
      site_ids: form.siteIds,
      throttle_minutes: form.throttle,
      notify_recovery: form.notifyRecovery,
    },
  }
  try {
    const saved = props.channel
      ? await unwrap(
          $api.PATCH('/notification-channels/{channel_id}', {
            params: {
              path: { channel_id: props.channel.id },
              header: { 'If-Match': ifMatch(props.channel.version) },
            },
            body,
          }),
        )
      : await unwrap($api.POST('/notification-channels', { body: { ...body, kind: form.kind } }))
    const creds = credentials()
    if (creds) {
      await withReauth(() =>
        unwrap(
          $api.PUT('/notification-channels/{channel_id}/credentials', {
            params: { path: { channel_id: saved.id } },
            body: creds,
          }),
        ),
      )
    }
    open.value = false
    toast.add({
      title: editing.value ? t('channels.form.updated') : t('channels.form.created'),
      color: 'success',
      icon: 'i-lucide-check',
    })
    emit('saved')
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

const kindItems = computed(() =>
  (['email', 'telegram', 'librenms', 'webhook'] as const).map((k) => ({
    label: t(`channels.kind.${k}`),
    value: k,
  })),
)
// Un webhook es un receptor máquina (p. ej. ArgosISP): todos los eventos del contrato, sin
// agrupar ni filtrar por severidad (el receptor deduplica por id).
watch(
  () => form.kind,
  (k) => {
    if (editing.value || k !== 'webhook') return
    Object.assign(form, {
      categories: ['security', 'security_updates', 'routers', 'infrastructure'],
      events: [],
      minSeverity: 'info',
      throttle: 0,
    })
  },
)
function toggleCategory(key: NotificationCategory, on: boolean | 'indeterminate') {
  form.categories =
    on === true ? [...new Set([...form.categories, key])] : form.categories.filter((c) => c !== key)
}
function eventItems(events: NotificationEventType[]) {
  return events.map((e) => ({ label: t(`channels.events.${e}`), value: e }))
}
function categoryEvents(key: NotificationCategory, events: NotificationEventType[]) {
  return form.categories.includes(key) ? events : form.events.filter((e) => events.includes(e))
}
function setCategoryEvents(events: NotificationEventType[], picked: NotificationEventType[]) {
  form.events = [...form.events.filter((e) => !events.includes(e)), ...picked]
}
const severityItems = computed(() =>
  (['info', 'low', 'medium', 'high', 'critical'] as const).map((s) => ({
    label: t('channels.form.fromSeverity', { s: t(`severity.${s}`) }),
    value: s,
  })),
)
const siteItems = computed(() => (sites.value ?? []).map((s) => ({ label: s.name, value: s.id })))
</script>

<template>
  <USlideover
    v-model:open="open"
    :title="editing ? t('channels.form.editTitle') : t('channels.form.title')"
    :description="t('channels.form.description')"
  >
    <template #body>
      <form id="channel-form" class="flex flex-col gap-4" @submit.prevent="save">
        <UFormField v-if="!editing" :label="t('channels.form.kind')">
          <URadioGroup
            v-model="form.kind"
            :items="kindItems"
            orientation="horizontal"
            data-testid="channel-kind"
          />
        </UFormField>
        <UFormField :label="t('channels.form.name')" :error="errors.name" required>
          <UInput v-model="form.name" class="w-full" data-testid="channel-name" />
        </UFormField>

        <UFormField
          v-if="form.kind === 'email'"
          :label="t('channels.form.recipients')"
          :help="t('channels.form.recipientsHelp')"
          :error="errors['config.recipients']"
          required
        >
          <UInput
            v-model="form.recipients"
            type="text"
            class="w-full"
            data-testid="channel-recipients"
          />
        </UFormField>

        <TelegramSetup
          v-if="form.kind === 'telegram'"
          v-model:chat-id="form.chatId"
          v-model:thread-id="form.threadId"
          v-model:bot-token="form.botToken"
          v-model:own-bot="form.ownBot"
          :editing="editing"
          :channel-id="props.channel?.id"
          :has-own-bot="!!props.channel?.has_credentials && props.channel?.kind === 'telegram'"
          :errors="errors"
        />

        <template v-if="form.kind === 'webhook'">
          <UFormField
            :label="t('channels.form.webhookUrl')"
            :help="t('channels.form.webhookUrlHelp')"
            :error="errors['config.url']"
            required
          >
            <UInput
              v-model="form.webhookUrl"
              type="url"
              class="w-full"
              :ui="{ base: 'font-mono' }"
              data-testid="channel-webhook-url"
            />
          </UFormField>
          <UFormField
            :label="t('channels.form.webhookSecret')"
            :help="editing ? t('channels.form.keepSecret') : t('channels.form.webhookSecretHelp')"
            :error="errors.webhook_secret"
            :required="!editing"
          >
            <UInput
              v-model="form.webhookSecret"
              type="password"
              autocomplete="new-password"
              class="w-full"
              data-testid="channel-webhook-secret"
            />
          </UFormField>
          <p class="text-muted text-xs">{{ t('channels.form.webhookSigning') }}</p>
        </template>

        <template v-if="form.kind === 'librenms'">
          <UFormField
            :label="t('channels.form.baseUrl')"
            :help="t('channels.form.baseUrlHelp')"
            :error="errors['config.base_url']"
            required
          >
            <UInput
              v-model="form.baseUrl"
              type="url"
              class="w-full"
              :ui="{ base: 'font-mono' }"
              data-testid="channel-base-url"
            />
          </UFormField>
          <UFormField
            :label="t('channels.form.username')"
            :error="errors['config.username']"
            required
          >
            <UInput v-model="form.username" class="w-full" data-testid="channel-username" />
          </UFormField>
          <div class="grid gap-3 sm:grid-cols-2">
            <UFormField
              :label="t('channels.form.password')"
              :help="editing ? t('channels.form.keepSecret') : t('channels.form.writeOnly')"
            >
              <UInput
                v-model="form.password"
                type="password"
                autocomplete="new-password"
                class="w-full"
                data-testid="channel-password"
              />
            </UFormField>
            <UFormField :label="t('channels.form.apiToken')" :help="t('channels.form.optional')">
              <UInput
                v-model="form.apiToken"
                type="password"
                autocomplete="new-password"
                class="w-full"
              />
            </UFormField>
          </div>
          <USwitch
            v-model="form.tlsVerify"
            :label="t('channels.form.tlsVerify')"
            :description="form.tlsVerify ? undefined : t('channels.form.tlsVerifyOff')"
          />
          <p class="text-muted text-xs">{{ t('channels.form.isolation') }}</p>
        </template>

        <UFormField
          :label="t('channels.form.events')"
          :help="t('channels.form.eventsHelp')"
          :error="errors['subscription.event_types'] ?? errors['subscription.categories']"
        >
          <ul class="flex flex-col gap-2" data-testid="channel-categories">
            <li
              v-for="cat in NOTIFICATION_CATEGORIES"
              :key="cat.key"
              class="border-default rounded-md border p-3"
              :data-category="cat.key"
            >
              <div class="flex items-start gap-2">
                <UCheckbox
                  :model-value="form.categories.includes(cat.key)"
                  :label="t(`channels.categories.${cat.key}.label`)"
                  :description="t(`channels.categories.${cat.key}.help`)"
                  class="min-w-0 flex-1"
                  @update:model-value="toggleCategory(cat.key, $event)"
                />
                <UIcon
                  :name="cat.icon"
                  class="text-muted mt-0.5 size-4 shrink-0"
                  aria-hidden="true"
                />
                <UButton
                  size="xs"
                  color="neutral"
                  variant="ghost"
                  :trailing-icon="
                    expanded[cat.key] ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'
                  "
                  :label="t('channels.form.types')"
                  :aria-expanded="!!expanded[cat.key]"
                  @click="expanded[cat.key] = !expanded[cat.key]"
                />
              </div>
              <UCheckboxGroup
                v-if="expanded[cat.key]"
                class="mt-2 ps-6"
                :model-value="categoryEvents(cat.key, cat.events)"
                :items="eventItems(cat.events)"
                :disabled="form.categories.includes(cat.key)"
                @update:model-value="
                  setCategoryEvents(cat.events, $event as NotificationEventType[])
                "
              />
            </li>
          </ul>
        </UFormField>
        <div class="grid gap-3 sm:grid-cols-2">
          <UFormField
            :label="t('channels.form.minSeverity')"
            :help="t('channels.form.minSeverityHelp')"
          >
            <USelect
              v-model="form.minSeverity"
              :items="severityItems"
              class="w-full"
              data-testid="channel-min-severity"
            />
          </UFormField>
          <UFormField
            :label="t('channels.form.throttle')"
            :help="t('channels.form.throttleHelp')"
            :error="errors['subscription.throttle_minutes']"
          >
            <UInputNumber
              v-model="form.throttle"
              :min="0"
              :max="1440"
              class="w-full"
              data-testid="channel-throttle"
            />
          </UFormField>
        </div>
        <UFormField :label="t('channels.form.sites')" :help="t('channels.form.sitesHelp')">
          <USelectMenu
            v-model="form.siteIds"
            :items="siteItems"
            value-key="value"
            multiple
            :placeholder="t('common.allSites')"
            class="w-full"
            data-testid="channel-sites"
          />
        </UFormField>
        <USwitch
          v-model="form.notifyRecovery"
          :label="t('channels.form.notifyRecovery')"
          :description="t('channels.form.notifyRecoveryHelp')"
          data-testid="channel-notify-recovery"
        />
        <USwitch
          v-model="form.includePersonal"
          :label="t('channels.form.personal')"
          :description="t('channels.form.personalHelp')"
        />
        <USwitch v-model="form.enabled" :label="t('channels.form.enabled')" />
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
          form="channel-form"
          :loading="saving"
          :label="t('channels.form.submit')"
          data-testid="channel-submit"
        />
      </div>
    </template>
  </USlideover>
</template>
