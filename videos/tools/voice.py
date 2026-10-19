"""Narración con VoxCPM2 (openbmb/VoxCPM2, Apache-2.0), uno de los motores de VoiceStudio.

Clona una voz de referencia (videos/voice/ref-<V>.wav) para que todas las frases suenen igual.
Caché por texto: solo regenera lo que cambió. Cada frase nueva pasa un control de calidad
(Whisper: se entiende el texto; clasificador: género esperado; ritmo razonable) y se repite si falla.
Uso: python voice.py <video|all> [--voice L]
Salida: videos/build/voice/<V>/<hash>.wav y videos/build/voice/<V>/<video>.json (duraciones).
"""
import argparse, difflib, hashlib, json, os, re, subprocess, unicodedata, warnings
warnings.filterwarnings('ignore')
import soundfile as sf

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
_BASE = 'Horus Flow ve cada flujo de tu red. Detecta botnets y ataques en segundos, y te avisa cuándo ampliar tus enlaces.'
# Voces creadas por descripción (ninguna es de una persona real). Género verificado con un clasificador.
#   continuation: clona continuando la grabación de referencia (necesita su texto).
#   reference: clona el timbre de la referencia y acepta una indicación de estilo (en inglés).
VOICES = {
    'A': {'mode': 'continuation', 'text': _BASE, 'gender': 'male'},      # hombre; suena a español de España
    'B': {'mode': 'continuation', 'text': _BASE, 'gender': 'female'},
    'P3': {'mode': 'continuation', 'text': _BASE, 'gender': 'female'},
    **{v: {'mode': 'continuation', 'text': 'Mirá, pues: ' + _BASE, 'gender': 'female'} for v in ('serena', 'alegre', 'cercana')},
    # L: timbre de A con acento latinoamericano neutro (ref-L sale de tools/latam-voice.py), locución profesional.
    'L': {'mode': 'full', 'gender': 'male', 'steps': 20, 'cfg': 2.0,
          'text': 'Hola. Con Horus Flow ves cada flujo de tu red, en cualquier zona y a cualquier hora. '
                  'Si el equipo de un cliente empieza a hacer cosas raras, lo sabes en minutos, y sabes qué hacer.'},
}
# L2: igual que L pero solo continuación (sin referencia de timbre aparte); para comparar fluidez.
VOICES['L2'] = {**VOICES['L'], 'mode': 'continuation', 'text': None}
# Solo para la voz: cómo se pronuncian siglas y nombres (los subtítulos usan el texto original).
SAY = [
    # Pistas de pronunciación como las diría un locutor latinoamericano, con la tilde donde va el acento.
    (r'\bHorus Flow\b', 'Órus Flou'), (r'\bHorus\b', 'Órus'), (r'\bArgosISP\b', 'Árgos i-ese-pe'),
    (r'\bNetFlow\b', 'Net-flou'), (r'\bsFlow\b', 'ese-flou'), (r'\bIPFIX\b', 'i-pe-fix'),
    (r'\bDDoS\b', 'dedós'), (r'\bIPv4\b', 'i-pe-ve cuatro'), (r'\bIPv6\b', 'i-pe-ve seis'),
    (r'\bISP\b', 'i-ese-pe'), (r'\bIP\b', 'i-pe'), (r'\bNOC\b', 'nok'), (r'\bSSH\b', 'ese-ese-ache'),
    (r'\bRouterOS\b', 'Ráuter O-ese'), (r'\brouters?\b', lambda m: 'ráuter' + ('s' if m.group(0).endswith('s') else '')),
    (r'\bRouter\b', 'Ráuter'), (r'\bPPPoE\b', 'pe-pe-pe-o-e'), (r'\bMikroTik\b', 'Míkrotik'),
    (r'\bHuawei\b', 'Juawéi'), (r'\bWinbox\b', 'Uínbox'), (r'\bWireGuard\b', 'Guáyer-gard'),
    (r'\bTelegram\b', 'Télegram'), (r'\bdashboards?\b', lambda m: 'dáshbord' + ('s' if m.group(0).endswith('s') else '')),
    (r'\bbotnets?\b', lambda m: 'bótnet' + ('s' if m.group(0).endswith('s') else '')), (r'\bscript\b', 'escrípt'),
    (r'\.rsc\b', ' punto erre-ese-ce'),
]

def spoken(text):
    for pat, rep in SAY:
        text = re.sub(pat, rep, text)
    # Una frase que continúa la anterior ("y te avisa…") empieza en minúscula; al modelo se le da con
    # mayúscula, porque si no a veces vocaliza algo al inicio (se oía como "punto").
    return text[:1].upper() + text[1:]

def norm(s):
    """Letras sin tildes ni 'h' ni espacios: compara texto y transcripción sin castigar siglas ni la h muda."""
    s = unicodedata.normalize('NFD', s.lower())
    s = ''.join(c for c in s if c.isalpha() and unicodedata.category(c) != 'Mn')
    return s.replace('h', '').replace('k', 'c').replace('z', 's').replace('v', 'b').replace('y', 'i')

