#!/usr/bin/env node
// make contracts-check (I0-05): verifica los contratos v0 C1–C10.
//   node tools/contracts-check.mjs            # verificar (CI)
//   node tools/contracts-check.mjs --write    # regenerar artefactos derivados (raíz OpenAPI, bundle, gateway-routes)
//   node tools/contracts-check.mjs --with-db  # además valida el DDL v0 (pglast + chdb, tools/ddl-check.py)
// Requiere Node ≥ 20, dependencias de package.json y `buf` en PATH (o BUF_BIN). Con --with-db: PYTHON con pglast y chdb.
import { spawnSync } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import Ajv2020 from 'ajv/dist/2020.js'
import addFormats from 'ajv-formats'
import YAML from 'yaml'

const args = new Set(process.argv.slice(2))
const WRITE = args.has('--write')
const WITH_DB = args.has('--with-db')
const HERE = dirname(fileURLToPath(import.meta.url))
const SCHEMAS = resolve(HERE, '..')
const PACKAGES = resolve(SCHEMAS, '..')
const EVENTS = join(PACKAGES, 'events')
const PROTO = join(PACKAGES, 'protobuf')
const OAS_DIR = join(SCHEMAS, 'openapi', 'v0')
const MODULE_FILES = ['auth', 'gateway', 'platform', 'devices', 'wireguard', 'flows', 'analytics', 'detection', 'alerts']
const SPECIAL_PERMISSIONS = new Set([
  'public', 'authenticated', 'refresh_cookie', 'kiosk_cookie', 'enrollment_token', 'widget_type', 'dashboard_access', 'kiosk_self',
])
const HTTP_METHODS = ['get', 'post', 'put', 'patch', 'delete']

const errors = []
const warnings = []
const fail = (msg) => errors.push(msg)
const warn = (msg) => warnings.push(msg)
const step = (msg) => console.log(`\n== ${msg}`)
const rel = (p) => relative(PACKAGES, p)
const readYaml = (p) => YAML.parse(readFileSync(p, 'utf8'))
const readJson = (p) => JSON.parse(readFileSync(p, 'utf8'))

function walk(dir, pred, out = []) {
  if (!existsSync(dir)) return out
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name === 'gen' || name.startsWith('.')) continue
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, pred, out)
    else if (pred(p)) out.push(p)
  }
  return out
}

function run(cmd, cmdArgs, opts = {}) {
  const r = spawnSync(cmd, cmdArgs, { encoding: 'utf8', ...opts })
  return { code: r.status ?? (r.error ? 127 : 1), out: `${r.stdout ?? ''}${r.stderr ?? ''}`, error: r.error }
}

function writeOrCompare(path, content) {
  const current = existsSync(path) ? readFileSync(path, 'utf8') : null
  if (WRITE) {
    if (current !== content) { mkdirSync(dirname(path), { recursive: true }); writeFileSync(path, content) }
    return
  }
  if (current !== content) fail(`${rel(path)} está desactualizado: ejecuta \`npm run generate\` en packages/schemas`)
}

// ---------------------------------------------------------------------------------------------
step('1. YAML y JSON bien formados')
const dataFiles = [SCHEMAS, EVENTS, PROTO].flatMap((d) => walk(d, (p) => /\.(ya?ml|json)$/.test(p) && !p.endsWith('package-lock.json')))
for (const f of dataFiles) {
  try {
    if (f.endsWith('.json')) readJson(f)
    else YAML.parseAllDocuments(readFileSync(f, 'utf8')).forEach((d) => { if (d.errors.length) throw d.errors[0] })
  } catch (e) {
    fail(`${rel(f)}: no parsea (${e.message})`)
  }
}
console.log(`${dataFiles.length} archivos`)

