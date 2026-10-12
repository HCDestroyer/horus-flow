<script setup lang="ts">
/**
 * Recorrido guiado para poner un nodo en marcha: Nodo → Router → Script de conexión WireGuard
 * → Credenciales → Prefijos. Marca lo hecho, el paso actual y lo que falta; opcionalmente
 * cada paso enlaza a donde se hace.
 */
type SetupStep = 'site' | 'router' | 'script' | 'credentials' | 'prefixes'

const props = withDefaults(
  defineProps<{ current: SetupStep; links?: Partial<Record<SetupStep, string>> }>(),
  { links: () => ({}) },
)
const { t } = useI18n()

const ORDER: SetupStep[] = ['site', 'router', 'script', 'credentials', 'prefixes']
const steps = computed(() => {
  const at = ORDER.indexOf(props.current)
  return ORDER.map((key, i) => ({
    key,
    state: i < at ? 'done' : i === at ? 'current' : 'waiting',
    to: props.links[key],
  }))
})
</script>

<template>
  <ol class="flex flex-col gap-2" :aria-label="t('devices.steps.label')" data-testid="setup-steps">
    <li
      v-for="(s, i) in steps"
      :key="s.key"
      class="flex items-start gap-3"
      :data-step="s.key"
      :data-state="s.state"
      :aria-current="s.state === 'current' ? 'step' : undefined"
    >
      <span
        class="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full text-xs font-semibold"
        :class="{
          'bg-success text-inverted': s.state === 'done',
          'bg-primary text-inverted': s.state === 'current',
          'bg-elevated text-muted ring-default ring-1': s.state === 'waiting',
        }"
        aria-hidden="true"
      >
        <UIcon v-if="s.state === 'done'" name="i-lucide-check" class="size-4" />
        <template v-else>{{ i + 1 }}</template>
      </span>
      <span class="flex min-w-0 flex-col">
        <NuxtLink
          v-if="s.to"
          :to="s.to"
          class="text-highlighted text-sm font-medium hover:underline"
          >{{ t(`devices.steps.${s.key}.title`) }}</NuxtLink
        >
        <span
          v-else
          class="text-sm font-medium"
          :class="s.state === 'waiting' ? 'text-muted' : 'text-highlighted'"
          >{{ t(`devices.steps.${s.key}.title`) }}</span
        >
        <span class="text-muted text-xs">{{ t(`devices.steps.${s.key}.body`) }}</span>
        <span class="sr-only">({{ t(`onboarding.state.${s.state}`) }})</span>
      </span>
    </li>
  </ol>
</template>
