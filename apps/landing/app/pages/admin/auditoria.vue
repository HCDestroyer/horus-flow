<script setup lang="ts">
// Registro de auditoría: quién cambió qué, con el antes y el después (sin secretos).
definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: 'Auditoría' })

interface Entry {
  id: number
  at: string
  adminEmail: string
  action: string
  entity: string
  entityId: string
  before: unknown
  after: unknown
  ip: string
}

const ACTIONS: Record<string, string> = {
  'auth.login': 'Inicio de sesión',
  'auth.login_failed': 'Inicio de sesión fallido',
  'auth.mfa_failed': 'Código de verificación fallido',
  'auth.logout': 'Cierre de sesión',
  'admin.create': 'Alta de administrador',
  'admin.delete': 'Baja de administrador',
  'admin.disable': 'Administrador desactivado',
  'admin.enable': 'Administrador activado',
  'admin.reset_totp': 'TOTP restablecido',
  'admin.totp_enabled': 'TOTP activado',
  'admin.change_password': 'Cambio de contraseña',
  'admin.recovery_codes': 'Códigos de recuperación nuevos',
  'pricing.publish': 'Precios publicados',
  'pricing.restore': 'Versión de precios restaurada',
  'payments.paypal': 'PayPal configurado',
  'payments.neo': 'Links Neo configurados',
  'payments.transfer': 'Transferencia configurada',
  'settings.update': 'Ajustes guardados',
  'request.status': 'Estado de solicitud',
  'requests.export': 'Exportación CSV',
}

const page = ref(1)
const data = ref<{ items: Entry[]; total: number; pageSize: number } | null>(null)
async function load() {
  data.value = await adminApi('/audit', { query: { page: page.value } })
}
watch(page, load)
onMounted(load)
const pages = computed(() =>
  data.value ? Math.max(1, Math.ceil(data.value.total / data.value.pageSize)) : 1,
)
const pretty = (v: unknown) => JSON.stringify(v, null, 2)
</script>

<template>
  <div>
    <AdminPageHeader
      title="Auditoría"
      lead="Cada cambio del panel: quién, cuándo, qué y los valores de antes y después."
    />
    <div class="surface overflow-hidden rounded-2xl">
      <ul class="divide-y divide-(--ui-border)">
        <li v-for="e in data?.items ?? []" :key="e.id" class="px-5 py-3">
          <div class="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <span class="font-medium text-highlighted">{{ ACTIONS[e.action] ?? e.action }}</span>
            <span v-if="e.entityId" class="font-mono text-sm text-muted"
              >{{ e.entity }} {{ e.entityId }}</span
            >
            <span class="ml-auto text-sm whitespace-nowrap text-muted">{{ formatDate(e.at) }}</span>
          </div>
          <p class="text-sm text-toned">
            {{ e.adminEmail }}<template v-if="e.ip"> · {{ e.ip }}</template>
          </p>
          <details v-if="e.before !== null || e.after !== null" class="mt-2">
            <summary class="inline-flex min-h-11 cursor-pointer items-center text-sm text-primary">
              Ver antes y después
            </summary>
            <div class="mt-2 grid gap-3 lg:grid-cols-2">
              <div>
                <p class="text-sm font-medium text-muted">Antes</p>
                <pre class="mt-1 max-h-80 overflow-auto rounded-lg bg-muted p-3 text-xs">{{
                  pretty(e.before)
                }}</pre>
              </div>
              <div>
                <p class="text-sm font-medium text-muted">Después</p>
                <pre class="mt-1 max-h-80 overflow-auto rounded-lg bg-muted p-3 text-xs">{{
                  pretty(e.after)
                }}</pre>
              </div>
            </div>
          </details>
        </li>
      </ul>
    </div>
    <nav
      v-if="pages > 1"
      aria-label="Paginación"
      class="mt-4 flex items-center justify-between gap-3"
    >
      <UButton
        color="neutral"
        variant="outline"
        class="min-h-11"
        label="Anterior"
        :disabled="page <= 1"
        @click="page--"
      />
      <span class="text-sm text-muted">Página {{ page }} de {{ pages }}</span>
      <UButton
        color="neutral"
        variant="outline"
        class="min-h-11"
        label="Siguiente"
        :disabled="page >= pages"
        @click="page++"
      />
    </nav>
  </div>
</template>