// ---------------------------------------------------------------------------------------------
step('2. OpenAPI: raíz generada desde las specs de módulo (C5)')
const escapePtr = (s) => s.replaceAll('~', '~0').replaceAll('/', '~1')
const modules = Object.fromEntries(MODULE_FILES.map((m) => [m, readYaml(join(OAS_DIR, `${m}.yaml`))]))
const rootPaths = {}
const tags = []
for (const [m, doc] of Object.entries(modules)) {
  for (const t of doc.tags ?? []) if (!tags.some((x) => x.name === t.name)) tags.push(t)
  for (const p of Object.keys(doc.paths ?? {})) {
    if (rootPaths[p]) fail(`ruta duplicada ${p} en ${m}.yaml y en ${rootPaths[p].$ref}`)
    rootPaths[p] = { $ref: `./${m}.yaml#/paths/${escapePtr(p)}` }
  }
}
const sortedPaths = Object.fromEntries(Object.keys(rootPaths).sort().map((k) => [k, rootPaths[k]]))
const common = readYaml(join(OAS_DIR, 'common.yaml'))
const root = {
  openapi: '3.1.0',
  info: {
    title: 'Horus Flow API (contrato v0)',
    version: '0.1.0',
    description: 'GENERADO por packages/schemas/tools/contracts-check.mjs --write a partir de las specs de módulo. No editar.\n'
      + 'API pública /api/v1 (docs/api.md). Tenant solo por el claim `tid` del token. Errores RFC 9457, paginación por cursor.',
    license: { name: 'Propietaria', identifier: 'LicenseRef-Horus-Proprietary' },
  },
  servers: common.servers,
  tags,
  paths: sortedPaths,
  components: {
    securitySchemes: Object.fromEntries(Object.keys(common.components.securitySchemes).map((k) => [k, { $ref: `./common.yaml#/components/securitySchemes/${k}` }])),
  },
}
const rootPath = join(OAS_DIR, 'horus-api.yaml')
writeOrCompare(rootPath, `# GENERADO: no editar (npm run generate en packages/schemas).\n${YAML.stringify(root, { lineWidth: 0 })}`)
console.log(`${Object.keys(sortedPaths).length} rutas en ${MODULE_FILES.length} módulos`)

// ---------------------------------------------------------------------------------------------
step('3. OpenAPI: lint (Redocly, reglas propias en redocly.yaml) y bundle')
const redocly = join(SCHEMAS, 'node_modules', '.bin', 'redocly')
if (!existsSync(redocly)) fail('falta @redocly/cli: ejecuta `npm ci` en packages/schemas')
else {
  for (const f of [rootPath, ...MODULE_FILES.map((m) => join(OAS_DIR, `${m}.yaml`))]) {
    const r = run(redocly, ['lint', relative(SCHEMAS, f), '--config', 'redocly.yaml', '--format=summary'], { cwd: SCHEMAS, env: { ...process.env, REDOCLY_TELEMETRY: 'off', REDOCLY_SUPPRESS_UPDATE_NOTICE: 'true' } })
    if (r.code !== 0) fail(`redocly lint ${rel(f)}:\n${r.out.split('\n').filter((l) => /error|warning|❌/.test(l)).join('\n')}`)
  }
  const bundleTmp = join(mkdtempSync(join(tmpdir(), 'hf-oas-')), 'bundle.yaml')
  const r = run(redocly, ['bundle', relative(SCHEMAS, rootPath), '--config', 'redocly.yaml', '-o', bundleTmp], { cwd: SCHEMAS, env: { ...process.env, REDOCLY_TELEMETRY: 'off', REDOCLY_SUPPRESS_UPDATE_NOTICE: 'true' } })
  if (r.code !== 0) fail(`redocly bundle:\n${r.out}`)
  else writeOrCompare(join(SCHEMAS, 'openapi', 'dist', 'horus-api.v0.yaml'), `# GENERADO (redocly bundle): no editar.\n${readFileSync(bundleTmp, 'utf8')}`)
}

