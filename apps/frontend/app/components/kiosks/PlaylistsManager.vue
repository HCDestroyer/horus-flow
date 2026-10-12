<script setup lang="ts">
import type { Kiosk, Playlist } from '~~/types/api'

/**
 * Listas de reproducción de las pantallas NOC (C9, playlist.schema.json): qué dashboards rota
 * cada kiosco y cuánto dura cada uno (10–3600 s, 1–20 por lista). Solo dashboards compartidos
 * con el ISP o de sistema: el servidor rechaza los privados en una lista. Editar una lista la
 * aplica en las TV que la usan (horus.analytics.playlist.updated) sin volver a enrolarlas.
 */
const props = defineProps<{ playlists: Playlist[]; kiosks: Kiosk[] }>()
const emit = defineEmits<{ changed: [] }>()

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { data: dashboards } = useDashboardList()

const shareable = computed(() => (dashboards.value ?? []).filter((x) => x.visibility !== 'private'))
const dashboardItems = computed(() => shareable.value.map((x) => ({ label: x.name, value: x.id })))
const dashboardName = (id: string) =>
  dashboards.value?.find((x) => x.id === id)?.name ?? t('playlists.unknownDashboard')
const usedBy = (p: Playlist) =>
  props.kiosks.filter((k) => k.playlist_id === p.id && k.status !== 'revoked')
const totalSeconds = (p: Playlist) => p.items.reduce((s, i) => s + i.duration_seconds, 0)

// --- Formulario ---------------------------------------------------------------------------
type Row = { dashboard_id: string; duration_seconds: number }
const open = ref(false)
const editing = ref<Playlist | null>(null)
const form = reactive({ name: '', transition: 'fade' as 'fade' | 'none', items: [] as Row[] })
const errors = ref<Record<string, string>>({})
const saving = ref(false)

function openForm(p?: Playlist) {
  editing.value = p ?? null
  Object.assign(form, {
    name: p?.name ?? '',
    transition: p?.transition ?? 'fade',
    items: p
      ? p.items.map((i) => ({ dashboard_id: i.dashboard_id, duration_seconds: i.duration_seconds }))
      : shareable.value.slice(0, 1).map((x) => ({ dashboard_id: x.id, duration_seconds: 30 })),
  })
  errors.value = {}
  open.value = true
}
function addRow() {
  const used = new Set(form.items.map((i) => i.dashboard_id))
  const next = shareable.value.find((x) => !used.has(x.id)) ?? shareable.value[0]
  if (next) form.items.push({ dashboard_id: next.id, duration_seconds: 30 })
}
function move(i: number, delta: number) {
  const j = i + delta
  if (j < 0 || j >= form.items.length) return
  const [row] = form.items.splice(i, 1)
  form.items.splice(j, 0, row!)
}

function validate() {
  const e: Record<string, string> = {}
  if (!form.name.trim()) e.name = t('playlists.errors.name')
  if (!form.items.length || form.items.length > 20) e.items = t('playlists.errors.count')
  else if (form.items.some((i) => !i.dashboard_id)) e.items = t('playlists.errors.dashboard')
  else if (
    form.items.some(
      (i) =>
        !Number.isInteger(i.duration_seconds) ||
        i.duration_seconds < 10 ||
        i.duration_seconds > 3600,
    )
  )
    e.items = t('playlists.errors.duration')
  errors.value = e
  return !Object.keys(e).length
}

