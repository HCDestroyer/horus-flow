// GET /api/admin/auth/enroll — alta del TOTP: secreto, URI otpauth y QR (SVG en data URL).
import { renderSVG } from 'uqr'
import { enrollStart } from '../../../lib/auth/service'

export default defineEventHandler((event) => {
  try {
    const { secret, uri } = enrollStart(adminAuthDeps(), adminSession(event))
    const svg = renderSVG(uri, { border: 2, pixelSize: 6 })
    return {
      secret,
      uri,
      qr: `data:image/svg+xml;base64,${Buffer.from(svg).toString('base64')}`,
    }
  } catch (err) {
    adminFail(event, err)
  }
})