// ---------------------------------------------------------------------------------------------
step('4. OpenAPI: ámbito de tenant, permisos y tabla del gateway (C5 criterio 1, C7)')
const perms = readYaml(join(SCHEMAS, 'permissions', 'v0', 'permissions.yaml'))
const permKeys = new Set([...perms.permissions.tenant, ...perms.permissions.platform].map((p) => p.key))
const ops = []
for (const [m, doc] of Object.entries(modules)) {
  for (const [p, item] of Object.entries(doc.paths ?? {})) {
    for (const method of HTTP_METHODS) {
      const op = item[method]
      if (!op) continue
      ops.push({ module: m, path: p, method: method.toUpperCase(), op })
    }
  }
}
for (const { module, path, method, op } of ops) {
  const id = `${method} ${path} (${module}.yaml)`
  const schemes = (op.security ?? []).flatMap((s) => Object.keys(s))
  const scope = op['x-scope']
  const perm = op['x-permission']
  const principals = op['x-principals'] ?? ['user', 'api_token']
  if (op['x-module'] && module !== 'platform' && op['x-module'] !== module && !(module === 'gateway')) {
    warn(`${id}: x-module=${op['x-module']} en la spec de ${module}`)
  }
  if (perm && !SPECIAL_PERMISSIONS.has(perm) && !permKeys.has(perm)) fail(`${id}: x-permission '${perm}' no existe en permissions.yaml`)
  for (const extra of op['x-permission-all'] ?? []) if (!permKeys.has(extra)) fail(`${id}: x-permission-all '${extra}' desconocido`)
  if (scope === 'tenant') {
    const bad = schemes.filter((s) => !['tenantBearer', 'kioskBearer'].includes(s))
    if (!schemes.includes('tenantBearer') && !schemes.includes('kioskBearer')) fail(`${id}: x-scope tenant sin tenantBearer/kioskBearer (el ISP debe ir en el token)`)
    if (bad.length) fail(`${id}: x-scope tenant admite esquemas sin tid: ${bad.join(', ')}`)
    if (schemes.includes('kioskBearer') !== principals.includes('kiosk')) fail(`${id}: kioskBearer y x-principals [kiosk] deben ir juntos`)
    if (/tenant_id/.test(path)) fail(`${id}: una ruta de negocio no lleva el tenant en la ruta (api.md §0.3)`)
  } else if (scope === 'platform') {
    if (schemes.join() !== 'platformBearer') fail(`${id}: x-scope platform debe exigir solo platformBearer`)
    if (!path.startsWith('/platform/')) fail(`${id}: rutas de plataforma bajo /platform/*`)
  } else if (scope === 'public') {
    if (perm === 'public' && schemes.length) fail(`${id}: ruta pública con security`)
  } else if (scope === 'session') {
    if (!schemes.length) fail(`${id}: x-scope session sin security`)
  }
  if (path.startsWith('/platform/') && scope !== 'platform') fail(`${id}: /platform/* con x-scope ${scope}`)
  // Las IPs de clientes nunca en la URL.
  for (const prm of [...(op.parameters ?? []), ...(modules[module].paths[path].parameters ?? [])]) {
    if (prm.in === 'query' && /^(address|ip|client_ip|prefix)$/.test(prm.name ?? '')) fail(`${id}: IP de cliente como parámetro de URL (${prm.name})`)
  }
  // Idempotency-Key obligatorio declarado si la descripción lo exige.
  const needsIdem = /Idempotency-Key.{0,3}obligatorio/i.test(`${op.summary} ${op.description ?? ''}`)
  const hasIdem = (op.parameters ?? []).some((p) => p.$ref?.endsWith('IdempotencyKeyRequired'))
  if (needsIdem && !hasIdem) fail(`${id}: menciona Idempotency-Key obligatorio pero no lo declara`)
}

