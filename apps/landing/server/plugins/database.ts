// Arranque: abre la base de datos (DATA_DIR), aplica migraciones, siembra el catálogo la primera
// vez y crea el primer administrador desde ADMIN_EMAIL + ADMIN_PASSWORD_FILE si no hay ninguno.
//
// También es la línea de órdenes del panel (README.md › Panel de administración):
//   node .output/server/index.mjs admin create --email ana@kns.gt [--name "Ana"] [--password-file f]
//   node .output/server/index.mjs admin reset-totp --email ana@kns.gt
//   node .output/server/index.mjs admin enable|disable --email ana@kns.gt
//   node .output/server/index.mjs admin list
// Sin --password-file, la contraseña se lee de la entrada estándar.
import { readFileSync } from 'node:fs'
import {
  findAdminByEmail,
  listAdmins,
  resetTotp,
  setAdminDisabled,
  createAdminSync,
  AuthError,
} from '../lib/auth/service'
import { bootstrapAdmin, initApp } from '../lib/context'
import { log } from '../utils/log'

function arg(args: string[], name: string): string | undefined {
  const i = args.indexOf(`--${name}`)
  return i >= 0 ? args[i + 1] : undefined
}

function runCli(args: string[]): number {
  const app = initApp()
  const deps = app.auth()
  const actor = { id: null, email: 'cli' }
  const [cmd] = args
  const email = arg(args, 'email') ?? ''
  const out = (s: string) => process.stdout.write(s + '\n')
  try {
    switch (cmd) {
      case 'create': {
        const file = arg(args, 'password-file')
        const password = (file ? readFileSync(file, 'utf8') : readFileSync(0, 'utf8')).replace(
          /\r?\n$/,
          '',
        )
        const id = createAdminSync(deps, { email, password, name: arg(args, 'name') }, actor)
        out(
          `Administrador ${email} creado (id ${id}). En el primer acceso se le pedirá dar de alta el TOTP.`,
        )
        return 0
      }
      case 'reset-totp':
      case 'enable':
      case 'disable': {
        const admin = findAdminByEmail(deps.db, email)
        if (!admin) throw new AuthError('NOT_FOUND')
        if (cmd === 'reset-totp') resetTotp(deps, admin.id, actor)
        else setAdminDisabled(deps, admin.id, cmd === 'disable', { id: null, email: 'cli' })
        out(`Hecho: ${cmd} ${admin.email}`)
        return 0
      }
      case 'list': {
        for (const a of listAdmins(deps.db)) {
          out(
            `${a.id}\t${a.email}\tTOTP:${a.totpEnabled ? 'sí' : 'no'}\t${a.disabled ? 'desactivado' : 'activo'}\túltimo acceso: ${a.lastLoginAt ?? '—'}`,
          )
        }
        return 0
      }
      default:
        out(
          'Uso: admin create|reset-totp|enable|disable|list [--email correo] [--name nombre] [--password-file archivo]',
        )
        return 2
    }
  } catch (err) {
    process.stderr.write(`Error: ${err instanceof AuthError ? err.code : String(err)}\n`)
    return 1
  }
}

export default defineNitroPlugin(() => {
  const argv = process.argv.slice(2)
  if (argv[0] === 'admin') {
    process.exit(runCli(argv.slice(1)))
  }

  const app = initApp()
  const admin = bootstrapAdmin(app.auth(), process.env)
  log(admin.startsWith('error') ? 'error' : 'info', 'landing.database', {
    dataKey: app.key.origin,
    dataKeyError: app.key.error,
    firstAdmin: admin,
  })
  if (process.env.NODE_ENV === 'production' && !app.key.box) {
    log('error', 'landing.data_key_missing', {
      hint: 'Define DATA_KEY_FILE: sin ella no se pueden guardar ni leer las credenciales de PayPal.',
    })
  }
})
