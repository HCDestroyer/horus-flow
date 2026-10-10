// Registro de proveedores de pago. PAYMENT_PROVIDER elige uno; si no existe, se usa "manual".
import { manualTransferProvider } from './manual'
import type { PaymentProvider } from './types'

const providers: Record<string, PaymentProvider> = {
  [manualTransferProvider.id]: manualTransferProvider,
  // stripe: stripeProvider,        ← ver README.md › "Añadir una pasarela de pago"
  // recurrente: recurrenteProvider,
  // paypal: paypalProvider,
}

export function getPaymentProvider(id: string): PaymentProvider {
  return providers[id] ?? manualTransferProvider
}

export type { PaymentProvider, PaymentResult, PurchaseOrder } from './types'
