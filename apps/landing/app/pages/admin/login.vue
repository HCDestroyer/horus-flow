<script setup lang="ts">
// Acceso al panel: contraseña → segundo factor (TOTP o código de recuperación), o alta del TOTP
// la primera vez (QR + código) con los códigos de recuperación.
definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: 'Iniciar sesión' })

type Step = 'password' | 'mfa' | 'enroll' | 'codes'
const session = useAdminSession()
const step = ref<Step>('password')
const busy = ref(false)
const error = ref<string | null>(null)
const email = ref('')
const password = ref('')
const code = ref('')
const useRecovery = ref(false)
const recovery = ref('')
const enroll = ref<{ secret: string; qr: string } | null>(null)
const codes = ref<string[]>([])
const heading = ref<HTMLElement | null>(null)

const titles: Record<Step, string> = {
  password: 'Panel de administración',
  mfa: 'Verificación en dos pasos',
  enroll: 'Activa la verificación en dos pasos',
  codes: 'Guarda tus códigos de recuperación',
}

async function go(next: Step) {
  step.value = next
  error.value = null
  code.value = ''
  await nextTick()
  heading.value?.focus()
}

onMounted(async () => {
  const s = session.value ?? (await refreshAdminSession())
  if (s.authenticated && s.stage === 'mfa') await go('mfa')
  else if (s.authenticated && s.stage === 'enroll') await startEnroll()
})

async function submitPassword() {
  busy.value = true
  error.value = null
  try {
    const { csrf } = await $fetch<{ csrf: string }>('/api/admin/auth/csrf', {
      credentials: 'same-origin',
    })
    session.value = { authenticated: false, csrf }
    const r = await adminApi<{ stage: 'mfa' | 'enroll'; csrf: string }>('/auth/login', {
      method: 'POST',
      body: { email: email.value, password: password.value },
    })
    session.value = { authenticated: true, stage: r.stage, csrf: r.csrf, email: email.value }
    password.value = ''
    if (r.stage === 'mfa') await go('mfa')
    else await startEnroll()
  } catch (err) {
    error.value = adminErrorMessage(err)
  } finally {
    busy.value = false
  }
}

async function startEnroll() {
  enroll.value = await adminApi<{ secret: string; qr: string }>('/auth/enroll')
  await go('enroll')
}

async function finish(r: { csrf: string }) {
  await refreshAdminSession()
  session.value = { ...session.value!, csrf: r.csrf }
}

async function submitMfa() {
  busy.value = true
  error.value = null
  try {
    const r = await adminApi<{ csrf: string }>('/auth/mfa', {
      method: 'POST',
      body: useRecovery.value ? { recoveryCode: recovery.value } : { code: code.value },
    })
    await finish(r)
    await navigateTo('/admin/solicitudes')
  } catch (err) {
    error.value = adminErrorMessage(err)
  } finally {
    busy.value = false
  }
}

async function submitEnroll() {
  busy.value = true
  error.value = null
  try {
    const r = await adminApi<{ csrf: string; recoveryCodes: string[] }>('/auth/enroll', {
      method: 'POST',
      body: { code: code.value },
    })
    await finish(r)
    codes.value = r.recoveryCodes
    await go('codes')
  } catch (err) {
    error.value = adminErrorMessage(err)
  } finally {
    busy.value = false
  }
}

const copiedCodes = ref(false)
async function copyCodes() {
  try {
    await navigator.clipboard.writeText(codes.value.join('\n'))
    copiedCodes.value = true
  } catch {
    copiedCodes.value = false
  }
}
</script>

