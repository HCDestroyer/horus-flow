<script setup lang="ts">
// Páginas legales e informativas de la web: lista y acceso al editor.
import type { AdminPage } from '#shared/pages'
import { pagePath } from '#shared/pages'

definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: 'Páginas' })

type Item = Omit<AdminPage, 'body'> & { hasEnglishBody: boolean }

const pages = ref<Item[] | null>(null)
const error = ref<string | null>(null)

async function load() {
  error.value = null
  try {
    pages.value = (await adminApi<{ pages: Item[] }>('/pages')).pages
  } catch (err) {
    error.value = adminErrorMessage(err)
  }
}
onMounted(load)
</script>

<template>
  <div>
    <AdminPageHeader
      title="Páginas"
      lead="Textos legales (aviso legal, privacidad, términos, cookies…) y páginas informativas. Se escriben en Markdown; al guardar, la web los muestra en la siguiente carga."
    >
      <template #actions>
        <UButton
          to="/admin/paginas/nuevo"
          class="min-h-11"
          icon="i-lucide-plus"
          label="Nueva página"
        />
      </template>
    </AdminPageHeader>

    <UAlert v-if="error" color="error" variant="subtle" :title="error" class="mb-6" role="alert" />

    <div class="surface overflow-hidden rounded-2xl">
      <div class="overflow-x-auto">
        <table class="w-full text-left text-[0.95rem]">
          <caption class="sr-only">
            Páginas de la web
          </caption>
          <thead class="border-b border-default bg-muted text-sm text-muted">
            <tr>
              <th scope="col" class="px-4 py-2 font-medium">Título</th>
              <th scope="col" class="px-4 py-2 font-medium">URL</th>
              <th scope="col" class="px-4 py-2 font-medium">Tipo</th>
              <th scope="col" class="px-4 py-2 font-medium">Estado</th>
              <th scope="col" class="px-4 py-2 font-medium">Pie</th>
              <th scope="col" class="px-4 py-2 font-medium">Actualizada</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="pages && !pages.length">
              <td colspan="6" class="px-4 py-6 text-center text-muted">Aún no hay páginas.</td>
            </tr>
            <tr
              v-for="p in pages ?? []"
              :key="p.id"
              class="border-b border-default last:border-0"
              :data-page-row="p.id"
            >
              <td class="px-4 py-2">
                <NuxtLink
                  :to="`/admin/paginas/${p.id}`"
                  class="inline-flex min-h-11 items-center font-medium text-highlighted underline-offset-4 hover:underline"
                >
                  {{ p.title.es }}
                </NuxtLink>
                <span
                  v-if="!p.hasEnglishBody"
                  class="ml-2 rounded-full border border-default px-2 text-xs whitespace-nowrap text-muted"
                  >Sin inglés</span
                >
              </td>
              <td class="px-4 py-2 font-mono text-sm text-toned">
                <span class="block">{{ pagePath(p.kind, 'es', p.slug.es) }}</span>
                <span class="block">{{ pagePath(p.kind, 'en', p.slug.en) }}</span>
              </td>
              <td class="px-4 py-2">{{ p.kind === 'legal' ? 'Legal' : 'Informativa' }}</td>
              <td class="px-4 py-2 whitespace-nowrap">
                <span class="inline-flex items-center gap-1.5">
                  <span
                    class="size-2 rounded-full"
                    :class="
                      p.published
                        ? 'bg-emerald-600'
                        : 'bg-transparent ring-1 ring-(--ui-text-muted)'
                    "
                    aria-hidden="true"
                  />
                  {{ p.published ? 'Publicada' : 'Sin publicar' }}
                </span>
                <span v-if="p.draftNotice" class="block text-sm text-muted">Aviso de borrador</span>
              </td>
              <td class="px-4 py-2">{{ p.showInFooter ? 'Sí' : 'No' }}</td>
              <td class="px-4 py-2 text-sm whitespace-nowrap text-muted">
                {{ formatDate(p.updatedAt, true) }}
                <span class="block">{{ p.updatedBy || '—' }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>
