<script setup lang="ts">
/**
 * Resolver en bloque los hallazgos activos de un tipo (POST /findings/bulk-resolve), p. ej.
 * para cerrar los falsos avisos de un detector ya mejorado. Comentario obligatorio (queda en
 * cada hallazgo y alimenta la retroalimentación) y recuento previo de lo que se va a cerrar.
 * La API resuelve como máximo 1000 por llamada; si quedan más, se vuelve a pulsar.
 */
const props = defineProps<{
  kind: string
  kindLabel: string
  states: ('open' | 'acknowledged')[]
}>()
const emit = defineEmits<{ done: [resolved: number] }>()
const open = defineModel<boolean>('open', { default: false })

const MAX = 1000

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const comment = ref('')
const touched = ref(false)
const saving = ref(false)
const counting = ref(false)
const count = ref<number | null>(null)
const error = shallowRef<unknown>(null)

/** Cuenta (hasta MAX) los hallazgos que entrarán, en páginas de 200. */
async function countTargets() {
  counting.value = true
  count.value = null
  try {
    let n = 0
    let cursor: string | undefined
    do {
      const page = await unwrap(
        $api.GET('/findings', {
          params: {
            query: { kind: props.kind, state: props.states.join(','), limit: 200, cursor },
          },
        }),
      )
      n += page.data.length
      cursor = page.page.next_cursor ?? undefined
    } while (cursor && n < MAX)
    count.value = Math.min(n, MAX)
  } catch {
    count.value = null
  } finally {
    counting.value = false
  }
}

watch(open, (o) => {
  if (!o) return
  comment.value = t('findings.bulkResolve.suggestion')
  touched.value = false
  error.value = null
  countTargets()
})

const commentError = computed(() =>
  touched.value && comment.value.trim().length < 3
    ? t('findings.bulkResolve.commentRequired')
    : undefined,
)

async function submit() {
  touched.value = true
  if (comment.value.trim().length < 3) return
  saving.value = true
  error.value = null
  try {
    const res = await unwrap(
      $api.POST('/findings/bulk-resolve', {
        body: { kind: props.kind, comment: comment.value.trim(), states: props.states },
      }),
    )
    open.value = false
    toast.add({
      title: t('findings.bulkResolve.done', { n: res.resolved }),
      description: res.resolved >= MAX ? t('findings.bulkResolve.more') : undefined,
      icon: 'i-lucide-check',
      color: 'success',
    })
    emit('done', res.resolved)
  } catch (e) {
    error.value = e
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <UModal
    v-model:open="open"
    :title="t('findings.bulkResolve.title', { kind: kindLabel })"
    :description="t('findings.bulkResolve.description')"
  >
    <template #body>
      <form id="bulk-resolve-form" class="flex flex-col gap-4" @submit.prevent="submit">
        <UAlert
          color="warning"
          variant="subtle"
          icon="i-lucide-list-checks"
          data-testid="bulk-resolve-count"
          :title="
            counting || count === null
              ? t('findings.bulkResolve.counting')
              : count >= MAX
                ? t('findings.bulkResolve.countMax', { n: MAX })
                : t('findings.bulkResolve.count', { n: count, kind: kindLabel })
          "
        />
        <UFormField
          :label="t('findings.bulkResolve.comment')"
          :hint="t('findings.bulkResolve.commentHint')"
          :error="commentError"
          required
        >
          <UTextarea
            v-model="comment"
            :rows="3"
            autoresize
            class="w-full"
            data-testid="bulk-resolve-comment"
            @blur="touched = true"
          />
        </UFormField>
        <ErrorState v-if="error" :error="error" @retry="submit" />
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
          form="bulk-resolve-form"
          :loading="saving"
          :disabled="count === 0"
          :label="t('findings.bulkResolve.submit')"
          data-testid="bulk-resolve-submit"
        />
      </div>
    </template>
  </UModal>
</template>