<template>
  <div class="mx-auto flex min-h-dvh max-w-md flex-col justify-center px-4 py-10">
    <div class="mb-6 flex items-center gap-2 font-semibold text-highlighted">
      <HorusMark :size="28" class="text-primary" />
      Horus Flow
    </div>
    <section class="surface rounded-2xl p-6 sm:p-8" aria-labelledby="login-title">
      <h1
        id="login-title"
        ref="heading"
        tabindex="-1"
        class="text-2xl font-bold text-highlighted outline-none"
      >
        {{ titles[step] }}
      </h1>

      <UAlert
        v-if="error"
        class="mt-4"
        color="error"
        variant="subtle"
        :title="error"
        role="alert"
        icon="i-lucide-circle-alert"
      />

      <!-- 1. Contraseña -->
      <form v-if="step === 'password'" class="mt-6 space-y-5" @submit.prevent="submitPassword">
        <div>
          <label for="admin-email" class="mb-1 block text-sm font-medium">Correo</label>
          <UInput
            id="admin-email"
            v-model="email"
            type="email"
            autocomplete="username"
            required
            size="xl"
            class="w-full"
          />
        </div>
        <div>
          <label for="admin-password" class="mb-1 block text-sm font-medium">Contraseña</label>
          <UInput
            id="admin-password"
            v-model="password"
            type="password"
            autocomplete="current-password"
            required
            size="xl"
            class="w-full"
          />
        </div>
        <UButton
          type="submit"
          block
          size="xl"
          class="min-h-12 rounded-xl"
          :loading="busy"
          label="Continuar"
        />
      </form>

      <!-- 2. Segundo factor -->
      <form v-else-if="step === 'mfa'" class="mt-4 space-y-5" @submit.prevent="submitMfa">
        <template v-if="!useRecovery">
          <p class="text-toned">Escribe el código de 6 dígitos de tu app de autenticación.</p>
          <div>
            <label for="admin-code" class="mb-1 block text-sm font-medium">Código</label>
            <UInput
              id="admin-code"
              v-model="code"
              inputmode="numeric"
              autocomplete="one-time-code"
              pattern="[0-9 ]*"
              maxlength="7"
              required
              size="xl"
              class="w-full font-mono tracking-widest"
            />
          </div>
        </template>
        <template v-else>
          <p class="text-toned">
            Escribe uno de tus códigos de recuperación. Cada código sirve una sola vez.
          </p>
          <div>
            <label for="admin-recovery" class="mb-1 block text-sm font-medium"
              >Código de recuperación</label
            >
            <UInput
              id="admin-recovery"
              v-model="recovery"
              autocomplete="off"
              required
              size="xl"
              class="w-full font-mono"
            />
          </div>
        </template>
        <UButton
          type="submit"
          block
          size="xl"
          class="min-h-12 rounded-xl"
          :loading="busy"
          label="Verificar"
        />
        <UButton
          type="button"
          variant="link"
          color="neutral"
          class="min-h-11 px-0"
          :label="useRecovery ? 'Usar el código de la app' : 'Usar un código de recuperación'"
          @click="useRecovery = !useRecovery"
        />
      </form>

      <!-- 3. Alta del TOTP -->
      <form
        v-else-if="step === 'enroll' && enroll"
        class="mt-4 space-y-5"
        @submit.prevent="submitEnroll"
      >
        <p class="text-toned">
          Es obligatoria para entrar al panel. Escanea el código con una app de autenticación
          (Google Authenticator, Microsoft Authenticator, 1Password…) y escribe el código que
          muestra.
        </p>
        <img
          :src="enroll.qr"
          width="220"
          height="220"
          alt="Código QR para añadir Horus Flow a tu app de autenticación"
          class="mx-auto rounded-xl bg-white p-2"
          data-testid="totp-qr"
        />
        <details class="rounded-xl border border-default p-3">
          <summary class="min-h-11 cursor-pointer content-center font-medium">
            ¿No puedes escanear? Escribe la clave
          </summary>
          <p class="mt-2 font-mono text-sm break-all text-highlighted" data-testid="totp-secret">
            {{ enroll.secret }}
          </p>
        </details>
        <div>
          <label for="admin-enroll-code" class="mb-1 block text-sm font-medium"
            >Código de 6 dígitos</label
          >
          <UInput
            id="admin-enroll-code"
            v-model="code"
            inputmode="numeric"
            autocomplete="one-time-code"
            maxlength="7"
            required
            size="xl"
            class="w-full font-mono tracking-widest"
          />
        </div>
        <UButton
          type="submit"
          block
          size="xl"
          class="min-h-12 rounded-xl"
          :loading="busy"
          label="Activar y entrar"
        />
      </form>

      <!-- 4. Códigos de recuperación -->
      <div v-else-if="step === 'codes'" class="mt-4 space-y-5">
        <p class="text-toned">
          Si pierdes el teléfono, cada uno de estos códigos te deja entrar una vez. Guárdalos en un
          lugar seguro: no se volverán a mostrar.
        </p>
        <ul
          class="grid grid-cols-2 gap-2 rounded-xl bg-muted p-4 font-mono text-highlighted"
          data-testid="recovery-codes"
        >
          <li v-for="c in codes" :key="c">{{ c }}</li>
        </ul>
        <div class="flex flex-wrap gap-3">
          <UButton
            color="neutral"
            variant="outline"
            size="lg"
            class="min-h-11"
            :icon="copiedCodes ? 'i-lucide-check' : 'i-lucide-copy'"
            :label="copiedCodes ? 'Copiados' : 'Copiar códigos'"
            @click="copyCodes"
          />
          <UButton
            size="lg"
            class="min-h-11"
            label="He guardado los códigos"
            to="/admin/solicitudes"
          />
        </div>
      </div>
    </section>
  </div>
</template>
