// Rate limit en memoria por clave (IP), con ventana deslizante. Suficiente para una sola
// instancia de la landing; con varias réplicas, cada una cuenta por separado (README.md).

export interface RateLimitResult {
  allowed: boolean
  remaining: number
  /** Segundos hasta que vuelva a haber cupo (solo si allowed = false). */
  retryAfterSec: number
}

export class RateLimiter {
  private hits = new Map<string, number[]>()

  constructor(
    readonly max: number,
    readonly windowMs: number,
    /** Tope de claves en memoria; al superarlo se purgan las caducadas. */
    private readonly maxKeys = 10_000,
  ) {}

  hit(key: string, now = Date.now()): RateLimitResult {
    const since = now - this.windowMs
    const recent = (this.hits.get(key) ?? []).filter((t) => t > since)
    if (recent.length >= this.max) {
      this.hits.set(key, recent)
      const retryAfterSec = Math.max(1, Math.ceil((recent[0]! + this.windowMs - now) / 1000))
      return { allowed: false, remaining: 0, retryAfterSec }
    }
    recent.push(now)
    this.hits.set(key, recent)
    if (this.hits.size > this.maxKeys) this.prune(now)
    return { allowed: true, remaining: this.max - recent.length, retryAfterSec: 0 }
  }

  prune(now = Date.now()): void {
    const since = now - this.windowMs
    for (const [key, times] of this.hits) {
      if (!times.some((t) => t > since)) this.hits.delete(key)
    }
  }

  reset(): void {
    this.hits.clear()
  }
}
