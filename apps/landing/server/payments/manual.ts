// "Solicitud manual / transferencia": no cobra nada en la web. Ventas recibe la solicitud con
// su referencia, envía la cotización y los datos bancarios, y el cliente paga por transferencia
// usando la referencia como concepto.
import type { PaymentProvider } from './types'

export const manualTransferProvider: PaymentProvider = {
  id: 'manual',
  async createPayment() {
    return { provider: 'manual', kind: 'manual' }
  },
}
