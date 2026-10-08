<script setup lang="ts">
import type { Increment } from '~/utils/navigation'

/** Consola de plataforma (frontend.md §3.4): solo usuarios con permisos `platform.*`. */
const route = useRoute()
const config = useRuntimeConfig()
const { me } = useAuth()

const path = computed(() => {
  const parts = route.params.section
  return Array.isArray(parts) ? parts.join('/') : String(parts ?? '')
})

const section = computed(() => findSection('platform', `/platform/${path.value}`))

function check() {
  const s = section.value
  if (!s || !isIncrementVisible(s.increment, config.public.navIncrement as Increment)) {
    throw createError({ statusCode: 404, statusMessage: 'Not Found' })
  }
  if (!canSeeSection(s, me.value)) {
    throw createError({ statusCode: 403, statusMessage: 'Forbidden' })
  }
}

check()
watch(path, check)
</script>

<template>
  <SectionPage v-if="section" :section="section" />
</template>
