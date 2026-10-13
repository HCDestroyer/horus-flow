<script setup lang="ts">
import type { PasswordResetIssued } from '~~/types/api'

/**
 * Enlace de restablecimiento de contraseña recién emitido por un administrador
 * (`POST /members/{user_id}/password-reset`, `POST /platform/users/{user_id}/password-reset`).
 * Mismo patrón que `InvitationLinkPanel`: se muestra una sola vez para compartirlo (hoja
 * nativa si el navegador la tiene) o copiarlo; dice si además salió por correo y hasta cuándo
 * vale. Quien lo abra puede fijar la contraseña de la cuenta: el aviso lo deja claro.
 */
const props = defineProps<{
  issued: PasswordResetIssued
  email: string
}>()

const { t, d } = useI18n()
const toast = useToast()
const { canShare, share, copy } = useShareLink()

const url = computed(() => props.issued.reset_url)
const shareable = computed(() => !!url.value && canShare(url.value))
const field = useTemplateRef<{ inputRef?: HTMLInputElement }>('field')

async function onShare() {
  const result = await share({
    title: t('members.resetLink.shareTitle'),
    text: t('members.resetLink.shareText'),
    url: url.value,
  })
  if (result === 'failed') {
    toast.add({ title: t('members.resetLink.shareFailed'), color: 'error', icon: 'i-lucide-x' })
  }
}

async function onCopy() {
  const ok = await copy(url.value, { input: field.value?.inputRef })
  toast.add(
    ok
      ? { title: t('members.resetLink.copied'), color: 'success', icon: 'i-lucide-check' }
      : { title: t('members.resetLink.copyFailed'), color: 'error', icon: 'i-lucide-x' },
  )
}
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="reset-link-panel">
    <UAlert
      color="warning"
      variant="subtle"
      icon="i-lucide-triangle-alert"
      :title="t('members.resetLink.onceTitle')"
      :description="t('members.resetLink.onceBody')"
    />
    <UFormField :label="t('members.resetLink.label')">
      <UInput
        ref="field"
        :model-value="url"
        readonly
        class="w-full"
        :ui="{ base: 'font-mono text-xs' }"
        data-testid="reset-link-url"
        @focus="($event.target as HTMLInputElement).select()"
      />
    </UFormField>
    <div class="grid gap-2 text-sm">
      <dl class="flex flex-wrap gap-x-2">
        <dt class="text-muted">{{ t('members.resetLink.expires') }}</dt>
        <dd data-testid="reset-link-expires">{{ d(new Date(issued.expires_at), 'long') }}</dd>
      </dl>
      <p class="flex gap-1.5" data-testid="reset-link-email-note">
        <UIcon
          :name="issued.email_sent ? 'i-lucide-mail-check' : 'i-lucide-mail-x'"
          class="text-muted mt-0.5 size-4 shrink-0"
          aria-hidden="true"
        />
        <span>{{
          issued.email_sent
            ? t('members.resetLink.emailSent', { email })
            : t('members.resetLink.noEmail')
        }}</span>
      </p>
    </div>
    <div class="flex flex-wrap gap-2">
      <UButton
        v-if="shareable"
        icon="i-lucide-share-2"
        :label="t('members.resetLink.share')"
        data-testid="reset-link-share"
        @click="onShare"
      />
      <UButton
        :color="shareable ? 'neutral' : 'primary'"
        :variant="shareable ? 'outline' : 'solid'"
        icon="i-lucide-copy"
        :label="t('members.resetLink.copy')"
        data-testid="reset-link-copy"
        @click="onCopy"
      />
    </div>
  </div>
</template>
