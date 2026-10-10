// Interfaz de proveedor de pago. La pasarela aún no está decidida: hoy solo existe la
// implementación "solicitud manual / transferencia" (manual.ts). Cómo añadir Stripe, Recurrente
// o PayPal: README.md › "Añadir una pasarela de pago".
import type { Currency, Period, PlanId } from '../../app/config/pricing'

/** Solicitud de compra ya validada, con su número de referencia. */
export interface PurchaseOrder {
  reference: string
  plan: PlanId
  period: Period
  currency: Currency
  /** Importe publicable o null (precios sin confirmar, o plan a medida). */
  amount: number | null
  customer: {
    legalName: string
    nit: string
    country: string
    contactName: string
    email: string
    phone: string
  }
  locale: 'es' | 'en'
}

export type PaymentResult =
  /** El pago se gestiona fuera de la web (cotización + transferencia). */
  | { provider: string; kind: 'manual' }
  /** La pasarela devuelve una URL de pago a la que se redirige al cliente. */
  | { provider: string; kind: 'redirect'; url: string }

export interface PaymentProvider {
  /** Identificador estable (PAYMENT_PROVIDER), p. ej. "manual", "stripe", "recurrente". */
  readonly id: string
  /**
   * Prepara el pago de una solicitud de compra. Se llama después de validar y antes de avisar a
   * ventas; si lanza, la solicitud se sigue enviando a ventas como manual.
   */
  createPayment(order: PurchaseOrder): Promise<PaymentResult>
}
