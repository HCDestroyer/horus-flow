// POST /api/lead — solicitud de demo o contacto (shared/schemas.ts › leadSchema).
import { submitLead } from '../utils/submissions'

export default defineEventHandler(async (event) => {
  const body = await readJsonBody(event)
  const result = await submitLead(body, requestClientIp(event), useSubmissionDeps())
  return sendResult(event, result)
})
