<script setup lang="ts">
import type { ViaPath } from '~/utils/switches'

/**
 * Camino de un switch vía router del sitio (ADR-0032, docs/tunnels.md §10): «vía router X por
 * WireGuard/IPsec/GRE», la protección heredada del túnel del router (icono + texto + color), la
 * dirección Horus con la que Horus lo consulta y su IP privada (nunca su identidad).
 */
defineProps<{ path: ViaPath; privateAddress?: string | null; tunnelDown?: boolean }>()
const { t } = useI18n()
const route = useRoute()
const slug = computed(() => String(route.params.slug ?? ''))
</script>

<template>
  <UCard data-testid="switch-via-path">
    <div class="flex flex-col gap-3">
      <div class="flex flex-wrap items-center gap-2">
        <UIcon name="i-lucide-route" class="text-muted size-5 shrink-0" aria-hidden="true" />
        <span class="text-highlighted font-medium" data-testid="switch-via-path-text">
          <i18n-t keypath="switches.path.via" scope="global">
            <template #router>
              <ULink
                :to="slug ? `/t/${slug}/routers/${path.router_id}` : undefined"
                class="text-primary underline-offset-2 hover:underline"
                >{{ path.router_name }}</ULink
              >
            </template>
            <template #transport>{{ t(`transport.mode.${path.transport}`) }}</template>
          </i18n-t>
        </span>
        <ProtectionBadge :level="path.protection_level" data-testid="switch-via-protection" />
      </div>
      <p
        v-if="tunnelDown"
        class="text-warning flex items-center gap-1.5 text-sm"
        role="status"
        data-testid="switch-via-tunnel-down"
      >
        <UIcon name="i-lucide-unplug" class="size-4 shrink-0" aria-hidden="true" />
        {{ t('switches.path.tunnelDown') }}
      </p>
      <dl class="grid grid-cols-1 gap-3 text-sm sm:grid-cols-3">
        <div class="flex flex-col gap-0.5">
          <dt class="text-muted text-xs">{{ t('switches.path.horusAddress') }}</dt>
          <dd class="text-highlighted font-mono" data-testid="switch-via-horus">
            {{ path.horus_address ?? '—' }}
          </dd>
        </div>
        <div v-if="privateAddress" class="flex flex-col gap-0.5">
          <dt class="text-muted text-xs">{{ t('switches.path.privateAddress') }}</dt>
          <dd class="text-default font-mono">{{ privateAddress }}</dd>
        </div>
        <div v-if="path.block" class="flex flex-col gap-0.5">
          <dt class="text-muted text-xs">{{ t('switches.path.block') }}</dt>
          <dd class="text-default font-mono">{{ path.block }}</dd>
        </div>
      </dl>
      <p class="text-muted text-xs">{{ t('switches.path.inherited') }}</p>
    </div>
  </UCard>
</template>
