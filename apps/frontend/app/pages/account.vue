<script setup lang="ts">
/** Mi cuenta (fuera de la barra, frontend.md §4). Gestión de TOTP y sesiones: historias de auth. */
const { t } = useI18n()
const { me } = useAuth()
</script>

<template>
  <AppPage :title="t('account.title')" panel-id="account">
    <UCard v-if="me" class="max-w-xl">
      <div class="mb-4 flex items-center gap-3">
        <UAvatar :alt="me.display_name" size="lg" />
        <div class="min-w-0">
          <p class="text-highlighted truncate font-semibold">{{ me.display_name }}</p>
          <p class="text-muted truncate text-sm">{{ me.email }}</p>
        </div>
      </div>
      <dl class="divide-default divide-y text-sm">
        <div class="flex justify-between gap-4 py-2.5">
          <dt class="text-muted">{{ t('account.mfa') }}</dt>
          <dd>
            <UBadge
              :color="me.mfa_enabled ? 'success' : 'neutral'"
              variant="subtle"
              :icon="me.mfa_enabled ? 'i-lucide-shield-check' : 'i-lucide-shield-off'"
              :label="me.mfa_enabled ? t('account.mfaOn') : t('account.mfaOff')"
            />
          </dd>
        </div>
        <div class="flex justify-between gap-4 py-2.5">
          <dt class="text-muted">{{ t('account.timeZone') }}</dt>
          <dd class="font-mono">{{ me.timezone ?? '—' }}</dd>
        </div>
      </dl>
    </UCard>
  </AppPage>
</template>
