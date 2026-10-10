<script setup lang="ts">
import { site } from '~/config/site'

const { t } = useI18n()
const localePath = useLocalePath()
const year = new Date().getFullYear()
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
            :href="`mailto:${site.seller.email}`"
            class="inline-flex min-h-11 items-center font-medium text-primary underline underline-offset-4"
          >
            {{ site.seller.email }}
          </a>
        </p>
      </div>
      <nav :aria-label="t('footer.product')">
        <h2 class="mb-2 text-sm font-semibold text-highlighted">{{ t('footer.product') }}</h2>
        <ul class="space-y-0.5">
          <li>
            <a :href="href('features')" class="inline-flex min-h-11 items-center text-muted hover:text-highlighted">
              {{ t('nav.features') }}
            </a>
          </li>
          <li>
            <a :href="href('pricing')" class="inline-flex min-h-11 items-center text-muted hover:text-highlighted">
              {{ t('nav.pricing') }}
            </a>
          </li>
          <li>
            <NuxtLink :to="localePath('/comprar')" class="inline-flex min-h-11 items-center text-muted hover:text-highlighted">
              {{ t('pricing.ctaBuy') }}
            </NuxtLink>
          </li>
          <li>
            <a :href="href('demo')" class="inline-flex min-h-11 items-center text-muted hover:text-highlighted">
              {{ t('nav.demo') }}
            </a>
          </li>
        </ul>
      </nav>
      <nav :aria-label="t('footer.legal')">
        <h2 class="mb-2 text-sm font-semibold text-highlighted">{{ t('footer.legal') }}</h2>
        <ul class="space-y-0.5">
          <li>
            <NuxtLink :to="localePath('/legal/aviso-legal')" class="inline-flex min-h-11 items-center text-muted hover:text-highlighted">
              {{ t('footer.notice') }}
            </NuxtLink>
          </li>
          <li>
            <NuxtLink :to="localePath('/legal/privacidad')" class="inline-flex min-h-11 items-center text-muted hover:text-highlighted">
              {{ t('footer.privacy') }}
            </NuxtLink>
          </li>
          <li>
            <NuxtLink :to="localePath('/legal/terminos')" class="inline-flex min-h-11 items-center text-muted hover:text-highlighted">
              {{ t('footer.terms') }}
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
        <p>{{ t('footer.proprietary') }} {{ t('footer.simulated') }}</p>
      </div>
    </div>
  </footer>
</template>
