<script setup lang="ts">
import type { DestinationForm } from '~/utils/remote-destinations'
import { PORT_RANGE, RETENTION_RANGE } from '~/utils/remote-destinations'

/**
 * Campos de un destino SFTP (alta y edición): nombre, host, puerto, usuario, ruta remota y días
 * de retención. Los errores llegan ya traducidos (validación local o `422` del servidor).
 */
const form = defineModel<DestinationForm>({ required: true })
defineProps<{ errors: Record<string, string | undefined>; idPrefix: string }>()
const { t } = useI18n()
</script>

<template>
  <div class="flex flex-col gap-4">
    <UFormField
      :label="t('platform.storage.form.name')"
      :help="t('platform.storage.form.nameHelp')"
      :error="errors.name"
      required
    >
      <UInput
        v-model="form.name"
        class="w-full"
        maxlength="120"
        autocomplete="off"
        :data-testid="`${idPrefix}-name`"
      />
    </UFormField>
    <div class="grid gap-4 sm:grid-cols-[1fr_8rem]">
      <UFormField
        :label="t('platform.storage.form.host')"
        :help="t('platform.storage.form.hostHelp')"
        :error="errors.host"
        required
      >
        <UInput
          v-model="form.host"
          class="w-full"
          maxlength="253"
          autocomplete="off"
          spellcheck="false"
          placeholder="nas.ejemplo.net"
          :ui="{ base: 'font-mono' }"
          :data-testid="`${idPrefix}-host`"
        />
      </UFormField>
      <UFormField :label="t('platform.storage.form.port')" :error="errors.port" required>
        <UInputNumber
          v-model="form.port"
          :min="PORT_RANGE.min"
          :max="PORT_RANGE.max"
          :format-options="{ useGrouping: false }"
          class="w-full"
          :data-testid="`${idPrefix}-port`"
        />
      </UFormField>
    </div>
    <UFormField
      :label="t('platform.storage.form.username')"
      :help="t('platform.storage.form.usernameHelp')"
      :error="errors.username"
      required
    >
      <UInput
        v-model="form.username"
        class="w-full"
        maxlength="128"
        autocomplete="off"
        spellcheck="false"
        :ui="{ base: 'font-mono' }"
        :data-testid="`${idPrefix}-username`"
      />
    </UFormField>
    <UFormField
      :label="t('platform.storage.form.path')"
      :help="t('platform.storage.form.pathHelp')"
      :error="errors.path"
      required
    >
      <UInput
        v-model="form.path"
        class="w-full"
        maxlength="1024"
        autocomplete="off"
        spellcheck="false"
        placeholder="/srv/copias/horus"
        :ui="{ base: 'font-mono' }"
        :data-testid="`${idPrefix}-path`"
      />
    </UFormField>
    <UFormField
      :label="t('platform.storage.form.retention')"
      :help="t('platform.storage.form.retentionHelp')"
      :error="errors.retention_days"
      required
    >
      <UInputNumber
        v-model="form.retention_days"
        :min="RETENTION_RANGE.min"
        :max="RETENTION_RANGE.max"
        class="w-full sm:w-48"
        :data-testid="`${idPrefix}-retention`"
      />
    </UFormField>
  </div>
</template>
