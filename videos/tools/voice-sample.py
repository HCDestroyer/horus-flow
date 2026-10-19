"""Muestra corta de una voz con frases reales de los guiones, para aprobarla antes de regenerar videos.
Uso: python voice-sample.py <voz> <salida.wav> [video:escena ...]   (por defecto, tres escenas de la demo)
"""
import importlib.util, json, os, subprocess, sys
import numpy as np, soundfile as sf

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
spec = importlib.util.spec_from_file_location('voice', os.path.join(ROOT, 'tools/voice.py'))
voice = importlib.util.module_from_spec(spec); spec.loader.exec_module(voice)

name, out = sys.argv[1], sys.argv[2]
picks = sys.argv[3:] or ['demo:0', 'demo:1', 'demo:4']
stories = json.loads(subprocess.check_output(['node', os.path.join(ROOT, 'tools/export-stories.mjs')]))
eng = voice.Engine(); cfg = voice.VOICES[name]; sr = eng.tts.tts_model.sample_rate
parts = []
for p in picks:
    vid, i = p.split(':'); text = stories[vid]['scenes'][int(i)]['say']
    print('frase', p, text, flush=True)
    parts += [eng.say(name, cfg, voice.spoken(text), text), np.zeros(int(sr * 0.6), dtype='float32')]
sf.write(out, np.concatenate(parts), sr)
print('FIN', out, flush=True)
