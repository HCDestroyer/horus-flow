<script setup lang="ts">
import type { TelegramBotInfo, TelegramChatCandidate, TelegramLinkCode } from '~~/types/api'

/**
 * Configuración guiada de un canal de Telegram: bot de Horus (el de la instalación) o bot
 * propio creado con @BotFather (los bots no se crean por la API: solo se guía y se valida el
 * token con getMe). «Detectar chat» lee los mensajes pendientes del bot (getUpdates): con un
 * bot propio lista los chats donde le escribieron; con el de la instalación exige un código
 * de un solo uso para no mezclar ISP. El token es write-only: se guarda al guardar el canal.
 */
const props = defineProps<{
  editing: boolean
  channelId?: string
  hasOwnBot: boolean
  errors: Record<string, string>
}>()
const chatId = defineModel<string>('chatId', { required: true })
const threadId = defineModel<number | null>('threadId', { required: true })
const botToken = defineModel<string>('botToken', { required: true })
const ownBot = defineModel<boolean>('ownBot', { required: true })

const { t } = useI18n()
const { $api } = useNuxtApp()

const bot = ref<TelegramBotInfo | null>(null)
const botError = ref<string | null>(null)
const validating = ref(false)
const link = ref<TelegramLinkCode | null>(null)
const linking = ref(false)
const chats = ref<TelegramChatCandidate[] | null>(null)
const detecting = ref(false)
const detectError = ref<string | null>(null)

watch(botToken, () => {
  bot.value = null
  botError.value = null
})
watch(ownBot, () => {
  chats.value = null
  detectError.value = null
})

function problemText(e: unknown) {
  if (e instanceof ApiError) {
    return e.problem.errors?.[0]?.message ?? e.problem.detail ?? t(describeError(e).messageKey)
  }
  return t(describeError(e).messageKey)
}

async function validateBot() {
  validating.value = true
  botError.value = null
  try {
    bot.value = await unwrap(
      $api.POST('/notification-channels/telegram/bot-validation', {
        body: { bot_token: botToken.value.trim() },
      }),
    )
  } catch (e) {
    botError.value = problemText(e)
  } finally {
    validating.value = false
  }
}

async function createCode() {
  linking.value = true
  detectError.value = null
  try {
    link.value = await unwrap($api.POST('/notification-channels/telegram/link-codes'))
  } catch (e) {
    detectError.value = problemText(e)
  } finally {
    linking.value = false
  }
}

const canDetect = computed(() => {
  if (ownBot.value) return !!botToken.value.trim() || (props.hasOwnBot && !!props.channelId)
  return !!link.value
})

async function detect() {
  detecting.value = true
  detectError.value = null
  try {
    const body = ownBot.value
      ? botToken.value.trim()
        ? { bot_token: botToken.value.trim() }
        : { channel_id: props.channelId }
      : { code: link.value?.code }
    const r = await unwrap($api.POST('/notification-channels/telegram/chat-detection', { body }))
    chats.value = r.data
  } catch (e) {
    detectError.value = problemText(e)
  } finally {
    detecting.value = false
  }
}

function pick(c: TelegramChatCandidate) {
  chatId.value = c.id
}

