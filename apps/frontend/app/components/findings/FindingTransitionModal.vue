<script setup lang="ts">
import type { Finding } from '~~/types/api'

/**
 * Resolver o marcar como falso positivo (frontend.md §8.2). Falso positivo exige comentario
 * y explica qué hará Horus con él (silenciar el patrón para ese cliente y enseñar a la
 * detección). Ambas acciones son reversibles por el ciclo del hallazgo: sin confirmación extra.
 */
const props = defineProps<{
  finding: Finding
  mode: 'resolve' | 'false_positive'
  appliedActions: string[]
}>()
const emit = defineEmits<{ done: [finding: Finding] }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const comment = ref('')
const silenceDays = ref(30)
const touched = ref(false)
const saving = ref(false)
const error = shallowRef<unknown>(null)

watch(open, (o) => {
  if (!o) return
  comment.value = ''
  touched.value = false
  error.value = null
  silenceDays.value = 30
})

const fp = computed(() => props.mode === 'false_positive')
const commentError = computed(() =>
  fp.value && touched.value && !comment.value.trim() ? t('findings.fp.commentRequired') : undefined,
)

async function submit() {
  touched.value = true
  if (fp.value && !comment.value.trim()) return
  saving.value = true
  error.value = null
  const params = {
    path: { finding_id: props.finding.id },
    header: { 'If-Match': ifMatch(props.finding.version) },
  }
  try {
    const updated = fp.value
      ? await unwrap(
          $api.POST('/findings/{finding_id}/mark-false-positive', {
            params,
            body: { comment: comment.value.trim(), silence_days: silenceDays.value },
          }),
        )
      : await unwrap(
          $api.POST('/findings/{finding_id}/resolve', {
            params,
            body: { comment: comment.value.trim() || null, actions_taken: props.appliedActions },
          }),
        )
    open.value = false
    toast.add({
      title: fp.value ? t('findings.fp.done') : t('findings.resolve.done'),
      icon: 'i-lucide-check',
      color: 'success',
    })
    emit('done', updated)
  } catch (e) {
    error.value = e
  } finally {
    saving.value = false
  }
}

const silenceOptions = computed(() =>
  [7, 30, 90].map((n) => ({ label: t('findings.fp.days', { n }), value: n })),
)
</script>

<template>
  <UModal
    v-model:open="open"
    :title="fp ? t('findings.fp.title') : t('findings.resolve.title')"
    :description="fp ? t('findings.fp.description') : t('findings.resolve.description')"
  >
    <template #body>
      <form id="transition-form" class="flex flex-col gap-4" @submit.prevent="submit">
        <UAlert
          v-if="fp"
          color="info"
          variant="subtle"
          icon="i-lucide-graduation-cap"
          :title="t('findings.fp.whatHappens')"
          data-testid="fp-explanation"
        >
          <template #description>
            <ul class="list-disc space-y-1 ps-4">
              <li>{{ t('findings.fp.effectClose') }}</li>
              <li>{{ t('findings.fp.effectSilence', { days: silenceDays }) }}</li>
              <li>{{ t('findings.fp.effectLearn') }}</li>
            </ul>
          </template>
        </UAlert>
        <p v-if="!fp && appliedActions.length" class="text-muted text-sm">
          {{ t('findings.resolve.applied', { n: appliedActions.length }) }}
        </p>
        <UFormField
          :label="fp ? t('findings.fp.comment') : t('findings.resolve.comment')"
          :error="commentError"
          :required="fp"
        >
          <UTextarea
            v-model="comment"
            :rows="3"
            autoresize
            class="w-full"
            :placeholder="fp ? t('findings.fp.placeholder') : t('findings.resolve.placeholder')"
            data-testid="transition-comment"
            @blur="touched = true"
          />
        </UFormField>
        <UFormField v-if="fp" :label="t('findings.fp.silence')">
          <USelect v-model="silenceDays" :items="silenceOptions" class="w-48" />
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
          form="transition-form"
          :loading="saving"
          :label="fp ? t('findings.fp.submit') : t('findings.resolve.submit')"
          data-testid="transition-submit"
        />
      </div>
    </template>
  </UModal>
</template>
