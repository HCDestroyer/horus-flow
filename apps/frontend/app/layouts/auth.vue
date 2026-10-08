<script setup lang="ts">
const { t } = useI18n()
</script>

<template>
  <div class="bg-muted flex min-h-dvh flex-col">
    <main
      id="main-content"
      class="flex flex-1 items-center justify-center px-4 py-10 sm:px-6"
      tabindex="-1"
    >
      <div class="w-full max-w-sm">
        <div class="mb-8 flex items-center justify-center gap-2.5">
          <AppLogo class="size-8" />
          <span class="text-highlighted text-lg font-semibold tracking-tight">
            {{ t('app.name') }}
          </span>
        </div>
        <slot />
      </div>
    </main>
    <footer class="text-muted px-4 pb-6 text-center text-xs">
      {{ t('app.tagline') }}
    </footer>
  </div>
</template>
