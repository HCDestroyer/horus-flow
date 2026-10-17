<script setup lang="ts">
import type { SsoLink } from '~~/types/api'

/**
 * Mis cuentas vinculadas para el autologin (contrato ArgosISP §1.5): qué cuenta de ArgosISP
 * entra a Horus como yo, en qué ISP y cuándo se usó por última vez. Quitar una vinculación hace
 * que el siguiente autologin vuelva a pedir iniciar sesión.
 */
const { t, d } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()

const links = ref<SsoLink[] | null>(null)
const error = ref<unknown>(null)
const loading = ref(true)

async function load() {
  loading.value = true
  error.value = null
  try {
    links.value = (await unwrap($api.GET('/me/sso-links'))).data
  } catch (e) {
    error.value = e
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function remove(l: SsoLink) {
  try {
    await unwrap($api.DELETE('/me/sso-links/{link_id}', { params: { path: { link_id: l.id } } }))
    toast.add({ title: t('account.sso.removed'), icon: 'i-lucide-check', color: 'success' })
    await load()
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  }
}

function external(l: SsoLink) {
  return (
    [l.external_user.name, l.external_user.email].filter(Boolean).join(' · ') || l.external_user.id
  )
}
</script>

<template>
  <UCard data-testid="sso-card">
    <h2 class="text-highlighted text-base font-semibold">{{ t('account.sso.title') }}</h2>
    <p class="text-muted mt-1 text-sm">{{ t('account.sso.intro') }}</p>
    <LoadingState v-if="loading && !links" :rows="1" class="mt-3" />
    <ErrorState v-else-if="error" :error="error" class="mt-3" @retry="load" />
    <p v-else-if="!links?.length" class="text-muted mt-3 text-sm" data-testid="sso-card-empty">
      {{ t('account.sso.empty') }}
    </p>
    <ul v-else class="divide-default mt-3 divide-y text-sm">
      <li
        v-for="l in links"
        :key="l.id"
        class="flex flex-wrap items-center justify-between gap-3 py-2.5"
        data-testid="sso-card-row"
      >
        <div class="min-w-0">
          <p class="text-highlighted break-words">{{ external(l) }}</p>
          <p class="text-muted text-xs">
            {{ t('account.sso.where', { tenant: l.tenant_name }) }} ·
            {{
              l.last_used_at
                ? t('account.sso.lastUsed', { time: d(new Date(l.last_used_at), 'long') })
                : t('account.sso.since', { time: d(new Date(l.created_at), 'long') })
            }}
          </p>
        </div>
        <UButton
          size="sm"
          color="neutral"
          variant="outline"
          icon="i-lucide-unlink"
          :label="t('account.sso.remove')"
          data-testid="sso-card-remove"
          @click="remove(l)"
        />
      </li>
    </ul>
  </UCard>
</template>
