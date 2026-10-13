import { describe, expect, it } from 'vitest'
import { z } from '~/utils/zod'

// La CSP de la consola no permite eval: Zod debe funcionar en modo jitless (sin la prueba
// `Function("")` que dispara una violación en consola).
describe('zod', () => {
  it('se usa sin JIT', () => {
    expect(z.config().jitless).toBe(true)
    expect(z.object({ a: z.string() }).safeParse({ a: 'x' }).success).toBe(true)
  })
})
