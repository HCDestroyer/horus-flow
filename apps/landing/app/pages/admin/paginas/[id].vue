<script setup lang="ts">
// Editor de una página legal o informativa (/admin/paginas/<id>, o /admin/paginas/nuevo para
// crear). Contenido en Markdown por idioma con vista previa convertida en el servidor (el mismo
// conversor seguro que la web), estado (publicada, en el pie, aviso de borrador) e historial
// de versiones con restaurar.
import {
  RESERVED_SLUGS,
  SLUG_MAX,
  SLUG_PATTERN,
  pagePath,
  type AdminPage,
  type PageKind,
  type PageLocale,
  type PageVersionSummary,
} from '#shared/pages'

definePageMeta({ layout: 'admin', middleware: 'admin' })

type Draft = Pick<
  AdminPage,
  | 'kind'
  | 'slug'
  | 'title'
  | 'summary'
  | 'body'
  | 'published'
  | 'showInFooter'
  | 'draftNotice'
  | 'sort'
>

const route = useRoute()
const toast = useToast()
const id = computed(() => String(route.params.id))
const isNew = computed(() => id.value === 'nuevo')

const saved = ref<AdminPage | null>(null)
const draft = ref<Draft | null>(null)
const versions = ref<PageVersionSummary[]>([])
const note = ref('')
const saving = ref(false)
const loadError = ref<string | null>(null)
const issues = ref<{ path: string; message: string }[]>([])
const lang = ref<PageLocale>('es')

useHead(() => ({
  title: isNew.value ? 'Nueva página' : (saved.value?.title.es ?? 'Página'),
}))

const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v))
const toDraft = (p: Draft): Draft => ({
  kind: p.kind,
  slug: { ...p.slug },
  title: { ...p.title },
  summary: { ...p.summary },
  body: { ...p.body },
  published: p.published,
  showInFooter: p.showInFooter,
  draftNotice: p.draftNotice,
  sort: p.sort,
})
const EMPTY: Draft = {
  kind: 'info',
  slug: { es: '', en: '' },
  title: { es: '', en: '' },
  summary: { es: '', en: '' },
  body: { es: '', en: '' },
  published: false,
  showInFooter: true,
  draftNotice: false,
  sort: 100,
}
const baseline = ref<Draft>(clone(EMPTY))
const dirty = computed(
  () => !!draft.value && JSON.stringify(draft.value) !== JSON.stringify(baseline.value),
)

async function load() {
  loadError.value = null
  issues.value = []
  if (isNew.value) {
    slugTouched.value = { es: false, en: false }
    saved.value = null
    versions.value = []
    baseline.value = clone(EMPTY)
    draft.value = clone(EMPTY)
    return
  }
  try {
    const r = await adminApi<{ page: AdminPage; versions: PageVersionSummary[] }>(
      `/pages/${encodeURIComponent(id.value)}`,
    )
    saved.value = r.page
    versions.value = r.versions
    baseline.value = toDraft(r.page)
    draft.value = toDraft(r.page)
  } catch (err) {
    loadError.value =
      toAdminError(err).status === 404 ? 'Esta página no existe.' : adminErrorMessage(err)
  }
}
onMounted(load)
watch(id, load)

// ---- Slugs ---------------------------------------------------------------------------------

function slugify(s: string): string {
  return s
    .normalize('NFD')
    .replace(/[̀-ͯ]/g, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, SLUG_MAX)
    .replace(/-+$/, '')
}
// Al crear, la URL se deriva del título mientras no se escriba a mano.
const slugTouched = ref({ es: false, en: false })
watch(
  () => [draft.value?.title.es, draft.value?.title.en] as const,
  () => {
    if (!isNew.value || !draft.value) return
    for (const l of ['es', 'en'] as const) {
      if (!slugTouched.value[l])
        draft.value.slug[l] = slugify(draft.value.title[l] || draft.value.title.es)
    }
  },
)
function slugProblem(s: string): string | null {
  if (!s) return 'Obligatoria.'
  if (s.length > SLUG_MAX) return `Como mucho ${SLUG_MAX} caracteres.`
  if (!SLUG_PATTERN.test(s))
    return 'Solo minúsculas sin acentos, números y guiones (p. ej. politica-de-envios).'
  if (RESERVED_SLUGS.has(s)) return 'Es una palabra reservada del sitio.'
  return null
}
const slugErrors = computed(() => ({
  es: draft.value ? slugProblem(draft.value.slug.es) : null,
  en: draft.value ? slugProblem(draft.value.slug.en) : null,
}))
const canSave = computed(
  () =>
    !!draft.value &&
    dirty.value &&
    !slugErrors.value.es &&
    !slugErrors.value.en &&
    draft.value.title.es.trim().length > 0,
)

