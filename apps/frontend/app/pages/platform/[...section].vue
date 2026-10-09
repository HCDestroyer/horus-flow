<script setup lang="ts">
/** Consola de plataforma (frontend.md §3.4): solo usuarios con permisos `platform.*`. */
const route = useRoute()

const path = computed(() => {
  const parts = route.params.section
  return Array.isArray(parts) ? parts.join('/') : String(parts ?? '')
})

const section = computed(() => findSection('platform', `/platform/${path.value}`))

const guard = useSectionGuard()
function check() {
  guard(section.value)
}

check()
watch(path, check)
</script>

<template>
  <SectionPage v-if="section" :section="section" />
</template>
