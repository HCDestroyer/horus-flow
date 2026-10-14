<script setup lang="ts">
import { sellerAddressLine, site } from '~/config/site'

const { t, locale } = useI18n()
const localePath = useLocalePath()
const year = new Date().getFullYear()
const siteData = useSite()
const email = computed(() => siteData.value.settings.contact.email || site.seller.email)
const loc = computed(() => (locale.value === 'en' ? 'en' : 'es'))
// Páginas de la base de datos con "mostrar en el pie" (/admin › Páginas): las legales en su
// columna; las informativas, al final de la de producto.
const footerPages = computed(() =>
  (siteData.value.pages ?? []).map((p) => ({
    id: p.id,
    kind: p.kind,
    title: p.title[loc.value],
    to: p.path[loc.value],
  })),
)
const legalPages = computed(() => footerPages.value.filter((p) => p.kind === 'legal'))
const infoPages = computed(() => footerPages.value.filter((p) => p.kind === 'info'))
const home = computed(() => localePath('/'))
const href = (id: string) => `${home.value === '/' ? '/' : home.value}#${id}`
</script>

<template>
  <footer class="mt-24 border-t border-default bg-muted">
    <div class="mx-auto grid max-w-6xl gap-10 px-4 py-12 sm:px-6 md:grid-cols-[1.4fr_1fr_1fr]">
      <div class="space-y-3">
        <div class="flex items-center gap-2 font-semibold text-highlighted">
          <HorusMark :size="24" class="text-primary" />
          Horus Flow
        </div>
        <p class="max-w-sm text-muted">{{ t('contact.seller') }}</p>
        <p>
          <a
            :href="`mailto:${email}`"
            class="inline-flex min-h-11 items-center font-medium text-primary underline underline-offset-4"
          >
            {{ email }}
          </a>
        </p>
      </div>
      <nav :aria-label="t('footer.product')">
        <h2 class="mb-2 text-sm font-semibold text-highlighted">{{ t('footer.product') }}</h2>
        <ul class="space-y-0.5">
          <li>
            <a
              :href="href('features')"
              class="inline-flex min-h-11 items-center text-muted hover:text-highlighted"
            >
              {{ t('nav.features') }}
            </a>
          </li>
          <li>
            <a
              :href="href('pricing')"
              class="inline-flex min-h-11 items-center text-muted hover:text-highlighted"
            >
              {{ t('nav.pricing') }}
            </a>
          </li>
          <li>
            <NuxtLink
              :to="localePath('/comprar')"
              class="inline-flex min-h-11 items-center text-muted hover:text-highlighted"
            >
              {{ t('pricing.ctaBuy') }}
            </NuxtLink>
          </li>
          <li>
            <a
              :href="href('demo')"
              class="inline-flex min-h-11 items-center text-muted hover:text-highlighted"
            >
              {{ t('nav.demo') }}
            </a>
          </li>
          <li v-for="p in infoPages" :key="p.id">
            <NuxtLink
              :to="p.to"
              class="inline-flex min-h-11 items-center text-muted hover:text-highlighted"
            >
              {{ p.title }}
            </NuxtLink>
          </li>
        </ul>
      </nav>
      <nav v-if="legalPages.length" :aria-label="t('footer.legal')">
        <h2 class="mb-2 text-sm font-semibold text-highlighted">{{ t('footer.legal') }}</h2>
        <ul class="space-y-0.5">
          <li v-for="p in legalPages" :key="p.id">
            <NuxtLink
              :to="p.to"
              class="inline-flex min-h-11 items-center text-muted hover:text-highlighted"
            >
              {{ p.title }}
            </NuxtLink>
          </li>
        </ul>
      </nav>
    </div>
    <div class="border-t border-default">
      <div class="mx-auto max-w-6xl space-y-1 px-4 py-6 text-sm text-muted sm:px-6">
        <p>
          © {{ year }} {{ site.seller.legalName }} ({{ site.seller.shortName }}).
          {{ t('footer.rights') }}
        </p>
        <p>{{ t('footer.address', { address: sellerAddressLine }) }}</p>
        <p>{{ t('footer.proprietary') }} {{ t('footer.simulated') }}</p>
      </div>
    </div>
  </footer>
</template>