// ---- Vista previa (HTML convertido en el servidor) -------------------------------------------

const previewHtml = ref<Record<PageLocale, string>>({ es: '', en: '' })
const previewLoading = ref(false)
const effectiveBodyLocale = (l: PageLocale): PageLocale =>
  l === 'en' && draft.value?.body.en.trim() ? 'en' : 'es'
let previewTimer: ReturnType<typeof setTimeout> | undefined
let previewSeq = 0

async function refreshPreview(l: PageLocale) {
  if (!draft.value) return
  const seq = ++previewSeq
  previewLoading.value = true
  try {
    const r = await adminApi<{ html: string }>('/pages/preview', {
      method: 'POST',
      body: { markdown: draft.value.body[effectiveBodyLocale(l)] },
    })
    if (seq === previewSeq) previewHtml.value[l] = r.html
  } catch (err) {
    toast.add({ title: adminErrorMessage(err), color: 'error' })
  } finally {
    if (seq === previewSeq) previewLoading.value = false
  }
}
watch(
  () => [lang.value, draft.value?.body.es, draft.value?.body.en] as const,
  () => {
    clearTimeout(previewTimer)
    previewTimer = setTimeout(() => refreshPreview(lang.value), 400)
  },
  { immediate: true },
)

const previewPage = computed(() => {
  const d = draft.value!
  const l = lang.value
  return {
    title: (l === 'en' && d.title.en.trim()) || d.title.es || 'Sin título',
    titleLocale: (l === 'en' && d.title.en.trim() ? 'en' : 'es') as PageLocale,
    html: previewHtml.value[l],
    contentLocale: effectiveBodyLocale(l),
    locale: l,
    draftNotice: d.draftNotice,
    updatedAt: saved.value?.updatedAt ?? new Date().toISOString(),
  }
})

// ---- Guardar, borrar, historial --------------------------------------------------------------

async function save() {
  if (!draft.value) return
  saving.value = true
  issues.value = []
  try {
    if (isNew.value) {
      const r = await adminApi<{ page: AdminPage }>('/pages', {
        method: 'POST',
        body: { page: draft.value, note: note.value },
      })
      baseline.value = toDraft(r.page)
      draft.value = toDraft(r.page)
      note.value = ''
      toast.add({ title: 'Página creada.', color: 'primary', icon: 'i-lucide-check' })
      await navigateTo(`/admin/paginas/${r.page.id}`, { replace: true })
      return
    }
    await adminApi(`/pages/${encodeURIComponent(id.value)}`, {
      method: 'POST',
      body: { page: draft.value, note: note.value, expectedUpdatedAt: saved.value?.updatedAt },
    })
    note.value = ''
    toast.add({
      title: draft.value.published
        ? 'Página guardada. La web ya muestra los cambios.'
        : 'Página guardada (sin publicar).',
      color: 'primary',
      icon: 'i-lucide-check',
    })
    await load()
  } catch (err) {
    issues.value = toAdminError(err).issues ?? []
    toast.add({ title: adminErrorMessage(err), color: 'error' })
  } finally {
    saving.value = false
  }
}

function discard() {
  draft.value = clone(baseline.value)
  issues.value = []
}

