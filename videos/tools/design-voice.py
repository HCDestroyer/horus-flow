"""Crea voces de referencia por descripción con VoxCPM2 y verifica el género con un clasificador.

VoxCPM2 a veces ignora el género si la descripción está en español: por eso las descripciones van
en inglés y cada intento pasa por un clasificador de voz (wav2vec2, género) y por Whisper. Solo se
guarda una voz si sale del género pedido con confianza ≥ 0.95 y el texto se entiende.
Uso: python design-voice.py <salida_dir> [--intentos 3]
"""
import argparse, json, os, sys, warnings
warnings.filterwarnings('ignore')
import librosa, soundfile as sf, torch

TEXT = 'Mirá, pues: Horus Flow ve cada flujo de tu red. Detecta botnets y ataques en segundos, y te avisa cuándo ampliar tus enlaces.'
PROFILES = [
    ('paisa-serena', 'female',
     'A Colombian woman from Medellín in her mid thirties, female voice, clear paisa accent from Antioquia, '
     'calm, confident and professional corporate presenter, medium pitch'),
    ('paisa-alegre', 'female',
     'A young Colombian woman from Medellín, about twenty five, female voice, strong sing-song paisa accent, '
     'bright, energetic and cheerful social media host'),
    ('paisa-cercana', 'female',
     'A warm Colombian woman from Antioquia in her forties, female voice, natural paisa accent, '
     'slow, friendly and reassuring, like a trusted advisor'),
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

def gender(y16):
    with torch.no_grad():
        p = gm(**gfe(y16, sampling_rate=16000, return_tensors='pt')).logits.softmax(-1)[0]
    return {gm.config.id2label[i]: float(p[i]) for i in range(len(p))}

report = []
for name, want, desc in PROFILES:
    for k in range(a.intentos):
        w = tts.generate(text=f'({desc}){TEXT}', cfg_value=2.5, inference_timesteps=10)
        sr = tts.tts_model.sample_rate
        y16 = librosa.resample(w.astype('float32'), orig_sr=sr, target_sr=16000)
        g = gender(y16)
        txt = ' '.join(s.text.strip() for s in stt.transcribe(y16, language='es')[0])
        ok = g.get(want, 0) >= 0.95 and 'flujo' in txt.lower() and 'enlaces' in txt.lower()
        print(json.dumps({'voz': name, 'intento': k + 1, 'genero': g, 'texto': txt, 'ok': ok}, ensure_ascii=False), flush=True)
        if ok:
            sf.write(os.path.join(a.out, f'{name}.wav'), w, sr)
            report.append({'voz': name, 'genero': g, 'texto': txt, 'segundos': round(len(w) / sr, 2)})
            break
json.dump(report, open(os.path.join(a.out, 'informe.json'), 'w'), ensure_ascii=False, indent=1)
print('FIN', len(report), 'voces', flush=True)
