<script setup lang="ts">
import type {
  NotificationChannel,
  NotificationChannelInput,
  NotificationChannelKind,
  NotificationEventType,
} from '~~/types/api'

/**
 * Alta / edición de un canal de notificación (D13, D17): email, Telegram o LibreNMS por su
 * API, por ISP. Las credenciales (token del bot, contraseña y token de LibreNMS) son
 * write-only: se envían aparte (`PUT …/credentials`) y nunca se vuelven a mostrar; al editar
 * se dejan vacías y solo se reemplazan si se escriben.
 */
const props = defineProps<{ channel?: NotificationChannel | null }>()
const emit = defineEmits<{ saved: [] }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()

const EVENTS: NotificationEventType[] = [
  'finding_opened',
  'finding_reopened',
  'exporter_silent',
  'exporter_recovered',
  'tunnel_down',
  'tunnel_recovered',
]

const form = reactive({
  kind: 'email' as NotificationChannelKind,
  name: '',
  enabled: true,
  includePersonal: false,
  recipients: '',
  chatId: '',
  baseUrl: 'https://',
  username: 'horus',
  tlsVerify: true,
  events: ['finding_opened', 'exporter_silent'] as NotificationEventType[],
  minSeverity: 'high' as 'low' | 'medium' | 'high' | 'critical',
  // Credenciales write-only.
  botToken: '',
  password: '',
  apiToken: '',
})
const errors = ref<Record<string, string>>({})
const saving = ref(false)
const editing = computed(() => !!props.channel)

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
    baseUrl: (cfg.base_url as string) ?? 'https://',
    username: (cfg.username as string) ?? 'horus',
    tlsVerify: (cfg.tls_verify as boolean) ?? true,
    events: c?.subscription.event_types ?? ['finding_opened', 'exporter_silent'],
    minSeverity: (c?.subscription.min_severity as typeof form.minSeverity) ?? 'high',
    botToken: '',
    password: '',
    apiToken: '',
  })
})

function config(): NotificationChannelInput['config'] {
  if (form.kind === 'email') {
    return {
      recipients: form.recipients.split(/[,;\s]+/).filter(Boolean),
      language: 'es',
      subject_prefix: '[Horus]',
    }
  }
  if (form.kind === 'telegram') return { chat_id: form.chatId.trim(), language: 'es' }
  return {
    base_url: form.baseUrl.trim(),
    username: form.username.trim(),
    tls_verify: form.tlsVerify,
  }
}

function credentials() {
  if (form.kind === 'telegram' && form.botToken) return { telegram_bot_token: form.botToken }
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
      event_types: form.events,
      min_severity: form.minSeverity,
      site_ids: [],
      throttle_minutes: 15,
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
      await unwrap(
        $api.PUT('/notification-channels/{channel_id}/credentials', {
          params: { path: { channel_id: saved.id } },
          body: creds,
        }),
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
  (['email', 'telegram', 'librenms'] as const).map((k) => ({
    label: t(`channels.kind.${k}`),
    value: k,
  })),
)
const eventItems = computed(() =>
  EVENTS.map((e) => ({ label: t(`channels.events.${e}`), value: e })),
)
const severityItems = computed(() =>
  (['low', 'medium', 'high', 'critical'] as const).map((s) => ({
    label: t('channels.form.fromSeverity', { s: t(`severity.${s}`) }),
    value: s,
  })),
)
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

        <template v-if="form.kind === 'telegram'">
          <UFormField
            :label="t('channels.form.chatId')"
            :help="t('channels.form.chatIdHelp')"
            :error="errors['config.chat_id']"
            required
          >
            <UInput
              v-model="form.chatId"
              class="w-full"
              :ui="{ base: 'font-mono' }"
              data-testid="channel-chat"
            />
          </UFormField>
          <UFormField
            :label="t('channels.form.botToken')"
            :help="editing ? t('channels.form.keepSecret') : t('channels.form.writeOnly')"
          >
            <UInput
              v-model="form.botToken"
              type="password"
              autocomplete="new-password"
              class="w-full"
              data-testid="channel-bot-token"
            />
          </UFormField>
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

        <UFormField :label="t('channels.form.events')" :error="errors['subscription.event_types']">
          <UCheckboxGroup v-model="form.events" :items="eventItems" />
        </UFormField>
        <UFormField :label="t('channels.form.minSeverity')">
          <USelect v-model="form.minSeverity" :items="severityItems" class="w-56" />
        </UFormField>
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