async function save() {
  if (!validate()) return
  saving.value = true
  const body = {
    name: form.name.trim(),
    transition: form.transition,
    items: form.items.map((i) => ({
      dashboard_id: i.dashboard_id,
      duration_seconds: i.duration_seconds,
    })),
  }
  try {
    if (editing.value) {
      await unwrap(
        $api.PATCH('/playlists/{playlist_id}', {
          params: {
            path: { playlist_id: editing.value.id },
            header: { 'If-Match': ifMatch(editing.value.version) },
          },
          body,
        }),
      )
    } else {
      await unwrap($api.POST('/playlists', { body }))
    }
    open.value = false
    toast.add({
      title: t(editing.value ? 'playlists.updated' : 'playlists.created', { name: body.name }),
      icon: 'i-lucide-check',
      color: 'success',
    })
    emit('changed')
  } catch (e) {
    if (e instanceof ApiError && e.problem.errors?.length) {
      errors.value = Object.fromEntries(
        e.problem.errors.map((x) => [x.field.split(/[.[]/)[0]!, x.message]),
      )
    } else errors.value = { _: t(describeError(e).messageKey) }
  } finally {
    saving.value = false
  }
}

// --- Borrar -------------------------------------------------------------------------------
const deleting = ref<Playlist | null>(null)
const deleteOpen = computed({
  get: () => !!deleting.value,
  set: (v) => {
    if (!v) deleting.value = null
  },
})
async function remove() {
  const p = deleting.value
  if (!p) return
  try {
    await unwrap(
      $api.DELETE('/playlists/{playlist_id}', {
        params: { path: { playlist_id: p.id }, header: { 'If-Match': ifMatch(p.version) } },
      }),
    )
    toast.add({
      title: t('playlists.deleted', { name: p.name }),
      icon: 'i-lucide-check',
      color: 'success',
    })
    emit('changed')
  } catch (e) {
    const inUse = e instanceof ApiError && e.problem.status === 409
    toast.add({
      title: inUse ? t('playlists.inUse') : t(describeError(e).messageKey),
      color: 'error',
    })
  }
  deleting.value = null
}

const transitionItems = computed(() => [
  { label: t('playlists.transition.fade'), value: 'fade' },
  { label: t('playlists.transition.none'), value: 'none' },
])
const minutes = (s: number) =>
  s >= 60 ? `${Math.floor(s / 60)} min ${s % 60 ? `${s % 60} s` : ''}`.trim() : `${s} s`
</script>

<template>
  <section class="flex flex-col gap-3" data-testid="playlists">
    <div class="flex flex-wrap items-end justify-between gap-2">
      <div>
        <h2 class="text-highlighted font-semibold">{{ t('playlists.title') }}</h2>
        <p class="text-muted text-sm">{{ t('playlists.intro') }}</p>
      </div>
      <UButton
        icon="i-lucide-list-plus"
        color="neutral"
        variant="outline"
        :label="t('playlists.add')"
        data-testid="add-playlist"
        @click="openForm()"
      />
    </div>
    <p v-if="!playlists.length" class="text-muted text-sm" data-testid="playlists-empty">
      {{ t('playlists.empty') }}
    </p>
    <ul v-else class="grid gap-3 lg:grid-cols-2">
      <li v-for="p in playlists" :key="p.id" :data-playlist="p.name">
        <UCard :ui="{ body: 'p-4 sm:p-4 flex flex-col gap-2' }">
          <div class="flex items-start justify-between gap-2">
            <h3 class="text-highlighted font-medium">{{ p.name }}</h3>
            <UBadge
              color="neutral"
              variant="subtle"
              :label="t('playlists.usedBy', { n: usedBy(p).length }, usedBy(p).length)"
            />
          </div>
          <ol class="text-muted list-decimal space-y-0.5 ps-5 text-sm">
            <li v-for="(it, i) in p.items" :key="i">
              {{ dashboardName(it.dashboard_id) }}
              <span class="text-dimmed tabular">· {{ minutes(it.duration_seconds) }}</span>
            </li>
          </ol>
          <p class="text-dimmed text-xs">
            {{ t('playlists.cycle', { time: minutes(totalSeconds(p)) }) }} ·
            {{ t(`playlists.transition.${p.transition}`) }}
          </p>
          <div class="flex flex-wrap gap-2">
            <UButton
              size="sm"
              color="neutral"
              variant="outline"
              icon="i-lucide-pencil"
              :label="t('playlists.edit')"
              data-testid="edit-playlist"
              @click="openForm(p)"
            />
            <UButton
              size="sm"
              color="neutral"
              variant="ghost"
              icon="i-lucide-trash-2"
              :label="t('playlists.delete')"
              :disabled="usedBy(p).length > 0"
              :title="usedBy(p).length ? t('playlists.inUse') : undefined"
              data-testid="delete-playlist"
              @click="deleting = p"
            />
          </div>
        </UCard>
      </li>
    </ul>

    <USlideover
      v-model:open="open"
      :title="editing ? t('playlists.form.editTitle') : t('playlists.form.title')"
      :description="t('playlists.form.description')"
    >
      <template #body>
        <form id="playlist-form" class="flex flex-col gap-4" @submit.prevent="save">
          <UFormField :label="t('playlists.form.name')" :error="errors.name" required>
            <UInput
              v-model="form.name"
              class="w-full"
              maxlength="80"
              :placeholder="t('playlists.form.namePlaceholder')"
              data-testid="playlist-name"
            />
          </UFormField>
          <UFormField :label="t('playlists.form.transition')">
            <USelect v-model="form.transition" :items="transitionItems" class="w-full" />
          </UFormField>
          <fieldset class="flex flex-col gap-2">
            <legend class="text-default mb-1 text-sm font-medium">
              {{ t('playlists.form.items') }}
            </legend>
            <p class="text-muted text-xs">{{ t('playlists.form.itemsHelp') }}</p>
            <p v-if="!shareable.length" class="text-warning text-sm">
              {{ t('playlists.form.noDashboards') }}
            </p>
            <div
              v-for="(row, i) in form.items"
              :key="i"
              class="ring-default flex flex-col gap-2 rounded-md p-2 ring-1"
              data-testid="playlist-item"
            >
              <USelect
                v-model="row.dashboard_id"
                :items="dashboardItems"
                class="w-full"
                :aria-label="t('playlists.form.dashboard', { n: i + 1 })"
              />
              <div class="flex flex-wrap items-center gap-2">
                <UInputNumber
                  v-model="row.duration_seconds"
                  :min="10"
                  :max="3600"
                  :step="5"
                  class="w-32"
                  :aria-label="t('playlists.form.duration', { n: i + 1 })"
                />
                <span class="text-muted text-xs">{{ t('playlists.form.seconds') }}</span>
                <span class="ms-auto flex gap-1">
                  <UButton
                    size="xs"
                    color="neutral"
                    variant="ghost"
                    icon="i-lucide-arrow-up"
                    :aria-label="t('playlists.form.up')"
                    :disabled="i === 0"
                    @click="move(i, -1)"
                  />
                  <UButton
                    size="xs"
                    color="neutral"
                    variant="ghost"
                    icon="i-lucide-arrow-down"
                    :aria-label="t('playlists.form.down')"
                    :disabled="i === form.items.length - 1"
                    @click="move(i, 1)"
                  />
                  <UButton
                    size="xs"
                    color="neutral"
                    variant="ghost"
                    icon="i-lucide-x"
                    :aria-label="t('playlists.form.remove')"
                    :disabled="form.items.length === 1"
                    @click="form.items.splice(i, 1)"
                  />
                </span>
              </div>
            </div>
            <UButton
              size="sm"
              color="neutral"
              variant="outline"
              icon="i-lucide-plus"
              class="self-start"
              :label="t('playlists.form.addItem')"
              :disabled="!shareable.length || form.items.length >= 20"
              data-testid="playlist-add-item"
              @click="addRow"
            />
            <p v-if="errors.items" class="text-error text-sm" role="alert">{{ errors.items }}</p>
          </fieldset>
          <UAlert v-if="errors._" color="error" variant="subtle" :title="errors._" />
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="ghost"
            :label="t('common.cancel')"
            @click="open = false"
          />
          <UButton
            type="submit"
            form="playlist-form"
            :loading="saving"
            :label="t('common.save')"
            data-testid="playlist-submit"
          />
        </div>
      </template>
    </USlideover>

    <UModal
      v-model:open="deleteOpen"
      :title="t('playlists.deleteTitle', { name: deleting?.name ?? '' })"
      :description="t('playlists.deleteBody')"
    >
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            :label="t('common.cancel')"
            autofocus
            @click="deleting = null"
          />
          <UButton
            color="error"
            icon="i-lucide-trash-2"
            :label="t('playlists.delete')"
            data-testid="playlist-delete-confirm"
            @click="remove"
          />
        </div>
      </template>
    </UModal>
  </section>
</template>
