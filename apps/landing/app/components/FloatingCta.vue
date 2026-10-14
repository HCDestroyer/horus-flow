<script setup lang="ts">
// CTA flotante solo en móvil (< sm, donde la barra no muestra "Solicitar demo"): aparece al
// pasar el titular (h1) y, en la portada, se oculta cuando el formulario de demo está a la
// vista. En las páginas interiores lleva al formulario de la portada. Capa funcional: vidrio,
// y su botón con el color de la acción principal.
const { t } = useI18n()
const localePath = useLocalePath()
const visible = ref(false)
const hasDemo = ref(true)
let observer: IntersectionObserver | null = null

const to = computed(() => {
  if (hasDemo.value) return '#demo'
  const home = localePath('/')
  return `${home === '/' ? '/' : home}#demo`
})

onMounted(() => {
  const hero = document.querySelector('main h1')
  const demo = document.getElementById('demo')
  hasDemo.value = Boolean(demo)
  if (!hero || !('IntersectionObserver' in window)) return
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
  if (demo) observer.observe(demo)
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
        :to="to"
        color="primary"
        size="xl"
        block
        class="min-h-12 rounded-xl"
        :label="t('nav.demo')"
      />
    </div>
  </Transition>
</template>
