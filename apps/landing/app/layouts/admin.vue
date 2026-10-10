<script setup lang="ts">
// Panel de administración: herramienta de trabajo con el sistema de la landing (Apple HIG /
// Liquid Glass) a densidad media. El vidrio solo está en la capa flotante (barra lateral en
// escritorio, barra superior en móvil); tablas y formularios van en superficies sólidas.
useHead({
  htmlAttrs: { lang: 'es-GT' },
  meta: [{ name: 'robots', content: 'noindex, nofollow' }],
  titleTemplate: (title) => (title ? `${title} · Panel Horus Flow` : 'Panel Horus Flow'),
})

const route = useRoute()
const session = useAdminSession()
const open = ref(false)
const isLogin = computed(() => route.path === '/admin/login')

const nav = [
  { to: '/admin/solicitudes', label: 'Solicitudes', icon: 'i-lucide-inbox' },
  { to: '/admin/precios', label: 'Precios y planes', icon: 'i-lucide-tags' },
  { to: '/admin/pagos', label: 'Pagos', icon: 'i-lucide-credit-card' },
  { to: '/admin/ajustes', label: 'Ajustes', icon: 'i-lucide-settings' },
  { to: '/admin/auditoria', label: 'Auditoría', icon: 'i-lucide-scroll-text' },
]

watch(
  () => route.fullPath,
  () => (open.value = false),
)

async function logout() {
  try {
    await adminApi('/auth/logout', { method: 'POST' })
  } finally {
    session.value = { authenticated: false }
    await navigateTo('/admin/login')
  }
}
</script>

<template>
  <UApp :toaster="{ position: 'bottom-right' }">
    <a
      href="#admin-main"
      class="sr-only focus:not-sr-only focus:fixed focus:top-3 focus:left-3 focus:z-[60] focus:rounded-md focus:bg-(--surface) focus:px-4 focus:py-3 focus:text-default focus:shadow-lg"
    >
      Saltar al contenido
    </a>

    <div v-if="isLogin" class="min-h-dvh">
      <main id="admin-main" tabindex="-1" class="outline-none">
        <slot />
      </main>
    </div>

    <div v-else class="min-h-dvh lg:grid lg:grid-cols-[16rem_1fr]">
      <!-- Barra superior flotante (móvil y tableta). -->
      <header class="sticky top-0 z-40 px-3 pt-3 lg:hidden">
        <div class="glass flex h-14 items-center gap-2 rounded-2xl pr-2 pl-3">
          <NuxtLink
            to="/admin/solicitudes"
            class="flex min-h-11 items-center gap-2 font-semibold text-highlighted"
          >
            <HorusMark :size="24" class="text-primary" />
            Panel
          </NuxtLink>
          <UButton
            class="ml-auto min-h-11 min-w-11"
            color="neutral"
            variant="ghost"
            :icon="open ? 'i-lucide-x' : 'i-lucide-menu'"
            :aria-label="open ? 'Cerrar menú' : 'Menú'"
            :aria-expanded="open"
            aria-controls="admin-nav-mobile"
            @click="open = !open"
          />
        </div>
        <nav
          v-show="open"
          id="admin-nav-mobile"
          aria-label="Panel"
          class="surface mt-2 rounded-2xl p-2 shadow-lg"
        >
          <ul>
            <li v-for="item in nav" :key="item.to">
              <NuxtLink
                :to="item.to"
                class="flex min-h-11 items-center gap-3 rounded-lg px-3 text-toned hover:bg-elevated"
                active-class="bg-(--ui-primary)/10 font-semibold text-highlighted"
              >
                <UIcon :name="item.icon" class="size-5" aria-hidden="true" />
                {{ item.label }}
              </NuxtLink>
            </li>
            <li class="mt-1 border-t border-default pt-1">
              <button
                type="button"
                class="flex min-h-11 w-full items-center gap-3 rounded-lg px-3 text-toned hover:bg-elevated"
                @click="logout"
              >
                <UIcon name="i-lucide-log-out" class="size-5" aria-hidden="true" />
                Cerrar sesión
              </button>
            </li>
          </ul>
        </nav>
      </header>

      <!-- Barra lateral flotante (escritorio). -->
      <aside class="sticky top-0 hidden h-dvh p-3 lg:block">
        <div class="glass flex h-full flex-col rounded-2xl p-3">
          <NuxtLink
            to="/admin/solicitudes"
            class="flex min-h-11 items-center gap-2 px-2 font-semibold text-highlighted"
          >
            <HorusMark :size="26" class="text-primary" />
            <span>Horus Flow <span class="font-normal text-muted">· Panel</span></span>
          </NuxtLink>
          <nav aria-label="Panel" class="mt-4">
            <ul class="space-y-0.5">
              <li v-for="item in nav" :key="item.to">
                <NuxtLink
                  :to="item.to"
                  class="flex min-h-11 items-center gap-3 rounded-lg px-3 text-[0.95rem] text-toned transition-colors hover:bg-(--ui-bg-accented)/60 hover:text-highlighted"
                  active-class="bg-(--ui-primary)/12 font-semibold text-highlighted"
                >
                  <UIcon :name="item.icon" class="size-5" aria-hidden="true" />
                  {{ item.label }}
                </NuxtLink>
              </li>
            </ul>
          </nav>
          <div class="mt-auto border-t border-default pt-3">
            <p class="truncate px-2 text-sm text-muted" :title="session?.email">
              {{ session?.email }}
            </p>
            <button
              type="button"
              class="mt-1 flex min-h-11 w-full items-center gap-3 rounded-lg px-3 text-[0.95rem] text-toned hover:bg-(--ui-bg-accented)/60"
              @click="logout"
            >
              <UIcon name="i-lucide-log-out" class="size-5" aria-hidden="true" />
              Cerrar sesión
            </button>
          </div>
        </div>
      </aside>

      <main
        id="admin-main"
        tabindex="-1"
        class="min-w-0 px-4 pt-6 pb-16 outline-none sm:px-6 lg:px-8 lg:pt-8"
      >
        <slot />
      </main>
    </div>
  </UApp>
</template>
