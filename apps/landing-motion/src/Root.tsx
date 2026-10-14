// Composiciones de la landing. Sin texto traducible dentro de los vídeos (solo nombres propios
// e IPs): el texto ES/EN vive en el HTML de la landing, accesible e indexable.
// El póster y los frames clave de cada una están en scripts/render.mjs.
import { Composition } from 'remotion'
import { HeroNetwork, HERO_FPS, HERO_FRAMES } from './compositions/HeroNetwork'
import { HowItWorks, HOW_FPS, HOW_FRAMES } from './compositions/HowItWorks'
import { Resilience, RES_FPS, RES_FRAMES } from './compositions/Resilience'

export function RemotionRoot() {
  return (
    <>
      <Composition
        id="HeroNetwork"
        component={HeroNetwork}
        durationInFrames={HERO_FRAMES}
        fps={HERO_FPS}
        width={1920}
        height={1920}
      />
      <Composition
        id="HowItWorks"
        component={HowItWorks}
        durationInFrames={HOW_FRAMES}
        fps={HOW_FPS}
        width={1920}
        height={640}
      />
      <Composition
        id="Resilience"
        component={Resilience}
        durationInFrames={RES_FRAMES}
        fps={RES_FPS}
        width={1920}
        height={640}
      />
    </>
  )
}
