<script setup lang="ts">
// Ajustes: contacto, soporte (24/7, tiempo de respuesta opcional), banner, administradores y
// la propia cuenta (contraseña, códigos de recuperación).
import type { SiteSettings } from '#shared/catalog'

definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: 'Ajustes' })

interface Admin {
  id: number
  email: string
  name: string
  totpEnabled: boolean
  disabled: boolean
  createdAt: string
  lastLoginAt: string | null
}

const toast = useToast()
const settings = ref<SiteSettings | null>(null)
const admins = ref<Admin[]>([])
const me = ref<number | null>(null)
const busy = ref<string | null>(null)
const newAdmin = reactive({ email: '', name: '', password: '' })
const pwd = reactive({ current: '', next: '' })
const codes = ref<string[]>([])

async function load() {
  const [s, a] = await Promise.all([
    adminApi<SiteSettings>('/settings'),
    adminApi<{ admins: Admin[]; me: number }>('/admins'),
  ])
  settings.value = s
  admins.value = a.admins
  me.value = a.me
}
onMounted(load)

async function run(key: string, fn: () => Promise<unknown>, ok: string) {
  busy.value = key
  try {
    await fn()
    toast.add({ title: ok, color: 'primary', icon: 'i-lucide-check' })
    await load()
    return true
  } catch (err) {
    toast.add({ title: adminErrorMessage(err), color: 'error' })
    return false
  } finally {
    busy.value = null
  }
}

const saveSettings = () =>
  run(
    'settings',
    () => adminApi('/settings', { method: 'POST', body: settings.value }),
    'Ajustes guardados',
  )

async function createAdmin() {
  const ok = await run(
    'create',
    () => adminApi('/admins', { method: 'POST', body: newAdmin }),
    'Administrador creado. Dará de alta su TOTP en el primer acceso.',
  )
  if (ok) Object.assign(newAdmin, { email: '', name: '', password: '' })
}
function resetTotp(a: Admin) {
  if (
    !window.confirm(
      `¿Restablecer el TOTP de ${a.email}? Tendrá que darlo de alta otra vez y se cerrarán sus sesiones.`,
    )
  )
    return
  run(
    `totp-${a.id}`,
    () => adminApi(`/admins/${a.id}/reset-totp`, { method: 'POST' }),
    'TOTP restablecido',
  )
}
function toggleDisabled(a: Admin) {
  run(
    `dis-${a.id}`,
    () => adminApi(`/admins/${a.id}/disabled`, { method: 'POST', body: { disabled: !a.disabled } }),
    a.disabled ? 'Administrador activado' : 'Administrador desactivado',
  )
}
function removeAdmin(a: Admin) {
  if (!window.confirm(`¿Eliminar a ${a.email}? No se puede deshacer.`)) return
  run(
    `del-${a.id}`,
    () => adminApi(`/admins/${a.id}`, { method: 'DELETE' }),
    'Administrador eliminado',
  )
}
async function changePassword() {
  const ok = await run(
    'pwd',
    () => adminApi('/me/password', { method: 'POST', body: pwd }),
    'Contraseña cambiada. Se cerraron tus otras sesiones.',
  )
  if (ok) Object.assign(pwd, { current: '', next: '' })
}
async function newCodes() {
  if (!window.confirm('¿Generar códigos nuevos? Los anteriores dejarán de valer.')) return
  busy.value = 'codes'
  try {
    codes.value = (
      await adminApi<{ recoveryCodes: string[] }>('/me/recovery-codes', { method: 'POST' })
    ).recoveryCodes
  } finally {
    busy.value = null
  }
}
</script>

