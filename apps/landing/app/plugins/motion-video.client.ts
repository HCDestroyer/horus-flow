// Activa los MotionVideo del HTML del servidor (las secciones de la portada no se hidratan) y
// los de cada página a la que se navega en el cliente. Ver app/utils/motion-video.ts.
import { enhanceAllMotionVideos } from '~/utils/motion-video'

export default defineNuxtPlugin((nuxtApp) => {
  const run = () => {
    requestAnimationFrame(() => enhanceAllMotionVideos())
  }
  nuxtApp.hook('app:mounted', run)
  nuxtApp.hook('page:finish', run)
})