// Tabla del gateway derivada de las operaciones (bloque por módulo, orden alfabético).
const routesByModule = {}
for (const { path, method, op } of ops) {
  const m = op['x-module']
  routesByModule[m] ??= {}
  const prefix = `/api/v1${path}`
  const r = (routesByModule[m][prefix] ??= { prefix, scope: op['x-scope'], methods: {} })
  if (r.scope !== op['x-scope']) fail(`gateway-routes: ${prefix} mezcla ámbitos ${r.scope} y ${op['x-scope']}`)
  r.methods[method] = op['x-permission']
  const principals = op['x-principals']
  if (principals) r.principals = [...new Set([...(r.principals ?? []), ...principals])]
  if (op['x-rate-limit']) r.rate_limit = op['x-rate-limit']
  if (op['x-reauth']) (r.reauth ??= []).push(method)
  if ((op.parameters ?? []).some((p) => p.$ref?.endsWith('IdempotencyKeyRequired'))) (r.idempotency_key_required ??= []).push(method)
  if (op['x-audited']) (r.audited ??= []).push(method)
}
routesByModule.gateway['/api/v1/ws'] = {
  prefix: '/api/v1/ws', scope: 'session', methods: { GET: 'authenticated' }, principals: ['user', 'api_token', 'kiosk'],
  note: 'Upgrade WebSocket con ticket de un uso (?ticket=); subprotocolo horus.ws.v1; ticket redactado en logs.',
}
const routesDoc = {
  version: 'v0',
  generated_from: 'packages/schemas/openapi/v0/*.yaml (x-module, x-scope, x-permission, x-principals, x-reauth, x-rate-limit, x-audited)',
  defaults: {
    principals: ['user', 'api_token'],
    timeouts: { crud: '5s', action: '15s', analytics: '30s' },
    deny_by_default: true,
    match: 'prefijo más específico',
    special_permissions: {
      public: 'sin autenticación (rate limit propio)',
      authenticated: 'cualquier token válido del ámbito indicado',
      refresh_cookie: 'cookie __Secure-hf_rt + X-Requested-With: horus + Origin permitido',
      kiosk_cookie: 'cookie __Secure-hf_kiosk + X-Requested-With: horus + allowed_cidrs',
      enrollment_token: 'token de enrolamiento de un uso en el cuerpo (lo valida wireguard)',
      widget_type: 'required_permission del tipo de widget (lo evalúa analytics) + acceso al dashboard',
      dashboard_access: 'dueño del dashboard o dashboards.manage si es del ISP (lo evalúa analytics)',
      kiosk_self: 'JWT de kiosco; devuelve solo su propia configuración',
    },
  },
  modules: Object.fromEntries(Object.keys(routesByModule).sort().map((m) => [
    m,
    Object.keys(routesByModule[m]).sort().map((k) => routesByModule[m][k]),
  ])),
}
writeOrCompare(join(SCHEMAS, 'openapi', 'v0', 'gateway-routes.yaml'),
  `# Tabla de rutas del gateway (C5; docs/api.md §3.1). GENERADA desde las extensiones x-* de las specs de módulo\n`
  + `# con \`npm run generate\` en packages/schemas; CI comprueba que está al día. Ruta sensible (persona).\n${YAML.stringify(routesDoc, { lineWidth: 0 })}`)
console.log(`${ops.length} operaciones; ${ops.filter((o) => o.op['x-scope'] === 'tenant').length} con ámbito de tenant`)

