<script setup lang="ts">
import type { Increment } from '~/utils/navigation'

/**
 * Secciones del ISP del mapa de navegación (frontend.md §4). Cada sección tendrá su página
 * propia cuando se implemente; hasta entonces, esta ruta comprueba que existe, que está en
 * el incremento visible y que el usuario tiene permiso (403 en contexto, §9.3).
 */
const route = useRoute()
const config = useRuntimeConfig()
const { me } = useAuth()

const path = computed(() => {
  const parts = route.params.section
  return Array.isArray(parts) ? parts.join('/') : String(parts ?? '')
})

const section = computed(() => findSection('tenant', path.value))

function check() {
  const s = section.value
  const slug = String(route.params.slug)
  if (!s || !isIncrementVisible(s.increment, config.public.navIncrement as Increment)) {
    throw createError({ statusCode: 404, statusMessage: 'Not Found' })
  }
  if (!canSeeSection(s, me.value, slug)) {
    throw createError({ statusCode: 403, statusMessage: 'Forbidden' })
  }
}

check()
watch(path, check)
</script>

<template>
  <SectionPage v-if="section" :section="section" />
</template>
