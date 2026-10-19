# Videos de Horus Flow y ArgosISP

Producción de los videos de producto **con datos de demostración** (mocks de Horus Flow y
fixtures de ArgosISP; nunca datos reales de clientes). Identidad "vidrio y luz" de la skill
`horus-identity`.

| Video | Para qué | Duración aprox. |
| --- | --- | --- |
| `demo` | Demo de producto para la landing | ~80 s |
| `promo` | Promo corta para redes | ~21 s |
| `integracion` | Integración Horus Flow ↔ ArgosISP | ~60 s |
| `tutorial` | Recorrido: alta de router MikroTik y pantalla NOC | ~2:20 |

Cada uno sale en **16:9** (1920×1080) y **9:16** (1080×1920), con voz, subtítulos quemados,
un `.srt` aparte y música de fondo.

## Piezas

- `capture/horus.mjs`, `capture/argos.mjs` — capturas reales de las consolas con Playwright
  (Chromium del entorno), 1600×900 a 2× → `build/shots/`.
- `src/stories.mjs` — guiones: escenas, encuadres, resaltes y narración.
- `src/compositor.html` — dibuja cada cuadro a partir del tiempo (aura, ventana de captura con
  paneo y zoom, resaltes con luz teal, rótulos, subtítulos, títulos y cierre).
- `src/render.mjs` — línea de tiempo según la duración real de la voz → cuadros → ffmpeg →
  mezcla de voz y música (la música baja cuando habla la voz) → `build/out/<video>-<formato>.mp4` y `.srt`.
- `tools/voice.py` — narración con **VoxCPM2** (`openbmb/VoxCPM2`, Apache-2.0), uno de los
  motores de [VoiceStudio](https://github.com/debpalash/VoiceStudio). Clona la voz de
  `voice/ref-A.wav` (creada con diseño de voz) para que todas las frases suenen igual.
  No se usa OmniVoice, el motor por defecto de VoiceStudio: sus pesos son CC-BY-NC (no comercial).
- `tools/music.py` — música sintetizada (sin muestras de terceros).

## Cómo regenerar

```sh
# 1. Horus Flow con la API simulada
cd apps/frontend && pnpm install && pnpm build:mocks && node tests/e2e/serve.mjs &   # :4173
# 2. ArgosISP en modo fixtures (repo hcdestroyer/argos-isp)
cd apps/web && NUXT_PUBLIC_USE_FIXTURES=true pnpm exec nuxt build && PORT=3150 NUXT_PUBLIC_USE_FIXTURES=true node .output/server/index.mjs &
# 3. Capturas
node videos/capture/horus.mjs && node videos/capture/argos.mjs
# 4. Voz (venv con torch CPU + voxcpm>=2.0.3) y música
python videos/tools/voice.py all --voice L
python3 videos/tools/music.py demo 90 --bpm 96 --energy 0.8
# 5. Video
node videos/src/render.mjs demo 16x9
node videos/src/render.mjs demo 9x16
```

`--preview 3,10,25` en `render.mjs` guarda cuadros sueltos para revisar encuadres sin renderizar todo.
