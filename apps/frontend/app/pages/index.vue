<script setup lang="ts">
/**
 * Entrada: abre el último ISP usado o el primero alfabéticamente (frontend.md §3.2). Un
 * usuario solo de plataforma va a la consola de plataforma.
 */
const { me } = useAuth()

const tenant = pickDefaultTenant(me.value, lastTenant())
if (tenant) {
  await navigateTo(`/t/${tenant.tenant_slug}`, { replace: true })
} else if (isPlatformUser(me.value)) {
  await navigateTo('/platform/isps', { replace: true })
} else {
  throw createError({ statusCode: 404, statusMessage: 'Not Found' })
}
</script>

<template>
  <div />
</template>
