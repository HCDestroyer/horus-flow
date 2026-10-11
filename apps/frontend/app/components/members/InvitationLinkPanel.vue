<script setup lang="ts">
import type { InvitationIssued } from '~~/types/api'

/**
 * Enlace de invitación recién emitido (`outcome=invited`): se muestra una sola vez para que
 * quien invita lo comparta (hoja nativa del sistema si el navegador la tiene) o lo copie.
 * Dice si además salió por correo y hasta cuándo vale.
 */
const props = defineProps<{
  issued: InvitationIssued
  email: string
  tenantName: string
}>()

const { t, d } = useI18n()
const toast = useToast()
const { canShare, share, copy } = useShareLink()

const url = computed(() => props.issued.invitation_url ?? '')
const shareable = computed(() => !!url.value && canShare(url.value))
const field = useTemplateRef<{ inputRef?: HTMLInputElement }>('field')

async function onShare() {
  const result = await share({
    title: t('members.link.shareTitle', { tenant: props.tenantName }),
    text: t('members.link.shareText', { tenant: props.tenantName }),
    url: url.value,
  })
  if (result === 'failed') {
    toast.add({ title: t('members.link.shareFailed'), color: 'error', icon: 'i-lucide-x' })
  }
}

async function onCopy() {
  const ok = await copy(url.value, { input: field.value?.inputRef })
  toast.add(
    ok
      ? { title: t('members.link.copied'), color: 'success', icon: 'i-lucide-check' }
      : { title: t('members.link.copyFailed'), color: 'error', icon: 'i-lucide-x' },
  )
}
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="invitation-panel">
    <UAlert
      color="warning"
      variant="subtle"
      icon="i-lucide-triangle-alert"
      :title="t('members.link.onceTitle')"
      :description="t('members.link.onceBody')"
    />
    <UFormField :label="t('members.link.label')">
      <UInput
        ref="field"
        :model-value="url"
        readonly
        class="w-full"
        :ui="{ base: 'font-mono text-xs' }"
        data-testid="invitation-url"
        @focus="($event.target as HTMLInputElement).select()"
      />
    </UFormField>
    <div class="grid gap-2 text-sm">
      <dl v-if="issued.expires_at" class="flex flex-wrap gap-x-2">
        <dt class="text-muted">{{ t('members.link.expires') }}</dt>
        <dd data-testid="invitation-expires">{{ d(new Date(issued.expires_at), 'long') }}</dd>
      </dl>
      <p class="flex gap-1.5" data-testid="invitation-email-note">
        <UIcon
          :name="issued.email_sent ? 'i-lucide-mail-check' : 'i-lucide-mail-x'"
          class="text-muted mt-0.5 size-4 shrink-0"
          aria-hidden="true"
        />
        <span>{{
          issued.email_sent ? t('members.link.emailSent', { email }) : t('members.link.noEmail')
        }}</span>
      </p>
    </div>
    <div class="flex flex-wrap gap-2">
      <UButton
        v-if="shareable"
        icon="i-lucide-share-2"
        :label="t('members.link.share')"
        data-testid="invitation-share"
        @click="onShare"
      />
      <UButton
        :color="shareable ? 'neutral' : 'primary'"
        :variant="shareable ? 'outline' : 'solid'"
        icon="i-lucide-copy"
        :label="t('members.link.copy')"
        data-testid="invitation-copy"
        @click="onCopy"
      />
    </div>
  </div>
</template>
