<script setup lang="ts">
/**
 * Administración › Integraciones (ADR-0030): códigos de conexión (API tokens `hf_pat_`, con la
 * plantilla "ArgosISP" preseleccionada) y conector ArgosISP. Horus funciona completo sin
 * ArgosISP; el conector es opcional y si ArgosISP no responde se conserva el último dato.
 */
const { t } = useI18n()
const route = useRoute()
const can = useCan()
const slug = computed(() => String(route.params.slug))
useSectionGuard()(findSection('tenant', 'admin/integrations'), slug.value)
const canTokens = computed(() => can('api_tokens.manage'))
</script>

<template>
  <AppPage :title="t('nav.items.integrations')" panel-id="integrations-admin">
    <template #toolbar-left>
      <p class="text-muted text-sm">{{ t('integrations.intro') }}</p>
    </template>
    <ApiTokensPanel v-if="canTokens" />
    <UAlert
      v-else
      color="neutral"
      variant="subtle"
      icon="i-lucide-lock"
      :title="t('integrations.tokens.noPermission')"
    />
    <ArgosConnectorCard />
  </AppPage>
</template>
