# Superpone una cuadrícula normalizada (0..1) a capturas para elegir encuadres. Uso: grid.py out.jpg a.png b.png ...
import sys
from PIL import Image, ImageDraw, ImageFont
out, files = sys.argv[1], sys.argv[2:]
W, H = 1600, 900
f = ImageFont.truetype('/usr/share/fonts/opentype/inter/Inter-Bold.otf', 16)
sheet = Image.new('RGB', (W * (2 if len(files) > 1 else 1), H * ((len(files) + 1) // 2)), 'black')
for i, fn in enumerate(files):
    im = Image.open(fn).convert('RGB').resize((W, H)); d = ImageDraw.Draw(im)
    for k in range(1, 10):
        d.line([(k * W / 10, 0), (k * W / 10, H)], fill=(255, 255, 0), width=1)
        d.line([(0, k * H / 10), (W, k * H / 10)], fill=(255, 255, 0), width=1)
        d.text((k * W / 10 + 3, 3), f'.{k}', fill='yellow', font=f); d.text((3, k * H / 10 + 3), f'.{k}', fill='yellow', font=f)
    d.text((W - 400, H - 24), fn.split('/')[-1], fill='magenta', font=f)
    sheet.paste(im, ((i % 2) * W, (i // 2) * H))
sheet.save(out, quality=78)
