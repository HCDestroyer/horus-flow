<script setup lang="ts">
// CTA flotante solo en móvil (< sm, donde la barra no muestra "Solicitar demo"): aparece al
// pasar el hero y se oculta cuando el formulario de demo está a la vista. Capa funcional:
// vidrio, y su botón con el color de la acción principal.
const { t } = useI18n()
const visible = ref(false)
let observer: IntersectionObserver | null = null

onMounted(() => {
  const hero = document.getElementById('hero-title')
  const demo = document.getElementById('demo')
  if (!hero || !demo || !('IntersectionObserver' in window)) return
  let pastHero = false
  let demoInView = false
  observer = new IntersectionObserver((entries) => {
    for (const e of entries) {
      if (e.target === hero) pastHero = !e.isIntersecting && e.boundingClientRect.top < 0
      if (e.target === demo) demoInView = e.isIntersecting
    }
    visible.value = pastHero && !demoInView
  })
  observer.observe(hero)
  observer.observe(demo)
})
onBeforeUnmount(() => observer?.disconnect())
</script>

<template>
  <Transition
    enter-active-class="transition duration-200 ease-out"
    enter-from-class="translate-y-4 opacity-0"
    leave-active-class="transition duration-150 ease-in"
    leave-to-class="translate-y-4 opacity-0"
  >
    <div
      v-if="visible"
      class="glass fixed inset-x-3 bottom-[max(0.75rem,env(safe-area-inset-bottom))] z-40 rounded-2xl p-2 sm:hidden"
    >
      <UButton
        to="#demo"
        color="primary"
        size="xl"
        block
        class="min-h-12 rounded-xl"
        :label="t('nav.demo')"
      />
    </div>
  </Transition>
</template>
