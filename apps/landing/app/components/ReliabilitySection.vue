<script setup lang="ts">
// Cifras de tests/load/REPORT.md y README.md › "Fiabilidad y rendimiento medidos", como una
// tabla de laboratorio (prueba → resultado) en lugar de números gigantes: el contexto de cada
// cifra pesa tanto como la cifra.
const { t } = useI18n()
const rows = ['restart', 'clickhouse', 'platform', 'capacity'] as const
</script>

<template>
  <section id="reliability" aria-labelledby="reliability-title" class="mx-auto max-w-6xl px-4 py-16 sm:px-6">
    <SectionHeading
      id="reliability-title"
      :eyebrow="t('reliability.eyebrow')"
      :title="t('reliability.title')"
      :lead="t('reliability.lead')"
    />
    <div class="surface mt-10 overflow-hidden rounded-2xl">
      <table class="w-full border-collapse text-left">
        <caption class="sr-only">{{ t('reliability.title') }}</caption>
        <thead class="hidden bg-muted md:table-header-group">
          <tr>
            <th scope="col" class="w-1/2 px-6 py-3 text-sm font-semibold text-muted">{{ t('reliability.colTest') }}</th>
            <th scope="col" class="px-6 py-3 text-sm font-semibold text-muted">{{ t('reliability.colResult') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in rows" :key="r" class="block border-t border-default first:border-t-0 md:table-row md:first:border-t">
            <th scope="row" class="block px-6 pt-5 font-normal text-toned md:table-cell md:py-5 md:align-top">
              {{ t(`reliability.rows.${r}.test`) }}
            </th>
            <td class="block px-6 pt-1 pb-5 md:table-cell md:py-5 md:align-top">
              <span class="font-semibold text-highlighted">{{ t(`reliability.rows.${r}.result`) }}</span>
              <span v-if="r === 'restart'" class="mt-1 block text-[0.95rem] text-muted">
                {{ t('reliability.rows.restart.note') }}
              </span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="mt-6 grid gap-4 text-[0.95rem] text-muted md:grid-cols-2">
      <p class="flex gap-2">
        <UIcon name="i-lucide-flask-conical" class="mt-0.5 size-5 shrink-0" aria-hidden="true" />
        <span>{{ t('reliability.conditions') }}</span>
      </p>
      <p class="flex gap-2">
        <UIcon name="i-lucide-hard-drive" class="mt-0.5 size-5 shrink-0" aria-hidden="true" />
        <span>{{ t('reliability.projection') }}</span>
      </p>
    </div>
  </section>
</template>
