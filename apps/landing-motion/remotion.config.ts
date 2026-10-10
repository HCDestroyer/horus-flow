// Configuración de Remotion Studio y de la CLI (`pnpm studio`). El render de producción lo hace
// scripts/render.mjs con la API de @remotion/renderer, que fija aparte el navegador y los códecs.
import { Config } from '@remotion/cli/config'

// Chromium ya instalado en la máquina (Playwright): nunca se descarga otro.
const browser = process.env.REMOTION_BROWSER_EXECUTABLE
  ?? '/opt/pw-browsers/chromium_headless_shell-1194/chrome-linux/headless_shell'

Config.setBrowserExecutable(browser)
Config.setVideoImageFormat('png')
Config.setConcurrency(4)
