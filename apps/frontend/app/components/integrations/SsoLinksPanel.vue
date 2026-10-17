<script setup lang="ts">
import type { SsoLink } from '~~/types/api'

/**
 * Cuentas de ArgosISP vinculadas a usuarios del ISP para el autologin (contrato §1.5). Cada
 * vinculación la creó su usuario iniciando sesión en Horus; aquí un administrador las ve
 * (users.read) y las revoca una a una o todas (users.manage; "todas" pide re-autenticación).
 * Revocar no toca usuarios ni permisos: el siguiente autologin vuelve a pedir iniciar sesión.
 */
const { t, d } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { withReauth } = useReauth()
const can = useCan()
const canManage = computed(() => can('users.manage'))

const {
  data: links,
  error,
  status,
  refresh,
} = useTenantQuery('sso-links', () =>
  unwrap($api.GET('/integrations/argosisp/sso/links')).then((r) => r.data),
)

function external(l: SsoLink) {
  return (
    [l.external_user.name, l.external_user.email].filter(Boolean).join(' · ') || l.external_user.id
  )
}

async function revoke(l: SsoLink) {
  try {
    await unwrap(
      $api.DELETE('/integrations/argosisp/sso/links/{link_id}', {
        params: { path: { link_id: l.id } },
      }),
    )
    toast.add({ title: t('integrations.sso.revoked'), icon: 'i-lucide-check', color: 'success' })
    refresh()
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  }
}

const revokingAll = ref(false)
async function revokeAll() {
  revokingAll.value = true
  try {
    const r = await withReauth(() => unwrap($api.DELETE('/integrations/argosisp/sso/links')))
    toast.add({
      title: t('integrations.sso.revokedAll', { n: r.revoked }),
      icon: 'i-lucide-check',
      color: 'success',
    })
    refresh()
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  } finally {
    revokingAll.value = false
  }
}
</script>

<template>
  <section class="flex flex-col gap-3" data-testid="sso-links">
    <div class="flex flex-wrap items-end justify-between gap-2">
      <div>
        <h2 class="text-highlighted font-semibold">{{ t('integrations.sso.title') }}</h2>
        <p class="text-muted text-sm">{{ t('integrations.sso.body') }}</p>
      </div>
      <UButton
        v-if="canManage && links?.length"
        color="error"
        variant="outline"
        icon="i-lucide-unlink"
        :loading="revokingAll"
        :label="t('integrations.sso.revokeAll')"
        data-testid="sso-links-revoke-all"
        @click="revokeAll"
      />
    </div>
    <LoadingState v-if="status === 'pending' && !links" :rows="2" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <UCard v-else :ui="{ body: 'p-0 sm:p-0' }">
      <p v-if="!links?.length" class="text-muted p-4 text-sm" data-testid="sso-links-empty">
        {{ t('integrations.sso.empty') }}
      </p>
      <ul v-else class="divide-default divide-y">
        <li
          v-for="l in links"
          :key="l.id"
          class="flex flex-wrap items-center justify-between gap-3 p-4"
          data-testid="sso-link-row"
        >
          <div class="min-w-0 text-sm">
            <p class="text-highlighted break-words">{{ l.user_email }}</p>
            <p class="text-muted break-words">
              {{ t('integrations.sso.linkedTo', { who: external(l) }) }}
            </p>
            <p class="text-muted text-xs">
              {{
                l.last_used_at
                  ? t('integrations.sso.lastUsed', { time: d(new Date(l.last_used_at), 'long') })
                  : t('integrations.sso.since', { time: d(new Date(l.created_at), 'long') })
              }}
            </p>
          </div>
          <UButton
            v-if="canManage"
            size="sm"
            color="neutral"
            variant="outline"
            icon="i-lucide-unlink"
            :label="t('integrations.sso.revoke')"
            @click="revoke(l)"
          />
        </li>
      </ul>
    </UCard>
  </section>
</template>