async function remove() {
  if (!saved.value) return
  if (
    !window.confirm(
      `¿Eliminar la página «${saved.value.title.es}» y su historial? Su URL dejará de existir. No se puede deshacer.`,
    )
  )
    return
  try {
    await adminApi(`/pages/${encodeURIComponent(id.value)}`, { method: 'DELETE' })
    baseline.value = clone(draft.value!)
    toast.add({ title: 'Página eliminada.', color: 'primary', icon: 'i-lucide-check' })
    await navigateTo('/admin/paginas')
  } catch (err) {
    toast.add({ title: adminErrorMessage(err), color: 'error' })
  }
}

async function viewVersion(v: number) {
  try {
    const r = await adminApi<{ page: AdminPage }>(
      `/pages/${encodeURIComponent(id.value)}/versions/${v}`,
    )
    draft.value = toDraft(r.page)
    toast.add({ title: `Versión ${v} cargada en el editor (sin guardar).`, color: 'neutral' })
  } catch (err) {
    toast.add({ title: adminErrorMessage(err), color: 'error' })
  }
}

async function restore(v: number) {
  if (!window.confirm(`¿Restaurar la versión ${v}? Se guardará como versión nueva.`)) return
  try {
    await adminApi(`/pages/${encodeURIComponent(id.value)}/versions/${v}/restore`, {
      method: 'POST',
    })
    toast.add({
      title: `Versión ${v} restaurada.`,
      color: 'primary',
      icon: 'i-lucide-check',
    })
    await load()
  } catch (err) {
    issues.value = toAdminError(err).issues ?? []
    toast.add({ title: adminErrorMessage(err), color: 'error' })
  }
}

onBeforeRouteLeave(() => {
  if (dirty.value && !window.confirm('Hay cambios sin guardar. ¿Salir sin guardarlos?'))
    return false
})

const FIELD: Record<string, string> = {
  kind: 'Tipo',
  'slug.es': 'URL en español',
  'slug.en': 'URL en inglés',
  'title.es': 'Título (español)',
  'title.en': 'Título (inglés)',
  'summary.es': 'Resumen (español)',
  'summary.en': 'Resumen (inglés)',
  'body.es': 'Contenido (español)',
  'body.en': 'Contenido (inglés)',
  sort: 'Orden',
}
const ISSUE: Record<string, string> = {
  required: 'es obligatorio',
  tooLong: 'es demasiado largo',
  slugFormat: 'solo minúsculas sin acentos, números y guiones',
  slugReserved: 'es una palabra reservada',
  SLUG_TAKEN: 'ya la usa otra página (en cualquiera de los dos idiomas)',
  KIND_LOCKED: 'no se puede cambiar',
}
const issueText = (i: { path: string; message: string }) =>
  `${FIELD[i.path] ?? (i.path || 'Página')}: ${ISSUE[i.message] ?? i.message}`

const KINDS: { value: PageKind; label: string; hint: string }[] = [
  { value: 'legal', label: 'Legal', hint: 'En /legal/…; no se puede borrar, solo despublicar.' },
  { value: 'info', label: 'Informativa', hint: 'En /info/…; se puede borrar.' },
]
const tabs = [
  { label: 'Español', value: 'es', slot: 'es' as const },
  { label: 'Inglés', value: 'en', slot: 'en' as const },
]
const publicUrl = computed(() =>
  saved.value?.published ? pagePath(saved.value.kind, 'es', saved.value.slug.es) : null,
)
</script>

