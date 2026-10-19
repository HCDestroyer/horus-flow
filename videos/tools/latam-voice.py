"""Variantes de voz masculina con acento latinoamericano neutro y lectura natural (VoxCPM2).

1. A-latam: el timbre de la voz A (clonación por referencia) con indicación de acento y estilo.
2. mx: voz nueva, hombre de Ciudad de México.
3. bog: voz nueva, hombre de Bogotá.
Ninguna copia a una persona real. Cada intento se verifica con un clasificador de género y Whisper.
Uso: python latam-voice.py <salida_dir> [--intentos 3]
"""
import argparse, json, os, warnings
warnings.filterwarnings('ignore')
import librosa, soundfile as sf, torch

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
# Frase de prueba con c/z ("zona", "hacer", "empieza") para notar si suena latina (seseo) o española.
TEXT = ('Hola. Con Horus Flow ves cada flujo de tu red, en cualquier zona y a cualquier hora. '
        'Si el equipo de un cliente empieza a hacer cosas raras, lo sabes en minutos, y sabes qué hacer.')
STYLE = 'Neutral Latin American Spanish accent with seseo, natural, relaxed and conversational delivery'
VARIANTS = [
    ('A-latam', {'reference_wav_path': os.path.join(ROOT, 'voice/ref-A.wav')}, STYLE),
    ('mx', {}, 'A man in his thirties from Mexico City, male voice, ' + STYLE + ', warm and friendly tech presenter'),
    ('bog', {}, 'A man in his thirties from Bogotá, Colombia, male voice, ' + STYLE + ', clear and calm presenter'),
]

ap = argparse.ArgumentParser(); ap.add_argument('out'); ap.add_argument('--intentos', type=int, default=3)
a = ap.parse_args(); os.makedirs(a.out, exist_ok=True)
torch.set_num_threads(os.cpu_count() or 4)
from transformers import AutoFeatureExtractor, AutoModelForAudioClassification
G = 'alefiury/wav2vec2-large-xlsr-53-gender-recognition-librispeech'
gfe = AutoFeatureExtractor.from_pretrained(G); gm = AutoModelForAudioClassification.from_pretrained(G).eval()
from faster_whisper import WhisperModel
stt = WhisperModel('small', device='cpu', compute_type='int8')
from voxcpm import VoxCPM
tts = VoxCPM.from_pretrained('openbmb/VoxCPM2', load_denoiser=False, optimize=False, device='cpu')

for name, kw, desc in VARIANTS:
    for k in range(a.intentos):
        w = tts.generate(text=f'({desc}){TEXT}', cfg_value=2.0, inference_timesteps=20, **kw)
        sr = tts.tts_model.sample_rate
        y16 = librosa.resample(w.astype('float32'), orig_sr=sr, target_sr=16000)
        with torch.no_grad():
            p = gm(**gfe(y16, sampling_rate=16000, return_tensors='pt')).logits.softmax(-1)[0]
        male = float(p[[i for i, l in gm.config.id2label.items() if l == 'male'][0]])
        txt = ' '.join(s.text.strip() for s in stt.transcribe(y16, language='es')[0])
        ok = male >= 0.95 and 'zona' in txt.lower() and 'minutos' in txt.lower()
        print(json.dumps({'voz': name, 'intento': k + 1, 'hombre': round(male, 3), 'seg': round(len(w) / sr, 1), 'texto': txt, 'ok': ok}, ensure_ascii=False), flush=True)
        if ok:
            sf.write(os.path.join(a.out, f'{name}.wav'), w, sr)
            break
print('FIN', flush=True)