// ---------------------------------------------------------------------------------------------
step('5. JSON Schemas y ejemplos (C6, C8, C9, C10)')
const ajv = new Ajv2020({ allErrors: true, strict: false })
addFormats(ajv)
const schemaFiles = [SCHEMAS, EVENTS].flatMap((d) => walk(d, (p) => p.endsWith('.schema.json')))
const compiled = {}
for (const f of schemaFiles) {
  try {
    const s = readJson(f)
    ajv.addSchema(s, s.$id)
    compiled[rel(f)] = s.$id
  } catch (e) {
    fail(`${rel(f)}: ${e.message}`)
  }
}
const validator = (id, def) => {
  const v = def ? ajv.getSchema(`${id}#/$defs/${def}`) : ajv.getSchema(id)
  if (!v) throw new Error(`esquema ${id}${def ? `#/$defs/${def}` : ''} no encontrado`)
  return v
}
const validate = (label, id, data, def) => {
  const v = validator(id, def)
  if (!v(data)) fail(`${label}: ${ajv.errorsText(v.errors, { separator: '; ' })}`)
}
const ID = (p) => `https://schemas.horus-flow.local/${p}`

// C8 hallazgo
for (const f of walk(join(SCHEMAS, 'finding', 'v0', 'examples'), (p) => p.endsWith('.json'))) validate(rel(f), ID('finding/v0/finding.schema.json'), readJson(f))

// C9 widgets, plantillas y documento de dashboard
const catalog = readJson(join(SCHEMAS, 'dashboard', 'v0', 'widget-types.json'))
const widgetTypes = {}
for (const wt of catalog.data) {
  validate(`widget-types.json[${wt.type}]`, ID('dashboard/v0/widget-type.schema.json'), wt)
  if (widgetTypes[wt.type]) fail(`widget-types.json: tipo duplicado ${wt.type}`)
  try { widgetTypes[wt.type] = { ...wt, validateConfig: ajv.compile(wt.config_schema) } } catch (e) { fail(`config_schema de ${wt.type}: ${e.message}`) }
  if (!SPECIAL_PERMISSIONS.has(wt.required_permission) && !permKeys.has(wt.required_permission)) fail(`widget ${wt.type}: required_permission desconocido ${wt.required_permission}`)
}
for (const f of walk(join(SCHEMAS, 'dashboard', 'v0', 'templates'), (p) => p.endsWith('.json'))) {
  const doc = readJson(f)
  validate(rel(f), ID('dashboard/v0/dashboard.schema.json'), doc)
  const cells = new Map()
  for (const w of doc.widgets) {
    const t = widgetTypes[w.type]
    if (!t) { fail(`${rel(f)}: widget ${w.id} de tipo desconocido ${w.type}`); continue }
    if (!t.validateConfig(w.config)) fail(`${rel(f)}: config de ${w.id} (${w.type}) no valida: ${ajv.errorsText(t.validateConfig.errors)}`)
    const { x, y, w: ww, h } = w.position
    if (!t.sizes.allowed.some((s) => s.w === ww && s.h === h)) fail(`${rel(f)}: ${w.id} tamaño ${ww}×${h} no permitido para ${w.type}`)
    if (x + ww > 12) fail(`${rel(f)}: ${w.id} se sale de la grilla de 12 columnas`)
    for (let i = x; i < x + ww; i++) for (let j = y; j < y + h; j++) {
      const k = `${i},${j}`
      if (cells.has(k)) fail(`${rel(f)}: ${w.id} se solapa con ${cells.get(k)} en (${k})`)
      cells.set(k, w.id)
    }
    if (doc.template_key && !t.kiosk_allowed) fail(`${rel(f)}: plantilla de kiosco con ${w.type} no kiosk_allowed`)
  }
  const rows = Math.max(...doc.widgets.map((w) => w.position.y + w.position.h))
  if (rows * doc.layout.row_height_px > 1080) warn(`${rel(f)}: ${rows} filas × ${doc.layout.row_height_px}px > 1080 px (mural sin scroll)`)
}

// C6 WebSocket
for (const f of walk(join(SCHEMAS, 'websocket', 'v0', 'examples'), (p) => p.endsWith('.json'))) {
  const msgs = readJson(f)
  for (const [i, m] of (Array.isArray(msgs) ? msgs : [msgs]).entries()) validate(`${rel(f)}[${i}]`, ID('websocket/v0/messages.schema.json'), m)
}
const topics = readYaml(join(SCHEMAS, 'websocket', 'v0', 'topics.yaml'))
for (const t of topics.topics) {
  if (!['event', 'state'].includes(t.class)) fail(`topics.yaml ${t.topic}: clase inválida`)
  const p = t.permission
  if (!permKeys.has(p) && !['authenticated', 'authenticated_or_kiosk', 'dashboard_access'].includes(p)) fail(`topics.yaml ${t.topic}: permiso desconocido ${p}`)
}
for (const wt of Object.values(widgetTypes)) {
  if (wt.realtime_topic && !topics.topics.some((t) => t.topic === wt.realtime_topic)) fail(`widget ${wt.type}: realtime_topic ${wt.realtime_topic} no existe en topics.yaml`)
}

// C10 simulador
for (const f of walk(join(SCHEMAS, 'flowsim', 'v0', 'examples'), () => true)) {
  if (f.endsWith('.scenario.yaml')) validate(rel(f), ID('flowsim/v0/scenario.schema.json'), readYaml(f))
  if (f.endsWith('.expected.json')) validate(rel(f), ID('flowsim/v0/expected.schema.json'), readJson(f))
}

// C7 roles
for (const fam of ['tenant', 'platform']) for (const r of perms.roles[fam]) for (const p of r.permissions) {
  if (p !== '*' && !permKeys.has(p)) fail(`permissions.yaml rol ${r.key}: permiso desconocido ${p}`)
}
console.log(`${schemaFiles.length} esquemas, ${catalog.data.length} tipos de widget`)

// ---------------------------------------------------------------------------------------------
step('6. Eventos: catálogo, golden files, tenant y Protobuf (C4, C8)')
const BUF = process.env.BUF_BIN || 'buf'
const bufOk = run(BUF, ['--version']).code === 0
if (!bufOk) fail('falta `buf` (instalar: go install github.com/bufbuild/buf/cmd/buf@v1.47.2, o BUF_BIN=...)')
else {
  const lint = run(BUF, ['lint'], { cwd: PROTO })
  if (lint.code !== 0) fail(`buf lint:\n${lint.out}`)
  const build = run(BUF, ['build'], { cwd: PROTO })
  if (build.code !== 0) fail(`buf build:\n${build.out}`)
}
// Validación estricta del JSON de cada payload contra su mensaje Protobuf (nombres proto snake_case, sin campos
// desconocidos, tipos escalares; null admitido como "sin valor" igual que protojson). `buf convert` descarta
// campos desconocidos, por eso se valida contra los descriptores de `buf build`.
let messages = null
if (bufOk) {
  const img = run(BUF, ['build', '-o', '-#format=json'], { cwd: PROTO, maxBuffer: 64 * 1024 * 1024 })
  if (img.code !== 0) fail(`buf build -o json:\n${img.out}`)
  else {
    messages = new Map()
    const addMsgs = (prefix, list) => {
      for (const m of list ?? []) {
        const name = `${prefix}.${m.name}`
        messages.set(name, m)
        addMsgs(name, m.nestedType)
      }
    }
    for (const file of JSON.parse(img.out).file) addMsgs(file.package, file.messageType)
  }
}
const SCALAR = {
  TYPE_STRING: (v) => typeof v === 'string',
  TYPE_BOOL: (v) => typeof v === 'boolean',
  TYPE_DOUBLE: (v) => typeof v === 'number',
  TYPE_FLOAT: (v) => typeof v === 'number',
  TYPE_INT32: (v) => Number.isInteger(v),
  TYPE_UINT32: (v) => Number.isInteger(v) && v >= 0,
  TYPE_INT64: (v) => Number.isInteger(v) || (typeof v === 'string' && /^-?\d+$/.test(v)),
  TYPE_UINT64: (v) => (Number.isInteger(v) && v >= 0) || (typeof v === 'string' && /^\d+$/.test(v)),
  TYPE_BYTES: (v) => typeof v === 'string',
}
function checkMessage(typeName, value, path) {
  const errs = []
  const t = typeName.replace(/^\./, '')
  if (value === null) return errs
  if (t === 'google.protobuf.Timestamp') {
    if (typeof value !== 'string' || Number.isNaN(Date.parse(value))) errs.push(`${path}: Timestamp RFC 3339 esperado`)
    return errs
  }
  if (t === 'google.protobuf.Struct') {
    if (typeof value !== 'object' || Array.isArray(value)) errs.push(`${path}: objeto esperado (Struct)`)
    return errs
  }
  if (t === 'google.protobuf.Value') return errs
  const msg = messages.get(t)
  if (!msg) return [`${path}: tipo ${t} desconocido`]
  if (typeof value !== 'object' || Array.isArray(value)) return [`${path}: objeto esperado (${t})`]
  const fields = new Map((msg.field ?? []).map((f) => [f.name, f]))
  for (const [k, v] of Object.entries(value)) {
    const f = fields.get(k)
    if (!f) { errs.push(`${path}.${k}: campo desconocido en ${t}`); continue }
    if (v === null) continue
    const items = f.label === 'LABEL_REPEATED' ? (Array.isArray(v) ? v : (errs.push(`${path}.${k}: lista esperada`), [])) : [v]
    items.forEach((item, i) => {
      const p = f.label === 'LABEL_REPEATED' ? `${path}.${k}[${i}]` : `${path}.${k}`
      if (f.type === 'TYPE_MESSAGE') errs.push(...checkMessage(f.typeName, item, p))
      else if (SCALAR[f.type] && !SCALAR[f.type](item)) errs.push(`${p}: valor ${JSON.stringify(item)} no es ${f.type}`)
    })
  }
  return errs
}
const streams = readYaml(join(EVENTS, 'streams', 'streams.yaml')).streams
const subjectMatches = (pattern, subject) => {
  const p = pattern.split('.')
  const s = subject.split('.')
  for (let i = 0; i < p.length; i++) {
    if (p[i] === '>') return true
    if (p[i] !== '*' && p[i] !== s[i]) return false
  }
  return p.length === s.length
}
const tmp = mkdtempSync(join(tmpdir(), 'hf-ev-'))
let nEvents = 0
const seenTypes = new Set()
for (const f of walk(join(EVENTS, 'catalog'), (p) => p.endsWith('.yaml'))) {
  const cat = readYaml(f)
  for (const e of cat.events) {
    nEvents++
    const label = `${rel(f)} ${e.type}`
    if (seenTypes.has(e.type)) fail(`${label}: tipo duplicado`)
    seenTypes.add(e.type)
    if (!/^horus\.[a-z_]+\.[a-z_]+\.[a-z_]+$/.test(e.type)) fail(`${label}: type no cumple horus.<dominio>.<entidad>.<evento>`)
    if (e.type.split('.')[1] !== cat.domain) fail(`${label}: el productor publica fuera de su dominio`)
    const concrete = e.subject.replace(/\{[a-z_]+\}/g, '0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55')
    if (concrete.length > 128) fail(`${label}: subject > 128 bytes`)
    if (e.family === 'domain' && !e.subject.startsWith(`${e.type}.`)) fail(`${label}: subject de dominio debe ser <type>.<entity_id>`)
    if (e.family === 'telemetry' && !e.subject.startsWith('horus.telemetry.')) fail(`${label}: telemetría fuera de horus.telemetry.>`)
    const st = streams.find((s) => s.name === e.stream)
    if (!st) fail(`${label}: stream ${e.stream} no definido`)
    else if (!st.subjects.some((p) => subjectMatches(p, concrete))) fail(`${label}: el stream ${e.stream} no cubre ${e.subject}`)
    if (e.pii && !(e.pii_fields ?? []).length) fail(`${label}: pii=true sin pii_fields`)
    if (!['tenant', 'platform'].includes(e.tenant_scope)) fail(`${label}: tenant_scope inválido`)
    const exPath = join(EVENTS, e.example)
    if (!existsSync(exPath)) { fail(`${label}: falta golden file ${e.example}`); continue }
    const ex = readJson(exPath)
    if (e.family === 'telemetry') {
      validate(`${e.example}`, ID('events/v0/envelope.schema.json'), ex, 'TelemetryEnvelope')
      const h = ex.headers?.['Horus-Tenant']
      if (e.tenant_scope === 'tenant' && h === 'platform') fail(`${e.example}: tipo de tenant con Horus-Tenant=platform`)
      if (e.tenant_scope === 'platform' && h !== 'platform') fail(`${e.example}: tipo de plataforma con Horus-Tenant de un ISP`)
      if (ex.headers?.['Horus-Type'] !== e.type) fail(`${e.example}: Horus-Type ≠ type`)
    } else {
      validate(`${e.example}`, ID('events/v0/envelope.schema.json'), ex)
      if (ex.type !== e.type) fail(`${e.example}: type ≠ catálogo`)
      const isAudit = e.type.endsWith('.audit.recorded')
      if (e.tenant_scope === 'tenant' && !ex.tenant_id && !isAudit) fail(`${e.example}: tipo de tenant con tenant_id nulo (CI: todo golden de tenant lleva tenant_id)`)
      if (e.tenant_scope === 'platform' && ex.tenant_id && !e.type.startsWith('horus.auth.tenant.')) fail(`${e.example}: tipo de plataforma con tenant_id`)
    }
    if (e.json_schema) validate(`${e.example} (data vs ${e.json_schema})`, ID('finding/v0/finding.schema.json'), ex.data)
    if (messages) {
      if (!messages.has(e.schema)) fail(`${label}: el mensaje ${e.schema} no existe en packages/protobuf`)
      else for (const err of checkMessage(e.schema, ex.data, 'data')) fail(`${e.example}: ${err}`)
    }
  }
}
rmSync(tmp, { recursive: true, force: true })
const golden = walk(join(EVENTS, 'examples'), (p) => p.endsWith('.json')).map((p) => p.split('/').pop().replace(/\.json$/, ''))
for (const g of golden) if (!seenTypes.has(g)) fail(`examples/${g}.json sin entrada en el catálogo`)
console.log(`${nEvents} tipos de evento`)

// ---------------------------------------------------------------------------------------------
if (WITH_DB) {
  step('7. DDL v0 en bases vacías (C2 PostgreSQL, C3 ClickHouse)')
  const ddl = join(SCHEMAS, 'datastore', 'v0')
  const py = process.env.PYTHON || 'python3'
  const r = run(py, [join(HERE, 'ddl-check.py'), join(ddl, 'postgres-contract-v0.sql'), join(ddl, 'clickhouse-contract-v0.sql')])
  if (r.code !== 0) fail(`DDL v0 (pglast + chdb; PYTHON=${py}):\n${r.out}`)
  else console.log(r.out.split('\n').slice(0, 2).join('\n'))
}

// ---------------------------------------------------------------------------------------------
console.log('')
for (const w of warnings) console.log(`AVISO  ${w}`)
for (const e of errors) console.log(`ERROR  ${e}`)
console.log(errors.length ? `\n❌ contracts-check: ${errors.length} errores` : `\n✅ contracts-check OK${WRITE ? ' (artefactos regenerados)' : ''}`)
process.exit(errors.length ? 1 : 0)
