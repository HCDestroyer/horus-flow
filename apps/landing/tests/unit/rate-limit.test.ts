import { describe, expect, it } from 'vitest'
import { RateLimiter } from '../../server/utils/rate-limit'

describe('RateLimiter', () => {
  it('permite hasta max en la ventana y luego bloquea con Retry-After', () => {
    const rl = new RateLimiter(3, 60_000)
    expect(rl.hit('a', 0).allowed).toBe(true)
    expect(rl.hit('a', 1000).allowed).toBe(true)
    expect(rl.hit('a', 2000).remaining).toBe(0)
    const blocked = rl.hit('a', 3000)
    expect(blocked.allowed).toBe(false)
    expect(blocked.retryAfterSec).toBe(57)
  })

  it('cuenta cada clave por separado', () => {
    const rl = new RateLimiter(1, 60_000)
    expect(rl.hit('a', 0).allowed).toBe(true)
    expect(rl.hit('b', 0).allowed).toBe(true)
    expect(rl.hit('a', 1).allowed).toBe(false)
  })

  it('la ventana se desliza', () => {
    const rl = new RateLimiter(2, 10_000)
    rl.hit('a', 0)
    rl.hit('a', 5000)
    expect(rl.hit('a', 9000).allowed).toBe(false)
    expect(rl.hit('a', 10_001).allowed).toBe(true)
  })

  it('purga claves caducadas al superar el tope de memoria', () => {
    const rl = new RateLimiter(1, 1000, 2)
    rl.hit('a', 0)
    rl.hit('b', 0)
    rl.hit('c', 5000)
    expect(rl.hit('a', 5001).allowed).toBe(true)
  })
})