<template>
  <div>
    <AdminPageHeader
      :title="isNew ? 'Nueva página' : (saved?.title.es ?? 'Página')"
      lead="Escribe en Markdown. La vista previa usa el mismo conversor que la web: el HTML que escribas se muestra como texto y solo se admiten enlaces http, https, mailto y relativos."
    >
      <template #actions>
        <UButton
          to="/admin/paginas"
          color="neutral"
          variant="ghost"
          class="min-h-11"
          icon="i-lucide-arrow-left"
          label="Todas las páginas"
        />
        <UButton
          v-if="publicUrl"
          :to="publicUrl"
          target="_blank"
          color="neutral"
          variant="outline"
          class="min-h-11"
          icon="i-lucide-external-link"
          label="Ver en la web"
        />
      </template>
    </AdminPageHeader>

    <UAlert
      v-if="loadError"
      color="error"
      variant="subtle"
      :title="loadError"
      class="mb-6"
      role="alert"
    />

    <template v-if="draft">
      <div
        class="sticky top-20 z-30 mb-6 flex flex-wrap items-center gap-3 rounded-2xl px-4 py-3 lg:top-3"
        :class="dirty ? 'glass' : 'surface'"
        role="region"
        aria-label="Guardar"
      >
        <p
          class="text-[0.95rem]"
          :class="dirty ? 'font-medium text-highlighted' : 'text-muted'"
          aria-live="polite"
        >
          {{ dirty ? 'Hay cambios sin guardar.' : 'Sin cambios.' }}
        </p>
        <div class="ml-auto flex flex-wrap items-end gap-2">
          <label for="page-note" class="sr-only">Nota de la versión</label>
          <UInput id="page-note" v-model="note" placeholder="Nota (opcional)" class="w-56" />
          <UButton
            color="neutral"
            variant="ghost"
            class="min-h-11"
            label="Descartar"
            :disabled="!dirty"
            @click="discard"
          />
          <UButton
            class="min-h-11"
            icon="i-lucide-save"
            :label="isNew ? 'Crear página' : 'Guardar'"
            :loading="saving"
            :disabled="!canSave"
            @click="save"
          />
        </div>
      </div>

      <UAlert
        v-if="issues.length"
        color="error"
        variant="subtle"
        title="No se pudo guardar"
        class="mb-6"
        role="alert"
      >
        <template #description>
          <ul class="list-disc pl-5">
            <li v-for="(i, k) in issues" :key="k">{{ issueText(i) }}</li>
          </ul>
        </template>
      </UAlert>

      <div class="space-y-6">
        <section class="surface rounded-2xl p-5" aria-labelledby="pg-general">
          <h2 id="pg-general" class="text-lg font-semibold text-highlighted">General</h2>
          <div class="mt-4 grid gap-5">
            <fieldset v-if="isNew">
              <legend class="mb-1 text-sm font-medium">Tipo</legend>
              <div class="flex flex-wrap gap-x-6">
                <label
                  v-for="k in KINDS"
                  :key="k.value"
                  :for="`pg-kind-${k.value}`"
                  class="flex min-h-11 items-center gap-2"
                >
                  <input
                    :id="`pg-kind-${k.value}`"
                    v-model="draft.kind"
                    type="radio"
                    name="pg-kind"
                    :value="k.value"
                    class="size-5 accent-(--ui-primary)"
                  />
                  <span>
                    <span class="font-medium">{{ k.label }}</span>
                    <span class="block text-sm text-muted">{{ k.hint }}</span>
                  </span>
                </label>
              </div>
            </fieldset>
            <p v-else class="text-[0.95rem]">
              <span class="font-medium">Tipo:</span>
              {{ draft.kind === 'legal' ? 'Legal' : 'Informativa' }}
              <span class="text-muted">
                {{
                  draft.kind === 'legal'
                    ? '— no se puede borrar; desmarca «Publicada» para retirarla de la web.'
                    : ''
                }}
              </span>
            </p>

            <AdminLocalizedInput id="pg-title" v-model="draft.title" label="Título" required />

            <fieldset class="min-w-0">
              <legend class="mb-1 text-sm font-medium">
                URL<span class="ms-0.5 text-error" aria-hidden="true">*</span>
              </legend>
              <div class="grid gap-3 sm:grid-cols-2">
                <div v-for="l in ['es', 'en'] as const" :key="l" class="min-w-0">
                  <label :for="`pg-slug-${l}`" class="mb-1 block text-sm text-muted">
                    {{ l === 'es' ? 'Español' : 'Inglés' }}:
                    <span class="font-mono">{{ pagePath(draft.kind, l, '') }}</span>
                  </label>
                  <UInput
                    :id="`pg-slug-${l}`"
                    v-model.trim="draft.slug[l]"
                    class="w-full font-mono"
                    required
                    autocomplete="off"
                    spellcheck="false"
                    :maxlength="SLUG_MAX"
                    :aria-invalid="!!slugErrors[l] && !!draft.slug[l]"
                    :aria-describedby="`pg-slug-${l}-help`"
                    @update:model-value="slugTouched[l] = true"
                  />
                  <p
                    :id="`pg-slug-${l}-help`"
                    class="mt-1 text-sm"
                    :class="slugErrors[l] && draft.slug[l] ? 'text-error' : 'text-muted'"
                  >
                    {{
                      draft.slug[l] && slugErrors[l]
                        ? slugErrors[l]
                        : 'Minúsculas, números y guiones. Cambiarla rompe los enlaces a la URL anterior.'
                    }}
                  </p>
                </div>
              </div>
            </fieldset>

            <AdminLocalizedInput
              id="pg-summary"
              v-model="draft.summary"
              label="Resumen (descripción para buscadores)"
              multiline
            />

            <div class="flex flex-wrap gap-x-6">
              <label for="pg-published" class="flex min-h-11 items-center gap-2">
                <input
                  id="pg-published"
                  v-model="draft.published"
                  type="checkbox"
                  class="size-5 accent-(--ui-primary)"
                />
                Publicada
              </label>
              <label for="pg-footer" class="flex min-h-11 items-center gap-2">
                <input
                  id="pg-footer"
                  v-model="draft.showInFooter"
                  type="checkbox"
                  class="size-5 accent-(--ui-primary)"
                />
                Mostrar en el pie de página
              </label>
              <label for="pg-draft" class="flex min-h-11 items-center gap-2">
                <input
                  id="pg-draft"
                  v-model="draft.draftNotice"
                  type="checkbox"
                  class="size-5 accent-(--ui-primary)"
                />
                Aviso «Borrador pendiente de revisión legal»
              </label>
            </div>
            <div>
              <label for="pg-sort" class="mb-1 block text-sm font-medium">Orden en el pie</label>
              <UInput
                id="pg-sort"
                v-model.number="draft.sort"
                type="number"
                min="0"
                step="10"
                inputmode="numeric"
                class="w-32 tabular"
                aria-describedby="pg-sort-help"
              />
              <p id="pg-sort-help" class="mt-1 text-sm text-muted">De menor a mayor.</p>
            </div>
          </div>
        </section>

        <section class="surface rounded-2xl p-5" aria-labelledby="pg-content">
          <h2 id="pg-content" class="text-lg font-semibold text-highlighted">Contenido</h2>
          <p class="mt-1 text-[0.95rem] text-toned">
            Si el inglés queda vacío, la web en inglés muestra el texto en español con un aviso.
          </p>
          <UTabs v-model="lang" :items="tabs" class="mt-4" :unmount-on-hide="false">
            <template v-for="t in tabs" :key="t.value" #[t.slot]>
              <div class="mt-2 grid gap-4 xl:grid-cols-2">
                <div class="min-w-0">
                  <label :for="`pg-body-${t.value}`" class="mb-1 block text-sm font-medium">
                    Markdown ({{ t.label.toLowerCase() }})
                  </label>
                  <UTextarea
                    :id="`pg-body-${t.value}`"
                    v-model="draft.body[t.value as PageLocale]"
                    :rows="22"
                    class="w-full"
                    :ui="{ base: 'font-mono text-sm leading-relaxed' }"
                    spellcheck="true"
                    :lang="t.value"
                  />
                  <details class="mt-2 text-sm text-toned">
                    <summary class="flex min-h-11 cursor-pointer items-center font-medium">
                      Ayuda de Markdown
                    </summary>
                    <ul class="list-disc space-y-1 pl-5">
                      <li>
                        <code>## Título de sección</code> (y <code>###</code> para subsecciones)
                      </li>
                      <li>Una línea en blanco separa los párrafos.</li>
                      <li><code>**negrita**</code> y <code>*cursiva*</code></li>
                      <li>Listas: líneas que empiezan por <code>- </code> o <code>1. </code></li>
                      <li>
                        Enlaces: <code>[texto](https://…)</code>,
                        <code>[texto](/legal/privacidad)</code> o
                        <code>[correo](mailto:info@kns.gt)</code>; las URL sueltas se enlazan solas.
                      </li>
                      <li>No se admiten imágenes ni HTML (se muestra como texto).</li>
                    </ul>
                  </details>
                </div>
                <section
                  :aria-label="`Vista previa (${t.label.toLowerCase()})`"
                  class="min-w-0 rounded-2xl border border-dashed border-(--ui-border-accented) bg-default pb-6"
                  :aria-busy="previewLoading"
                >
                  <p class="px-4 pt-3 text-sm font-medium text-muted sm:px-6">Vista previa</p>
                  <LegalPage v-if="t.value === lang" :page="previewPage" preview class="!pt-4" />
                </section>
              </div>
            </template>
          </UTabs>
        </section>

        <section v-if="!isNew" class="surface rounded-2xl p-5" aria-labelledby="pg-danger">
          <h2 id="pg-danger" class="text-lg font-semibold text-highlighted">Eliminar</h2>
          <p v-if="draft.kind === 'legal'" class="mt-1 text-toned">
            Las páginas legales no se pueden eliminar: desmarca «Publicada» y guarda para retirarla
            de la web (la URL dará 404).
          </p>
          <template v-else>
            <p class="mt-1 text-toned">
              Elimina la página y su historial. La auditoría conserva su contenido.
            </p>
            <UButton
              color="error"
              variant="outline"
              class="mt-3 min-h-11"
              icon="i-lucide-trash-2"
              label="Eliminar página"
              @click="remove"
            />
          </template>
        </section>
      </div>

      <section v-if="!isNew" class="surface mt-8 rounded-2xl" aria-labelledby="pg-history">
        <h2 id="pg-history" class="px-5 pt-5 text-lg font-semibold text-highlighted">
          Historial de versiones
        </h2>
        <div class="mt-3 overflow-x-auto">
          <table class="w-full text-left text-[0.95rem]">
            <thead class="border-y border-default bg-muted text-sm text-muted">
              <tr>
                <th scope="col" class="px-5 py-2 font-medium">Versión</th>
                <th scope="col" class="px-5 py-2 font-medium">Fecha</th>
                <th scope="col" class="px-5 py-2 font-medium">Quién</th>
                <th scope="col" class="px-5 py-2 font-medium">Nota</th>
                <th scope="col" class="px-5 py-2 font-medium">
                  <span class="sr-only">Acciones</span>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="(v, k) in versions"
                :key="v.id"
                class="border-b border-default last:border-0"
              >
                <td class="px-5 py-2 font-mono">
                  {{ v.id }}
                  <span
                    v-if="k === 0"
                    class="ml-1 rounded-full bg-(--ui-primary)/12 px-2 font-sans text-sm text-highlighted"
                    >Actual</span
                  >
                </td>
                <td class="px-5 py-2 whitespace-nowrap text-muted">
                  {{ formatDate(v.createdAt) }}
                </td>
                <td class="px-5 py-2">{{ v.adminEmail ?? '—' }}</td>
                <td class="px-5 py-2 text-toned">
                  {{ v.note || (v.restoredFrom ? `Restaurada de la ${v.restoredFrom}` : '—') }}
                </td>
                <td class="px-5 py-2 text-right whitespace-nowrap">
                  <UButton
                    color="neutral"
                    variant="ghost"
                    class="min-h-11"
                    label="Ver"
                    :aria-label="`Ver versión ${v.id}`"
                    @click="viewVersion(v.id)"
                  />
                  <UButton
                    v-if="k > 0"
                    color="neutral"
                    variant="outline"
                    class="min-h-11"
                    label="Restaurar"
                    :aria-label="`Restaurar versión ${v.id}`"
                    @click="restore(v.id)"
                  />
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </template>
  </div>
</template>
