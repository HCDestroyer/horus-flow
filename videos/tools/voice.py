"""Narración con VoxCPM2 (openbmb/VoxCPM2, Apache-2.0), uno de los motores de VoiceStudio.

Clona una voz de referencia (videos/voice/ref-<V>.wav, creada con diseño de voz) para que todas
las frases suenen igual. Caché por texto: solo regenera lo que cambió.
Uso: python voice.py <video|all> [--voice A]
Salida: videos/build/voice/<V>/<hash>.wav y videos/build/voice/<V>/<video>.json (duraciones).
"""
import argparse, hashlib, json, os, re, subprocess, sys
import soundfile as sf

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
REF_TEXT = {
    'A': 'Horus Flow ve cada flujo de tu red. Detecta botnets y ataques en segundos, y te avisa cuándo ampliar tus enlaces.',
    'B': 'Horus Flow ve cada flujo de tu red. Detecta botnets y ataques en segundos, y te avisa cuándo ampliar tus enlaces.',
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

def main():
    ap = argparse.ArgumentParser(); ap.add_argument('video'); ap.add_argument('--voice', default='A')
    a = ap.parse_args()
    stories = json.loads(subprocess.check_output(['node', os.path.join(ROOT, 'tools/export-stories.mjs')]))
    vids = list(stories) if a.video == 'all' else [a.video]
    out = os.path.join(ROOT, 'build/voice', a.voice); os.makedirs(out, exist_ok=True)
    model = None
    for vid in vids:
        manifest = []
        for i, sc in enumerate(stories[vid]['scenes']):
            text = sc.get('say')
            if not text:
                manifest.append(None); continue
            sp = spoken(text)
            h = hashlib.sha1(f'{a.voice}|{sp}'.encode()).hexdigest()[:16]
            path = os.path.join(out, h + '.wav')
            if not os.path.exists(path):
                if model is None:
                    import torch; torch.set_num_threads(os.cpu_count() or 4)
                    from voxcpm import VoxCPM
                    model = VoxCPM.from_pretrained('openbmb/VoxCPM2', load_denoiser=False, optimize=False, device='cpu')
                ref = os.path.join(ROOT, 'voice', f'ref-{a.voice}.wav')
                wav = model.generate(text=sp, prompt_wav_path=ref, prompt_text=REF_TEXT[a.voice], cfg_value=2.0, inference_timesteps=10)
                sf.write(path, wav, model.tts_model.sample_rate)
                print('voz', vid, i, round(len(wav) / model.tts_model.sample_rate, 2), sp, flush=True)
            info = sf.info(path)
            manifest.append({'file': path, 'dur': info.frames / info.samplerate, 'text': text})
        with open(os.path.join(out, vid + '.json'), 'w') as f:
            json.dump(manifest, f, ensure_ascii=False, indent=1)
        print('listo', vid, flush=True)

if __name__ == '__main__':
    main()
