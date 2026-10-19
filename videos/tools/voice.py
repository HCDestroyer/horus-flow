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
    'L': {'mode': 'reference', 'gender': 'male',
          'style': 'Professional Latin American Spanish voice-over announcer, neutral accent with seseo, '
                   'natural, confident and warm, smooth steady pace'},
}
# Solo para la voz: cómo se pronuncian siglas y nombres (los subtítulos usan el texto original).
SAY = [
    (r'\bsFlow\b', 'ese flow'), (r'\bDDoS\b', 'de dos'), (r'\bIPv4\b', 'I P ve cuatro'), (r'\bIPv6\b', 'I P ve seis'),
    (r'\bArgosISP\b', 'Argos I S P'), (r'\bISP\b', 'I S P'), (r'\bIPFIX\b', 'I P fix'), (r'\bIP\b', 'I P'),
    (r'\bNOC\b', 'noc'), (r'\bSSH\b', 'ese ese hache'), (r'\bRouterOS\b', 'Router O S'), (r'\bPPPoE\b', 'pe pe pe o e'),
    (r'\bMikroTik\b', 'Mikrotik'), (r'\bWinbox\b', 'Uinbox'), (r'\bWireGuard\b', 'Uaier Gard'), (r'\bTelegram\b', 'Télegram'),
    (r'\bdashboards?\b', lambda m: 'dashbord' + ('s' if m.group(0).endswith('s') else '')),
    (r'\bhandshake\b', 'jandsheik'), (r'\bbotnets?\b', lambda m: 'bótnet' + ('s' if m.group(0).endswith('s') else '')),
]

def spoken(text):
    for pat, rep in SAY:
        text = re.sub(pat, rep, text)
    return text

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
        heard = ' '.join(s.text.strip() for s in self.stt.transcribe(y, language='es')[0])
        sim = difflib.SequenceMatcher(None, norm(text), norm(heard)).ratio()
        with self.torch.no_grad():
            p = self.gm(**self.gfe(y, sampling_rate=16000, return_tensors='pt')).logits.softmax(-1)[0]
        g = {self.gm.config.id2label[i]: float(p[i]) for i in range(len(p))}
        pace = (len(wav) / sr) / max(1, len(text))  # segundos por carácter
        ok = sim >= 0.8 and g.get(gender, 0) >= 0.9 and pace <= 0.095
        return ok, {'sim': round(sim, 2), gender: round(g.get(gender, 0), 3), 'seg_por_car': round(pace, 3), 'oido': heard}

    def say(self, voice, cfg, sp, text, tries=3):
        ref = os.path.join(ROOT, 'voice', f'ref-{voice}.wav')
        best = None
        for k in range(tries):
            if cfg['mode'] == 'reference':
                wav = self.tts.generate(text=f"({cfg['style']}){sp}", reference_wav_path=ref, cfg_value=2.0, inference_timesteps=12)
            else:
                wav = self.tts.generate(text=sp, prompt_wav_path=ref, prompt_text=cfg['text'], cfg_value=2.0, inference_timesteps=10)
            ok, info = self.check(wav, text, cfg['gender'])
            print('  intento', k + 1, 'ok' if ok else 'NO', json.dumps(info, ensure_ascii=False), flush=True)
            if ok:
                return wav
            if best is None or info['sim'] > best[1]['sim']:
                best = (wav, info)
        print('  AVISO: ningún intento pasó el control; se usa el más inteligible', flush=True)
        return best[0]

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
