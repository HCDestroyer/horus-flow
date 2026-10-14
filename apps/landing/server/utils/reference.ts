// Números de referencia legibles: HF-<tipo>-<AAAAMMDD>-<6 caracteres>, p. ej. HF-P-20261010-7K3M9Q.
// Alfabeto Crockford (sin I, L, O, U) para que se puedan dictar por teléfono sin ambigüedad.
import { randomBytes } from 'node:crypto'

const ALPHABET = '0123456789ABCDEFGHJKMNPQRSTVWXYZ'

export type ReferenceKind = 'D' | 'C' | 'P'

export function newReference(kind: ReferenceKind, now = new Date()): string {
  const date = now.toISOString().slice(0, 10).replaceAll('-', '')
  const bytes = randomBytes(6)
  let suffix = ''
  for (const b of bytes) suffix += ALPHABET[b % 32]
  return `HF-${kind}-${date}-${suffix}`
}

export const REFERENCE_RE = /^HF-[DCP]-\d{8}-[0-9A-HJKMNP-TV-Z]{6}$/