const sourceItems = computed(() => [
  {
    label: t('channels.telegram.sharedBot'),
    value: 'shared',
    description: t('channels.telegram.sharedBotHelp'),
  },
  {
    label: t('channels.telegram.ownBot'),
    value: 'own',
    description: t('channels.telegram.ownBotHelp'),
  },
])
const source = computed({
  get: () => (ownBot.value ? 'own' : 'shared'),
  set: (v: string) => (ownBot.value = v === 'own'),
})
const chatIcon = (type: string) =>
  type === 'channel'
    ? 'i-lucide-megaphone'
    : type === 'private'
      ? 'i-lucide-user'
      : 'i-lucide-users'
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="telegram-setup">
    <UFormField :label="t('channels.telegram.source')">
      <URadioGroup v-model="source" :items="sourceItems" data-testid="telegram-source" />
    </UFormField>

    <UCard v-if="ownBot" variant="subtle" :ui="{ body: 'p-3 sm:p-4 flex flex-col gap-3' }">
      <p class="text-highlighted text-sm font-medium">{{ t('channels.telegram.wizardTitle') }}</p>
      <ol class="text-muted flex list-decimal flex-col gap-1.5 ps-5 text-sm">
        <li>
          {{ t('channels.telegram.step1') }}
          <ULink
            to="https://t.me/BotFather"
            target="_blank"
            rel="noopener noreferrer"
            class="text-primary font-medium"
            >@BotFather</ULink
          >
        </li>
        <li>
          {{ t('channels.telegram.step2a') }} <code class="font-mono">/newbot</code>
          {{ t('channels.telegram.step2b') }}
        </li>
        <li>{{ t('channels.telegram.step3') }}</li>
      </ol>
      <UFormField
        :label="t('channels.form.botToken')"
        :help="editing && hasOwnBot ? t('channels.form.keepSecret') : t('channels.form.writeOnly')"
        :error="errors.telegram_bot_token"
      >
        <div class="flex gap-2">
          <UInput
            v-model="botToken"
            type="password"
            autocomplete="new-password"
            class="w-full"
            :ui="{ base: 'font-mono' }"
            data-testid="channel-bot-token"
          />
          <UButton
            color="neutral"
            variant="outline"
            :label="t('channels.telegram.validate')"
            :loading="validating"
            :disabled="!botToken.trim()"
            data-testid="telegram-validate"
            @click="validateBot"
          />
        </div>
      </UFormField>
      <UAlert
        v-if="bot"
        color="success"
        variant="subtle"
        icon="i-lucide-bot"
        :title="
          t('channels.telegram.botOk', { name: bot.first_name, username: '@' + bot.username })
        "
        data-testid="telegram-bot-ok"
      />
      <UAlert
        v-else-if="botError"
        color="error"
        variant="subtle"
        icon="i-lucide-circle-x"
        :title="botError"
      />
      <p class="text-muted text-sm">{{ t('channels.telegram.step4') }}</p>
    </UCard>

    <UCard v-else variant="subtle" :ui="{ body: 'p-3 sm:p-4 flex flex-col gap-3' }">
      <UAlert
        v-if="hasOwnBot"
        color="warning"
        variant="subtle"
        icon="i-lucide-triangle-alert"
        :title="t('channels.telegram.ownBotStored')"
      />
      <p class="text-muted text-sm">{{ t('channels.telegram.sharedIntro') }}</p>
      <div v-if="link" class="flex flex-col gap-2" data-testid="telegram-link-code">
        <p class="text-sm">
          {{ t('channels.telegram.codeLabel') }}
          <code class="text-highlighted font-mono text-base">{{ link.code }}</code>
        </p>
        <p class="text-muted text-sm">
          <template v-if="link.start_url">
            {{ t('channels.telegram.codePrivate') }}
            <ULink
              :to="link.start_url"
              target="_blank"
              rel="noopener noreferrer"
              class="text-primary font-medium"
              >{{ link.bot_username }}</ULink
            >.
          </template>
          {{
            t('channels.telegram.codeGroup', {
              bot: link.bot_username ?? 'el bot',
              code: link.code,
            })
          }}
        </p>
        <p class="text-dimmed text-xs">
          {{ t('channels.telegram.codeExpires') }} <RelativeTime :at="link.expires_at" />
        </p>
      </div>
      <UButton
        class="self-start"
        color="neutral"
        variant="outline"
        icon="i-lucide-key-round"
        :loading="linking"
        :label="link ? t('channels.telegram.newCode') : t('channels.telegram.createCode')"
        data-testid="telegram-create-code"
        @click="createCode"
      />
    </UCard>

    <div class="flex flex-col gap-2">
      <UButton
        class="self-start"
        icon="i-lucide-radar"
        :loading="detecting"
        :disabled="!canDetect"
        :label="t('channels.telegram.detect')"
        data-testid="telegram-detect"
        @click="detect"
      />
      <UAlert
        v-if="detectError"
        color="error"
        variant="subtle"
        icon="i-lucide-circle-x"
        :title="detectError"
        data-testid="telegram-detect-error"
      />
      <p
        v-else-if="chats && !chats.length"
        class="text-muted text-sm"
        data-testid="telegram-no-chats"
      >
        {{ t('channels.telegram.noChats') }}
      </p>
      <ul v-else-if="chats" class="flex flex-col gap-1.5" data-testid="telegram-chats">
        <li v-for="c in chats" :key="c.id">
          <UButton
            block
            :color="chatId === c.id ? 'primary' : 'neutral'"
            :variant="chatId === c.id ? 'soft' : 'outline'"
            :icon="chatIcon(c.type)"
            class="justify-start"
            :data-chat="c.id"
            @click="pick(c)"
          >
            <span class="truncate">{{ c.title || c.username || c.id }}</span>
            <span class="text-dimmed ms-auto font-mono text-xs">{{ c.id }}</span>
          </UButton>
        </li>
      </ul>
    </div>

    <div class="grid gap-3 sm:grid-cols-[1fr_10rem]">
      <UFormField
        :label="t('channels.form.chatId')"
        :help="t('channels.form.chatIdHelp')"
        :error="errors['config.chat_id']"
        required
      >
        <UInput
          v-model="chatId"
          class="w-full"
          :ui="{ base: 'font-mono' }"
          data-testid="channel-chat"
        />
      </UFormField>
      <UFormField
        :label="t('channels.telegram.thread')"
        :help="t('channels.telegram.threadHelp')"
        :error="errors['config.message_thread_id']"
      >
        <UInputNumber
          v-model="threadId"
          :min="1"
          :increment="false"
          :decrement="false"
          class="w-full"
          data-testid="channel-thread"
        />
      </UFormField>
    </div>
  </div>
</template>
