<script setup lang="ts">
// Barra de navegación flotante: la única superficie de vidrio permanente de la página
// (capa funcional, HIG liquid-glass › The two layers). El menú móvil se despliega como panel
// sólido debajo, no como vidrio sobre vidrio.
const { t, locale } = useI18n()
const localePath = useLocalePath()
const switchLocalePath = useSwitchLocalePath()
const route = useRoute()

const open = ref(false)
const otherLocale = computed(() => (locale.value === 'es' ? 'en' : 'es'))

const sections = [
  { id: 'features', key: 'nav.features' },
  { id: 'how', key: 'nav.how' },
  { id: 'reliability', key: 'nav.reliability' },
  { id: 'pricing', key: 'nav.pricing' },
  { id: 'faq', key: 'nav.faq' },
]

const home = computed(() => localePath('/'))
const href = (id: string) => `${home.value === '/' ? '/' : home.value}#${id}`

watch(
  () => route.fullPath,
  () => {
    open.value = false
  },
)

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && open.value) open.value = false
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <header class="sticky top-0 z-50 px-3 pt-3 sm:px-4">
    <nav
      :aria-label="t('nav.primary')"
      class="glass mx-auto flex h-14 max-w-6xl items-center gap-2 rounded-2xl pr-2 pl-3 sm:pl-4"
    >
      <NuxtLink
        :to="home"
        class="flex min-h-11 items-center gap-2 rounded-lg pr-2 font-semibold text-highlighted"
        :aria-label="t('nav.home')"
      >
        <HorusMark :size="26" class="text-primary" />
        <span class="text-[1.05rem] tracking-tight">Horus Flow</span>
      </NuxtLink>

      <ul class="ml-4 hidden items-center gap-1 lg:flex">
        <li v-for="s in sections" :key="s.id">
          <a
            :href="href(s.id)"
            class="flex min-h-11 items-center rounded-lg px-3 text-[0.95rem] text-toned transition-colors hover:bg-(--ui-bg-accented)/60 hover:text-highlighted"
          >
            {{ t(s.key) }}
          </a>
        </li>
      </ul>

      <div class="ml-auto flex items-center gap-1">
        <NuxtLink
          :to="switchLocalePath(otherLocale)"
          :hreflang="otherLocale"
          :lang="otherLocale"
          class="flex min-h-11 min-w-11 items-center justify-center rounded-lg px-3 text-[0.95rem] text-toned hover:bg-(--ui-bg-accented)/60 hover:text-highlighted"
          :aria-label="`${t('nav.language')}: ${t('nav.switchTo')}`"
        >
          <UIcon name="i-lucide-languages" class="size-5 sm:mr-1.5" aria-hidden="true" />
          <span class="hidden sm:inline">{{ t('nav.switchTo') }}</span>
        </NuxtLink>
        <UButton
          :to="href('demo')"
          color="primary"
          size="lg"
          class="hidden min-h-11 rounded-xl px-4 sm:inline-flex"
          :label="t('nav.demo')"
        />
        <button
          type="button"
          class="flex size-11 items-center justify-center rounded-lg text-highlighted hover:bg-(--ui-bg-accented)/60 lg:hidden"
          :aria-expanded="open"
          aria-controls="menu-movil"
          :aria-label="open ? t('nav.closeMenu') : t('nav.menu')"
          @click="open = !open"
        >
          <UIcon :name="open ? 'i-lucide-x' : 'i-lucide-menu'" class="size-6" aria-hidden="true" />
        </button>
      </div>
    </nav>

    <div
      v-show="open"
      id="menu-movil"
      class="surface mx-auto mt-2 max-w-6xl rounded-2xl p-2 shadow-lg lg:hidden"
    >
      <ul class="flex flex-col">
        <li v-for="s in sections" :key="s.id">
          <a
            :href="href(s.id)"
            class="flex min-h-12 items-center rounded-lg px-3 text-lg text-highlighted hover:bg-elevated"
            @click="open = false"
          >
            {{ t(s.key) }}
          </a>
        </li>
        <li class="p-1 pt-2 sm:hidden">
          <UButton
            :to="href('demo')"
            color="primary"
            size="xl"
            block
            class="min-h-12 rounded-xl"
            :label="t('nav.demo')"
            @click="open = false"
          />
        </li>
      </ul>
    </div>
  </header>
</template>