<template>
  <div>
    <AdminPageHeader
      title="Ajustes"
      lead="Contacto y soporte que se muestran en la web, banner de anuncio y administradores del panel."
    />
    <div v-if="settings" class="space-y-6">
      <form
        class="surface space-y-5 rounded-2xl p-5"
        aria-labelledby="s-site"
        @submit.prevent="saveSettings"
      >
        <h2 id="s-site" class="text-lg font-semibold text-highlighted">Contacto y soporte</h2>
        <div class="grid gap-4 md:grid-cols-3">
          <div>
            <label for="s-email" class="mb-1 block text-sm font-medium">Correo de contacto</label>
            <UInput
              id="s-email"
              v-model="settings.contact.email"
              type="email"
              required
              class="w-full"
            />
          </div>
          <div>
            <label for="s-phone" class="mb-1 block text-sm font-medium"
              >Teléfono <span class="font-normal text-muted">(opcional)</span></label
            >
            <UInput id="s-phone" v-model="settings.contact.phone" type="tel" class="w-full" />
          </div>
          <div>
            <label for="s-wa" class="mb-1 block text-sm font-medium"
              >WhatsApp <span class="font-normal text-muted">(opcional)</span></label
            >
            <UInput id="s-wa" v-model="settings.contact.whatsapp" type="tel" class="w-full" />
          </div>
        </div>
        <AdminLocalizedInput
          id="s-support"
          v-model="settings.support.text"
          label="Texto de soporte"
          required
        />
        <div>
          <AdminLocalizedInput
            id="s-rt"
            v-model="settings.support.responseTime"
            label="Tiempo de respuesta comprometido (opcional)"
          />
          <p class="mt-1 text-sm text-muted">
            Déjalo vacío mientras no esté acordado: la web no mostrará ningún tiempo de respuesta.
          </p>
        </div>

        <fieldset class="space-y-3 border-t border-default pt-5">
          <legend class="sr-only">Banner</legend>
          <h3 class="font-semibold text-highlighted">Banner de anuncio</h3>
          <label for="s-banner" class="flex min-h-11 items-center gap-3">
            <input
              id="s-banner"
              v-model="settings.banner.enabled"
              type="checkbox"
              class="size-5 accent-(--ui-primary)"
            />
            Mostrar un banner en lo alto de la web
          </label>
          <AdminLocalizedInput
            id="s-banner-text"
            v-model="settings.banner.text"
            label="Texto del banner"
          />
          <div>
            <label for="s-banner-url" class="mb-1 block text-sm font-medium"
              >Enlace
              <span class="font-normal text-muted">(opcional: https://… o /ruta)</span></label
            >
            <UInput id="s-banner-url" v-model="settings.banner.url" class="w-full max-w-md" />
          </div>
        </fieldset>
        <UButton
          type="submit"
          class="min-h-11"
          label="Guardar ajustes"
          :loading="busy === 'settings'"
        />
      </form>

      <section class="surface rounded-2xl" aria-labelledby="s-admins">
        <h2 id="s-admins" class="px-5 pt-5 text-lg font-semibold text-highlighted">
          Administradores
        </h2>
        <div class="mt-3 overflow-x-auto">
          <table class="w-full text-left text-[0.95rem]">
            <thead class="border-y border-default bg-muted text-sm text-muted">
              <tr>
                <th scope="col" class="px-5 py-2 font-medium">Correo</th>
                <th scope="col" class="px-5 py-2 font-medium">TOTP</th>
                <th scope="col" class="px-5 py-2 font-medium">Estado</th>
                <th scope="col" class="px-5 py-2 font-medium">Último acceso</th>
                <th scope="col" class="px-5 py-2"><span class="sr-only">Acciones</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="a in admins" :key="a.id" class="border-b border-default last:border-0">
                <td class="px-5 py-2">
                  <span class="font-medium text-highlighted">{{ a.email }}</span>
                  <span v-if="a.id === me" class="ml-1 text-sm text-muted">(tú)</span>
                  <span v-if="a.name" class="block text-sm text-muted">{{ a.name }}</span>
                </td>
                <td class="px-5 py-2">{{ a.totpEnabled ? 'Activado' : 'Pendiente de alta' }}</td>
                <td class="px-5 py-2">{{ a.disabled ? 'Desactivado' : 'Activo' }}</td>
                <td class="px-5 py-2 whitespace-nowrap text-muted">
                  {{ formatDate(a.lastLoginAt) }}
                </td>
                <td class="px-5 py-2 text-right whitespace-nowrap">
                  <template v-if="a.id !== me">
                    <UButton
                      color="neutral"
                      variant="ghost"
                      class="min-h-11"
                      label="Restablecer TOTP"
                      :aria-label="`Restablecer TOTP de ${a.email}`"
                      @click="resetTotp(a)"
                    />
                    <UButton
                      color="neutral"
                      variant="ghost"
                      class="min-h-11"
                      :label="a.disabled ? 'Activar' : 'Desactivar'"
                      :aria-label="`${a.disabled ? 'Activar' : 'Desactivar'} ${a.email}`"
                      @click="toggleDisabled(a)"
                    />
                    <UButton
                      color="error"
                      variant="ghost"
                      class="min-h-11"
                      label="Eliminar"
                      :aria-label="`Eliminar ${a.email}`"
                      @click="removeAdmin(a)"
                    />
                  </template>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <form
          class="grid gap-4 border-t border-default p-5 md:grid-cols-[1fr_1fr_1fr_auto] md:items-end"
          aria-label="Nuevo administrador"
          @submit.prevent="createAdmin"
        >
          <div>
            <label for="na-email" class="mb-1 block text-sm font-medium">Correo</label>
            <UInput
              id="na-email"
              v-model="newAdmin.email"
              type="email"
              required
              autocomplete="off"
              class="w-full"
            />
          </div>
          <div>
            <label for="na-name" class="mb-1 block text-sm font-medium"
              >Nombre <span class="font-normal text-muted">(opcional)</span></label
            >
            <UInput id="na-name" v-model="newAdmin.name" autocomplete="off" class="w-full" />
          </div>
          <div>
            <label for="na-pass" class="mb-1 block text-sm font-medium"
              >Contraseña inicial (mín. 12)</label
            >
            <UInput
              id="na-pass"
              v-model="newAdmin.password"
              type="password"
              required
              minlength="12"
              autocomplete="new-password"
              class="w-full"
            />
          </div>
          <UButton
            type="submit"
            class="min-h-11"
            icon="i-lucide-user-plus"
            label="Añadir"
            :loading="busy === 'create'"
          />
        </form>
      </section>

      <section class="surface rounded-2xl p-5" aria-labelledby="s-me">
        <h2 id="s-me" class="text-lg font-semibold text-highlighted">Mi cuenta</h2>
        <form
          class="mt-3 grid gap-4 md:grid-cols-[1fr_1fr_auto] md:items-end"
          aria-label="Cambiar contraseña"
          @submit.prevent="changePassword"
        >
          <div>
            <label for="me-cur" class="mb-1 block text-sm font-medium">Contraseña actual</label>
            <UInput
              id="me-cur"
              v-model="pwd.current"
              type="password"
              required
              autocomplete="current-password"
              class="w-full"
            />
          </div>
          <div>
            <label for="me-new" class="mb-1 block text-sm font-medium"
              >Contraseña nueva (mín. 12)</label
            >
            <UInput
              id="me-new"
              v-model="pwd.next"
              type="password"
              required
              minlength="12"
              autocomplete="new-password"
              class="w-full"
            />
          </div>
          <UButton
            type="submit"
            class="min-h-11"
            label="Cambiar contraseña"
            :loading="busy === 'pwd'"
          />
        </form>
        <div class="mt-5 border-t border-default pt-5">
          <UButton
            color="neutral"
            variant="outline"
            class="min-h-11"
            icon="i-lucide-key-round"
            label="Generar códigos de recuperación nuevos"
            :loading="busy === 'codes'"
            @click="newCodes"
          />
          <ul
            v-if="codes.length"
            class="mt-3 grid max-w-md grid-cols-2 gap-2 rounded-xl bg-muted p-4 font-mono"
          >
            <li v-for="c in codes" :key="c">{{ c }}</li>
          </ul>
        </div>
      </section>
    </div>
  </div>
</template>
