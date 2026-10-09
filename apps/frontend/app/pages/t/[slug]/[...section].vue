<script setup lang="ts">
/**
 * Secciones del ISP del mapa de navegación (frontend.md §4). Cada sección tendrá su página
 * propia cuando se implemente; hasta entonces, esta ruta comprueba que existe, que está en
 * el incremento visible y que el usuario tiene permiso (403 en contexto, §9.3).
 */
const route = useRoute()

const path = computed(() => {
  const parts = route.params.section
  return Array.isArray(parts) ? parts.join('/') : String(parts ?? '')
})

const section = computed(() => findSection('tenant', path.value))

const guard = useSectionGuard()
function check() {
  guard(section.value, String(route.params.slug))
}

check()
watch(path, check)
</script>

<template>
  <SectionPage v-if="section" :section="section" />
</template>
