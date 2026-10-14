<script setup lang="ts">
// Barra de navegación flotante: la única superficie de vidrio permanente de la página
// (capa funcional, HIG liquid-glass › The two layers). El menú móvil se despliega como panel
// sólido debajo, no como vidrio sobre vidrio.
//
// Enlaces con nombre propio a cada página (apple-design §16: "Direct, specific labels") y
// Precios como ancla de la portada. La página actual lleva aria-current="page" (NuxtLink) y
// se marca con texto más intenso y un fondo; no solo con color.
const { t, locale } = useI18n()
const localePath = useLocalePath()
const switchLocalePath = useSwitchLocalePath()
const route = useRoute()

const open = ref(false)
const otherLocale = computed(() => (locale.value === 'es' ? 'en' : 'es'))

const pages = computed(() => [
  { to: localePath('/producto'), key: 'nav.product' },
  { to: localePath('/inteligencia-artificial'), key: 'nav.ai', long: 'nav.aiLong' },
  { to: localePath('/monitoreo'), key: 'nav.monitoring' },
  { to: localePath('/pantallas-noc'), key: 'nav.noc' },
])

const home = computed(() => localePath('/'))
const href = (id: string) => `${home.value === '/' ? '/' : home.value}#${id}`

watch(
  () => route.fullPath,
  () => {
    open.value = false
  },
)

const toggle = ref<HTMLButtonElement>()
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && open.value) {
    open.value = false
    toggle.value?.focus()
  }
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))

const linkClass =
  'flex min-h-11 items-center rounded-lg px-3 text-[0.95rem] text-toned transition-colors hover:bg-(--ui-bg-accented)/60 hover:text-highlighted aria-[current=page]:bg-(--ui-bg-accented)/70 aria-[current=page]:font-semibold aria-[current=page]:text-highlighted'
const mobileLinkClass =
  'flex min-h-12 items-center rounded-lg px-3 text-lg text-highlighted hover:bg-elevated aria-[current=page]:bg-(--ui-bg-accented) aria-[current=page]:font-semibold'
</script>

<template>
  <header class="site-header sticky top-0 z-50 px-3 pt-3 sm:px-4">
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

      <ul class="ml-3 hidden items-center gap-0.5 lg:flex">
        <li v-for="p in pages" :key="p.key">
          <NuxtLink :to="p.to" :class="linkClass">
            <span v-if="p.long" class="sr-only">{{ t(p.long) }}</span>
            <span :aria-hidden="p.long ? 'true' : undefined">{{ t(p.key) }}</span>
          </NuxtLink>
        </li>
        <li>
          <a :href="href('pricing')" :class="linkClass">{{ t('nav.pricing') }}</a>
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
          ref="toggle"
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

    <Transition name="menu">
      <div
        v-show="open"
        id="menu-movil"
        class="surface mx-auto mt-2 max-w-6xl origin-top rounded-2xl p-2 shadow-lg lg:hidden"
      >
        <ul class="flex flex-col">
          <li v-for="p in pages" :key="p.key">
            <NuxtLink :to="p.to" :class="mobileLinkClass" @click="open = false">
              {{ t(p.long ?? p.key) }}
            </NuxtLink>
          </li>
          <li>
            <a :href="href('pricing')" :class="mobileLinkClass" @click="open = false">
              {{ t('nav.pricing') }}
            </a>
          </li>
          <li>
            <a :href="href('faq')" :class="mobileLinkClass" @click="open = false">
              {{ t('nav.faq') }}
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
    </Transition>
  </header>
</template>

<style scoped>
/* La barra no participa en el fundido entre páginas: se queda quieta. */
.site-header {
  view-transition-name: site-header;
}

/* El panel nace de la barra (origen arriba) y vuelve por el mismo camino (apple-design §7). */
.menu-enter-active,
.menu-leave-active {
  transition:
    opacity 0.18s var(--ease-out),
    translate 0.22s var(--ease-out),
    scale 0.22s var(--ease-out);
}
.menu-enter-from,
.menu-leave-to {
  opacity: 0;
  translate: 0 -6px;
  scale: 0.98;
}
@media (prefers-reduced-motion: reduce) {
  .menu-enter-from,
  .menu-leave-to {
    translate: none;
    scale: none;
  }
}
</style>
