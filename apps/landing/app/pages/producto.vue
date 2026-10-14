<script setup lang="ts">
// Recorrido completo del producto (/producto, /en/product): una fila por área con captura
// real (datos simulados) o ilustración, y enlaces a las páginas de detalle.
definePageMeta({ viewTransition: true })
usePageSeo('product', 'meta.productTitle', 'meta.productDescription')
usePageLd('product', 'nav.product', 'meta.productDescription')

const { t } = useI18n()
const localePath = useLocalePath()
const s = (key: string) => `product.sections.${key}`
const pts = (key: string, ids: string[]) => ids.map((id) => t(`${s(key)}.points.${id}`))

const nav = [
  'detection',
  'customers',
  'traffic',
  'monitoring',
  'noc',
  'ai',
  'onboarding',
  'console',
  'operations',
]
const ops = [
  { key: 'install', icon: 'i-lucide-terminal-square' },
  { key: 'upgrade', icon: 'i-lucide-shield-check' },
  { key: 'backups', icon: 'i-lucide-database-backup' },
  { key: 'proxy', icon: 'i-lucide-globe' },
  { key: 'offline', icon: 'i-lucide-hard-drive-download' },
  { key: 'multi', icon: 'i-lucide-layers' },
]
</script>

<template>
  <div>
    <PageIntro
      :crumb="t('nav.product')"
      :eyebrow="t('product.eyebrow')"
      :title="t('product.title')"
      :lead="t('product.lead')"
    >
      <figure class="surface overflow-hidden rounded-2xl shadow-sm">
        <div class="bg-[#121214]">
          <ShotPicture
            name="mural"
            :alt="t('product.heroAlt')"
            sizes="(min-width: 1152px) 580px, (min-width: 1024px) 50vw, calc(100vw - 32px)"
            eager
          />
        </div>
        <figcaption class="border-t border-default px-4 py-2 text-sm text-muted">
          {{ t('common.simulated') }}
        </figcaption>
      </figure>
    </PageIntro>

    <nav :aria-label="t('common.onThisPage')" class="mx-auto max-w-6xl px-4 sm:px-6">
      <ul class="flex flex-wrap gap-2 border-y border-default py-4">
        <li v-for="(n, i) in nav" :key="n" data-reveal="fade" :style="{ '--rv-i': i }">
          <a
            :href="`#${n}`"
            class="inline-flex min-h-11 items-center rounded-full border border-default bg-(--surface) px-4 text-[0.95rem] text-toned hover:border-(--ui-border-accented) hover:text-highlighted"
          >
            {{ t(`${s(n)}.nav`) }}
          </a>
        </li>
      </ul>
    </nav>

    <FeatureRow
      id="detection"
      :eyebrow="t(`${s('detection')}.eyebrow`)"
      :title="t(`${s('detection')}.title`)"
      :body="t(`${s('detection')}.body`)"
      :points="pts('detection', ['c2', 'scan', 'fanout', 'spam', 'ddos', 'asn', 'bulk'])"
      shot="c2"
      :alt="t(`${s('detection')}.alt`)"
    />
    <FeatureRow
      id="customers"
      reverse
      :eyebrow="t(`${s('customers')}.eyebrow`)"
      :title="t(`${s('customers')}.title`)"
      :body="t(`${s('customers')}.body`)"
      :points="pts('customers', ['state', 'live', 'ipv6', 'audit'])"
      shot="customer"
      :alt="t(`${s('customers')}.alt`)"
    />
    <FeatureRow
      id="traffic"
      :eyebrow="t(`${s('traffic')}.eyebrow`)"
      :title="t(`${s('traffic')}.title`)"
      :body="t(`${s('traffic')}.body`)"
      :points="pts('traffic', ['top', 'services', 'coverage'])"
      shot="traffic"
      :alt="t('features.items.traffic.alt')"
    />
    <FeatureRow
      id="monitoring"
      reverse
      :eyebrow="t(`${s('monitoring')}.eyebrow`)"
      :title="t(`${s('monitoring')}.title`)"
      :body="t(`${s('monitoring')}.body`)"
      :link="{ to: localePath('/monitoreo'), label: t(`${s('monitoring')}.cta`) }"
    >
      <template #visual>
        <RouterHealthGrid />
      </template>
    </FeatureRow>
    <FeatureRow
      id="noc"
      :eyebrow="t(`${s('noc')}.eyebrow`)"
      :title="t(`${s('noc')}.title`)"
      :body="t(`${s('noc')}.body`)"
      :link="{ to: localePath('/pantallas-noc'), label: t(`${s('noc')}.cta`) }"
      shot="security"
      :alt="t('features.items.security.alt')"
    />
    <FeatureRow
      id="ai"
      reverse
      :eyebrow="t(`${s('ai')}.eyebrow`)"
      :title="t(`${s('ai')}.title`)"
      :body="t(`${s('ai')}.body`)"
      :link="{ to: localePath('/inteligencia-artificial'), label: t(`${s('ai')}.cta`) }"
      shot="ai"
      :alt="t(`${s('ai')}.alt`)"
    />
    <FeatureRow
      id="onboarding"
      :eyebrow="t(`${s('onboarding')}.eyebrow`)"
      :title="t(`${s('onboarding')}.title`)"
      :body="t(`${s('onboarding')}.body`)"
      :points="pts('onboarding', ['ros', 'nat', 'undo'])"
      shot="router"
      :alt="t('features.items.router.alt')"
    />
    <FeatureRow
      id="console"
      reverse
      :eyebrow="t(`${s('console')}.eyebrow`)"
      :title="t(`${s('console')}.title`)"
      :body="t(`${s('console')}.body`)"
      :points="pts('console', ['reset', 'reauth', 'users', 'overlap', 'alerts'])"
    >
      <template #visual>
        <figure class="surface flex justify-center rounded-2xl p-6">
          <div class="w-52 overflow-hidden rounded-[1.5rem] border border-default sm:w-60">
            <ShotPicture name="mobile" :alt="t('features.items.mobile.alt')" sizes="240px" />
          </div>
          <figcaption class="sr-only">{{ t('common.simulated') }}</figcaption>
        </figure>
      </template>
    </FeatureRow>

    <section
      id="operations"
      aria-labelledby="operations-title"
      class="mx-auto max-w-6xl scroll-mt-24 px-4 py-14 sm:px-6"
    >
      <SectionHeading
        id="operations-title"
        :eyebrow="t(`${s('operations')}.eyebrow`)"
        :title="t(`${s('operations')}.title`)"
      />
      <ul class="mt-10 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <li
          v-for="(o, i) in ops"
          :key="o.key"
          data-reveal
          :style="{ '--rv-i': i % 3 }"
          class="surface rounded-2xl p-6"
        >
          <UIcon :name="o.icon" class="size-6 text-primary" aria-hidden="true" />
          <h3 class="mt-4 text-lg font-semibold text-highlighted">
            {{ t(`${s('operations')}.items.${o.key}.title`) }}
          </h3>
          <p class="mt-2 text-toned">{{ t(`${s('operations')}.items.${o.key}.body`) }}</p>
        </li>
      </ul>
    </section>

    <CtaBand />
    <LazyFloatingCta hydrate-on-idle />
  </div>
</template>
