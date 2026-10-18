"""Música de fondo sintetizada (sin muestras ni derechos de terceros): pad ambiental en
La menor (Am–F–C–G), bajo suave, arpegio de notas cortas y, en la promo, pulso rítmico.
Uso: python3 music.py <video> <segundos> [--bpm 96] [--energy 0.8]  → videos/build/music/<video>.wav
"""
import argparse, os, subprocess, wave
import numpy as np

SR = 48000
ap = argparse.ArgumentParser(); ap.add_argument('video'); ap.add_argument('secs', type=float)
ap.add_argument('--bpm', type=float, default=96); ap.add_argument('--energy', type=float, default=0.8)
a = ap.parse_args()
n = int(SR * (a.secs + 1)); t = np.arange(n) / SR
rng = np.random.default_rng(7)
hz = lambda m: 440 * 2 ** ((m - 69) / 12)
beat = 60 / a.bpm; bar = beat * 4
chords = [[57, 60, 64, 69], [53, 57, 60, 65], [48, 55, 60, 64], [55, 59, 62, 67]]  # Am F C G
L = np.zeros(n); R = np.zeros(n)

def env(start, dur, att, rel):
    e = np.zeros(n); i0 = int(start * SR); i1 = min(n, int((start + dur + rel) * SR))
    if i0 >= n: return e
    tt = t[i0:i1] - start
    e[i0:i1] = np.clip(tt / att, 0, 1) * np.where(tt > dur, np.exp(-(tt - dur) / (rel / 4)), 1)
    return e

k = 0; s = 0.0
while s < a.secs + 1:
    ch = chords[k % 4]; e = env(s, bar * 2, 1.2, 1.6)
    for j, m in enumerate(ch):  # pad: senos desafinados en estéreo
        for det, pan in ((-0.08, 0.3), (0.08, 0.7)):
            f = hz(m + det); v = np.sin(2 * np.pi * f * t + j) + 0.25 * np.sin(4 * np.pi * f * t)
            L += v * e * 0.05 * (1 - pan); R += v * e * 0.05 * pan
    b = env(s, bar * 2 - 0.2, 0.08, 0.6) * np.sin(2 * np.pi * hz(ch[0] - 12) * t) * 0.13  # bajo
    L += b; R += b
    for i in range(16):  # arpegio de corcheas con decaimiento corto
        st = s + i * beat / 2
        if st > a.secs: break
        m = ch[[0, 1, 2, 3, 2, 1, 3, 2][i % 8]] + 12
        i0 = int(st * SR); i1 = min(n, i0 + int(SR * 0.9)); tt = t[i0:i1] - st
        v = np.sin(2 * np.pi * hz(m) * tt) * np.exp(-tt * 5) * 0.045 * a.energy
        pan = 0.35 + 0.3 * ((i % 4) / 3)
        L[i0:i1] += v * (1 - pan); R[i0:i1] += v * pan
    if a.energy >= 1:  # pulso: bombo suave en negras y hi-hat en contratiempos
        for i in range(8):
            st = s + i * beat
            i0 = int(st * SR); i1 = min(n, i0 + int(SR * 0.35)); tt = t[i0:i1] - st
            if i1 <= i0: continue
            kick = np.sin(2 * np.pi * (50 + 90 * np.exp(-tt * 30)) * tt) * np.exp(-tt * 9) * 0.22
            L[i0:i1] += kick; R[i0:i1] += kick
            h0 = int((st + beat / 2) * SR); h1 = min(n, h0 + int(SR * 0.06))
            if h1 > h0:
                hh = rng.standard_normal(h1 - h0) * np.exp(-np.arange(h1 - h0) / SR * 60) * 0.03
                L[h0:h1] += hh * 0.8; R[h0:h1] += hh
    s += bar * 2; k += 1

fade = np.clip(t / 1.5, 0, 1); L *= fade; R *= fade
x = np.stack([L, R], 1); x /= np.abs(x).max() + 1e-9; x *= 0.7
out = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), 'build/music'); os.makedirs(out, exist_ok=True)
raw = os.path.join(out, a.video + '.raw.wav')
with wave.open(raw, 'wb') as w:
    w.setnchannels(2); w.setsampwidth(2); w.setframerate(SR); w.writeframes((x * 32767).astype('<i2').tobytes())
# Pulido: paso bajo suave y reverberación ligera.
subprocess.run(['ffmpeg', '-y', '-loglevel', 'error', '-i', raw, '-af',
                'lowpass=f=6500,aecho=0.8:0.6:120|260:0.25|0.15,loudnorm=I=-20:TP=-2', '-ar', str(SR), os.path.join(out, a.video + '.wav')], check=True)
os.remove(raw)
print('música', a.video, a.secs)
