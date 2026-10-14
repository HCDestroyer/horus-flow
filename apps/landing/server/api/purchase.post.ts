// POST /api/purchase — solicitud de compra / cotización (shared/schemas.ts › purchaseSchema).
// Devuelve el número de referencia y lo que indique el proveedor de pago (hoy, manual).
import { submitPurchase } from '../utils/submissions'

export default defineEventHandler(async (event) => {
  const body = await readJsonBody(event)
  const result = await submitPurchase(body, requestClientIp(event), useSubmissionDeps())
  return sendResult(event, result)
})