class Engine:
    def __init__(self):
        import torch, librosa
        torch.set_num_threads(os.cpu_count() or 4)
        self.torch, self.librosa = torch, librosa
        from voxcpm import VoxCPM
        self.tts = VoxCPM.from_pretrained('openbmb/VoxCPM2', load_denoiser=False, optimize=False, device='cpu')
        from faster_whisper import WhisperModel
        self.stt = WhisperModel('small', device='cpu', compute_type='int8')
        from transformers import AutoFeatureExtractor, AutoModelForAudioClassification
        G = 'alefiury/wav2vec2-large-xlsr-53-gender-recognition-librispeech'
        self.gfe = AutoFeatureExtractor.from_pretrained(G)
        self.gm = AutoModelForAudioClassification.from_pretrained(G).eval()

    def check(self, wav, text, gender):
        sr = self.tts.tts_model.sample_rate
        y = self.librosa.resample(wav.astype('float32'), orig_sr=sr, target_sr=16000)
        segs = list(self.stt.transcribe(y, language='es', word_timestamps=True)[0])
        heard = ' '.join(s.text.strip() for s in segs)
        sim = difflib.SequenceMatcher(None, norm(text), norm(heard)).ratio()
        # Fluidez: confianza media de Whisper por palabra (baja si hay sílabas trabadas o mal acentuadas).
        probs = [w.probability for s in segs for w in (s.words or [])]
        flu = sum(probs) / len(probs) if probs else 0.0
        with self.torch.no_grad():
            p = self.gm(**self.gfe(y, sampling_rate=16000, return_tensors='pt')).logits.softmax(-1)[0]
        g = {self.gm.config.id2label[i]: float(p[i]) for i in range(len(p))}
        pace = (len(wav) / sr) / max(1, len(text))  # segundos por carácter
        extra = len(norm(heard)) / max(1, len(norm(text)))  # palabras de más (p. ej. "punto" al inicio)
        ok = sim >= 0.8 and extra <= 1.08 and g.get(gender, 0) >= 0.9 and pace <= 0.095
        return ok, {'sim': round(sim, 2), 'extra': round(extra, 2), 'fluidez': round(flu, 3), gender: round(g.get(gender, 0), 3),
                    'seg_por_car': round(pace, 3), 'oido': heard}

    def gen(self, voice, cfg, sp):
        ref = os.path.join(ROOT, 'voice', f'ref-{voice}.wav')
        steps, cfgv = cfg.get('steps', 10), cfg.get('cfg', 2.0)
        if cfg['mode'] == 'reference':
            return self.tts.generate(text=f"({cfg['style']}){sp}", reference_wav_path=ref, cfg_value=cfgv, inference_timesteps=steps)
        if cfg['mode'] == 'full':  # timbre (referencia) + prosodia y acento (continuación)
            return self.tts.generate(text=sp, reference_wav_path=ref, prompt_wav_path=ref, prompt_text=spoken(cfg['text']),
                                     cfg_value=cfgv, inference_timesteps=steps)
        ptext = cfg['text'] if cfg.get('text') else spoken(VOICES['L']['text'])
        if voice == 'L2':
            ref = os.path.join(ROOT, 'voice', 'ref-L.wav')
        return self.tts.generate(text=sp, prompt_wav_path=ref, prompt_text=ptext, cfg_value=cfgv, inference_timesteps=steps)

    def say(self, voice, cfg, sp, text, takes=1, tries=3):
        """Una toma por frase; solo se repite si no pasa el control (máx. `tries` intentos)."""
        good, best = [], None
        for k in range(tries):
            wav = self.gen(voice, cfg, sp)
            ok, info = self.check(wav, text, cfg['gender'])
            print('  toma', k + 1, 'ok' if ok else 'NO', json.dumps(info, ensure_ascii=False), flush=True)
            if ok:
                good.append((info['fluidez'], wav))
                # Una toma muy fluida basta; si no, se busca otra y se elige la mejor.
                if len(good) >= takes or info['fluidez'] >= 0.92:
                    break
            elif best is None or info['sim'] > best[0]:
                best = (info['sim'], wav)
        if good:
            return max(good, key=lambda x: x[0])[1]
        print('  AVISO: ninguna toma pasó el control; se usa la más inteligible', flush=True)
        return best[1]

def main():
    ap = argparse.ArgumentParser(); ap.add_argument('video'); ap.add_argument('--voice', default='L')
    a = ap.parse_args()
    cfg = VOICES[a.voice]
    stories = json.loads(subprocess.check_output(['node', os.path.join(ROOT, 'tools/export-stories.mjs')]))
    vids = list(stories) if a.video == 'all' else a.video.split(',')
    out = os.path.join(ROOT, 'build/voice', a.voice); os.makedirs(out, exist_ok=True)
    eng = None
    for vid in vids:
        manifest = []
        for i, sc in enumerate(stories[vid]['scenes']):
            text = sc.get('say')
            if not text:
                manifest.append(None); continue
            sp = spoken(text)
            h = hashlib.sha1(f'{a.voice}|{json.dumps(cfg, sort_keys=True)}|{sp}'.encode()).hexdigest()[:16]
            path = os.path.join(out, h + '.wav')
            if not os.path.exists(path):
                eng = eng or Engine()
                print('voz', vid, i, sp, flush=True)
                wav = eng.say(a.voice, cfg, sp, text)
                sf.write(path, wav, eng.tts.tts_model.sample_rate)
            info = sf.info(path)
            manifest.append({'file': path, 'dur': info.frames / info.samplerate, 'text': text})
        with open(os.path.join(out, vid + '.json'), 'w') as f:
            json.dump(manifest, f, ensure_ascii=False, indent=1)
        print('listo', vid, flush=True)

if __name__ == '__main__':
    main()
