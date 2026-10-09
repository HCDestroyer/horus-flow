<script setup lang="ts">
import type { RecommendedAction } from '~~/types/api'

/**
 * Acciones recomendadas (D11): Horus solo avisa y recomienda. Cada acción explica por qué,
 * su impacto en el cliente y cómo deshacerla, con los comandos RouterOS listos para COPIAR y
 * su comando para deshacer. No hay ningún botón que actúe sobre el router (ADR-0022).
 * "La apliqué" solo anota qué se hizo a mano (se envía como `actions_taken` al resolver).
 */
const props = defineProps<{ actions: RecommendedAction[]; findingId: string }>()
const applied = defineModel<string[]>('applied', { default: () => [] })
const { t, te } = useI18n()

const sorted = computed(() => [...props.actions].sort((a, b) => a.priority - b.priority))
const RISK: Record<string, 'success' | 'warning' | 'error'> = {
  low: 'success',
  medium: 'warning',
  high: 'error',
}
function toggle(code: string, value: boolean | 'indeterminate') {
  applied.value =
    value === true
      ? [...new Set([...applied.value, code])]
      : applied.value.filter((c) => c !== code)
}
const audience = (a: string) =>
  te(`findings.actions.audience.${a}`) ? t(`findings.actions.audience.${a}`) : a
</script>

<template>
  <section
    class="flex flex-col gap-3"
    :aria-labelledby="`actions-${findingId}`"
    data-testid="recommended-actions"
  >
    <div class="flex flex-col gap-1">
      <h2 :id="`actions-${findingId}`" class="text-highlighted text-base font-semibold">
        {{ t('findings.actions.title') }}
      </h2>
      <p class="text-muted flex gap-1.5 text-sm">
        <UIcon name="i-lucide-hand" class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
        {{ t('findings.actions.manualNote') }}
      </p>
    </div>
    <ol class="flex flex-col gap-3">
      <li v-for="a in sorted" :key="a.code" data-testid="recommended-action" :data-action="a.code">
        <UCard :ui="{ body: 'p-4 sm:p-4 flex flex-col gap-3' }">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <h3 class="text-highlighted flex min-w-0 items-baseline gap-2 font-semibold">
              <span
                class="bg-elevated text-muted inline-flex size-6 shrink-0 items-center justify-center rounded-full text-xs tabular"
                >{{ a.priority }}</span
              >
              <span class="min-w-0">{{ a.title }}</span>
            </h3>
            <span class="flex flex-wrap gap-1.5">
              <UBadge
                :color="RISK[a.risk] ?? 'neutral'"
                variant="subtle"
                :label="t(`findings.actions.risk.${a.risk}`)"
              />
              <UBadge color="neutral" variant="outline" :label="audience(a.audience)" />
            </span>
          </div>
          <p class="text-default text-sm">{{ a.explanation }}</p>

          <div v-if="a.customer_message" class="flex flex-col gap-1.5">
            <CodeBlock
              :code="a.customer_message"
              :label="t('findings.actions.customerMessage')"
              :copy-label="t('findings.actions.copyMessage')"
              prose
            />
          </div>

          <template v-if="a.routeros">
            <CodeBlock
              :code="(a.routeros.rendered_commands ?? a.routeros.commands).join('\n')"
              :label="t('findings.actions.commands', { version: a.routeros.min_version })"
              :copy-label="t('findings.actions.copyCommands')"
            />
            <CodeBlock
              :code="(a.routeros.rendered_undo_commands ?? a.routeros.undo_commands).join('\n')"
              :label="t('findings.actions.undo')"
              :copy-label="t('findings.actions.copyUndo')"
            />
            <p v-if="!a.routeros.rendered_commands" class="text-muted text-xs">
              {{ t('findings.actions.placeholders') }}
            </p>
            <p v-if="a.routeros.notes" class="text-muted flex gap-1.5 text-xs">
              <UIcon name="i-lucide-info" class="mt-px size-3.5 shrink-0" aria-hidden="true" />
              {{ a.routeros.notes }}
            </p>
          </template>

          <UCheckbox
            :model-value="applied.includes(a.code)"
            :label="t('findings.actions.applied')"
            @update:model-value="(v) => toggle(a.code, v)"
          />
        </UCard>
      </li>
    </ol>
  </section>
</template>
