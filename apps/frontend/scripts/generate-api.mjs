// Genera los tipos y las copias de contrato que usa el frontend (I0-15, conventions.md §3.5).
//
//   pnpm api:generate   → escribe types/api/*
//   pnpm api:check      → falla si types/api/* no coincide con los contratos (CI)
//
// Fuentes (contratos v0 aprobados, solo lectura):
//   packages/schemas/openapi/dist/horus-api.v0.yaml   → types/api/schema.d.ts (openapi-typescript)
//   packages/schemas/dashboard/v0/widget-types.json   → types/api/widget-catalog.ts (`as const`)
//   packages/schemas/dashboard/v0/templates/*.json    → types/api/contract/templates/*.json
//   packages/schemas/finding/v0/examples/*.json       → types/api/contract/examples/*.json
//   packages/schemas/websocket/v0/examples/*.json     → types/api/contract/examples/ws-*.json
//
// Reproducible: sin fechas ni rutas absolutas en la salida; mismo contrato ⇒ mismos bytes.
/* eslint-disable no-console -- script de línea de comandos */
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join, relative, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import openapiTS, { astToString } from 'openapi-typescript'

const app = resolve(import.meta.dirname, '..')
const schemas = resolve(app, '../../packages/schemas')
const out = join(app, 'types/api')
const check = process.argv.includes('--check')

const header = (source) =>
  `// GENERADO por scripts/generate-api.mjs desde ${source}.\n// No editar: \`pnpm api:generate\`.\n`

/** @type {Map<string, string>} ruta relativa a types/api → contenido */
const files = new Map()

// 1. Tipos del OpenAPI (C5).
const ast = await openapiTS(pathToFileURL(join(schemas, 'openapi/dist/horus-api.v0.yaml')), {
  alphabetize: true,
  // Un `default` de JSON Schema no garantiza que el servidor envíe el campo.
  defaultNonNullable: false,
  // `{type: object}` sin propiedades (filas de tabla, `config`) = mapa de valores desconocidos.
  emptyObjectsUnknown: true,
  // El bundle conserva los `$defs` de los JSON Schema de C9 dentro de components (ya
  // resueltos a `#/components/schemas/…`, nadie los referencia); openapi-typescript los
  // trataría como una propiedad obligatoria `$defs` del objeto. Se descartan.
  transform(schemaObject) {
    if (schemaObject && typeof schemaObject === 'object' && '$defs' in schemaObject) {
      delete schemaObject.$defs
    }
    return undefined
  },
})
files.set(
  'schema.d.ts',
  header('packages/schemas/openapi/dist/horus-api.v0.yaml') +
    '/* eslint-disable */\n' +
    astToString(ast),
)

// 2. Catálogo de widgets (C9, mitad servidor) como constante con tipos literales.
const catalog = JSON.parse(readFileSync(join(schemas, 'dashboard/v0/widget-types.json'), 'utf8'))
files.set(
  'widget-catalog.ts',
  header('packages/schemas/dashboard/v0/widget-types.json') +
    '/* eslint-disable */\n' +
    `export const WIDGET_CATALOG_VERSION = ${JSON.stringify(catalog.version)} as const\n\n` +
    `export const WIDGET_CATALOG = ${JSON.stringify(catalog.data, null, 2)} as const\n\n` +
    "export type WidgetTypeName = (typeof WIDGET_CATALOG)[number]['type']\n",
)

// 3. Copias de documentos de contrato (plantillas y ejemplos) para mocks y tests.
function copyJsonDir(dir, prefix) {
  for (const name of readdirSync(dir)
    .filter((f) => f.endsWith('.json'))
    .sort()) {
    const json = JSON.parse(readFileSync(join(dir, name), 'utf8'))
    files.set(`${prefix}${name}`, JSON.stringify(json, null, 2) + '\n')
  }
}
copyJsonDir(join(schemas, 'dashboard/v0/templates'), 'contract/templates/')
copyJsonDir(join(schemas, 'finding/v0/examples'), 'contract/examples/')
copyJsonDir(join(schemas, 'websocket/v0/examples'), 'contract/examples/ws-')

// Escritura o comprobación.
let drift = 0
for (const [path, content] of files) {
  const target = join(out, path)
  const current = existsSync(target) ? readFileSync(target, 'utf8') : null
  if (current === content) continue
  drift++
  if (check) {
    console.error(`desactualizado: ${relative(app, target)}`)
  } else {
    mkdirSync(dirname(target), { recursive: true })
    writeFileSync(target, content)
    console.log(`escrito: ${relative(app, target)}`)
  }
}

// Sobrantes en contract/ (una plantilla o ejemplo que ya no está en el contrato).
function walk(dir) {
  if (!existsSync(dir)) return []
  return readdirSync(dir, { withFileTypes: true }).flatMap((d) =>
    d.isDirectory() ? walk(join(dir, d.name)) : [join(dir, d.name)],
  )
}
for (const file of walk(join(out, 'contract'))) {
  const rel = relative(out, file).split('\\').join('/')
  if (files.has(rel)) continue
  drift++
  if (check) {
    console.error(`sobrante: ${relative(app, file)}`)
  } else {
    rmSync(file)
    console.log(`borrado: ${relative(app, file)}`)
  }
}

if (check && drift > 0) {
  console.error(`\n${drift} archivo(s) no coinciden con los contratos. Ejecuta: pnpm api:generate`)
  process.exit(1)
}
console.log(
  check ? 'types/api al día con los contratos.' : `Generación completa (${drift} cambios).`,
)
