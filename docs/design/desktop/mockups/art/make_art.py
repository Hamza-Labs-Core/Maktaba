"""Stand-in art for the desktop mockups (story 13.9): documentaries and history, drawn here.

Demo media in Maktaba's mockups and references shows no women or girls (posters, frames,
thumbnails, avatars, characters or names). Until the new demo library lands, the mockups use
these drawn scenes (space, desert, reef, sky, mountains, a library's arches) and the demo
library's three children's animations with only animals in them (Big Buck Bunny, Caminandes).

    uvx --with pillow python -I docs/design/desktop/mockups/art/make_art.py
"""

import math
import random
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter

OUT = Path(__file__).resolve().parent
W, H = 1280, 720


def grad(top, bottom, w=W, h=H):
    im = Image.new("RGB", (w, h))
    d = ImageDraw.Draw(im)
    for y in range(h):
        t = y / (h - 1)
        d.line([(0, y), (w, y)], fill=tuple(int(top[i] + (bottom[i] - top[i]) * t) for i in range(3)))
    return im


def glow(im, xy, r, colour, alpha=160):
    layer = Image.new("RGBA", im.size, (0, 0, 0, 0))
    ImageDraw.Draw(layer).ellipse([xy[0] - r, xy[1] - r, xy[0] + r, xy[1] + r], fill=colour + (alpha,))
    layer = layer.filter(ImageFilter.GaussianBlur(r / 2))
    return Image.alpha_composite(im.convert("RGBA"), layer).convert("RGB")


def stars(im, n, seed, top=1.0):
    rnd = random.Random(seed)
    d = ImageDraw.Draw(im)
    for _ in range(n):
        x, y = rnd.randrange(W), rnd.randrange(int(H * top))
        b = rnd.randrange(120, 255)
        s = rnd.choice([0, 0, 0, 1])
        d.ellipse([x - s, y - s, x + s, y + s], fill=(b, b, min(255, b + 10)))


def ridge(d, base, amp, freq, phase, colour, w=W, h=H, jag=0.0, seed=0):
    rnd = random.Random(seed)
    pts = [(0, h)]
    for x in range(0, w + 8, 8):
        y = base + amp * math.sin(x * freq + phase) + amp * 0.5 * math.sin(x * freq * 2.3 + phase * 1.7)
        y += rnd.uniform(-jag, jag)
        pts.append((x, y))
    pts.append((w, h))
    d.polygon(pts, fill=colour)


def saturn():
    im = grad((4, 6, 14), (10, 14, 30))
    stars(im, 900, 1)
    im = glow(im, (820, 330), 230, (227, 178, 82), 40)
    d = ImageDraw.Draw(im, "RGBA")
    cx, cy, r = 820, 340, 170
    # the far half of the rings, behind the planet
    for i, (a, c) in enumerate([(380, (200, 170, 120, 90)), (330, (225, 195, 140, 150)), (290, (180, 150, 105, 120))]):
        d.arc([cx - a, cy - a * 0.28, cx + a, cy + a * 0.28], 180, 360, fill=c, width=14 - i * 3)
    planet = Image.new("RGBA", (2 * r, 2 * r), (0, 0, 0, 0))
    pd = ImageDraw.Draw(planet)
    for y in range(2 * r):
        t = y / (2 * r)
        band = 0.5 + 0.5 * math.sin(t * 26)
        col = (int(205 + 30 * band), int(170 + 25 * band), int(115 + 20 * band), 255)
        half = math.sqrt(max(0, r * r - (y - r) ** 2))
        pd.line([(r - half, y), (r + half, y)], fill=col)
    shade = Image.new("L", (2 * r, 2 * r), 0)
    ImageDraw.Draw(shade).ellipse([-r * 0.6, -r * 0.4, 2 * r * 0.95, 2 * r * 1.05], fill=255)
    shade = shade.filter(ImageFilter.GaussianBlur(40))
    dark = Image.new("RGBA", (2 * r, 2 * r), (8, 10, 20, 255))
    planet = Image.composite(planet, Image.composite(dark, planet, planet.split()[3]), shade)
    mask = Image.new("L", (2 * r, 2 * r), 0)
    ImageDraw.Draw(mask).ellipse([0, 0, 2 * r - 1, 2 * r - 1], fill=255)
    im.paste(planet.convert("RGB"), (cx - r, cy - r), mask)
    d = ImageDraw.Draw(im, "RGBA")
    for i, (a, c) in enumerate([(380, (200, 170, 120, 110)), (330, (235, 205, 150, 190)), (290, (185, 155, 110, 150))]):
        d.arc([cx - a, cy - a * 0.28, cx + a, cy + a * 0.28], 0, 180, fill=c, width=14 - i * 3)
    im = glow(im, (180, 560), 40, (160, 190, 255), 90)
    return im


def dunes():
    im = grad((52, 40, 78), (238, 150, 92))
    im = glow(im, (900, 420), 120, (255, 214, 150), 200)
    d = ImageDraw.Draw(im)
    d.ellipse([850, 370, 950, 470], fill=(255, 226, 170))
    ridge(d, 470, 40, 0.004, 0.5, (196, 112, 70))
    ridge(d, 540, 50, 0.003, 2.0, (160, 84, 56))
    ridge(d, 620, 45, 0.005, 4.0, (112, 56, 44))
    ridge(d, 690, 25, 0.006, 1.0, (70, 36, 34))
    return im


def reef():
    im = grad((18, 92, 140), (4, 20, 44))
    layer = Image.new("RGBA", im.size, (0, 0, 0, 0))
    ld = ImageDraw.Draw(layer)
    for x0 in (180, 420, 660, 900, 1120):
        ld.polygon([(x0 - 30, 0), (x0 + 30, 0), (x0 + 160, H), (x0 + 40, H)], fill=(180, 230, 255, 38))
    im = Image.alpha_composite(im.convert("RGBA"), layer.filter(ImageFilter.GaussianBlur(18))).convert("RGB")
    d = ImageDraw.Draw(im)
    rnd = random.Random(4)
    for _ in range(26):
        x, y, s = rnd.randrange(80, 1200), rnd.randrange(120, 480), rnd.randrange(10, 26)
        c = rnd.choice([(250, 180, 70), (240, 120, 60), (120, 200, 230), (250, 220, 120)])
        d.ellipse([x - s, y - s * 0.45, x + s, y + s * 0.45], fill=c)
        d.polygon([(x + s, y), (x + s * 1.6, y - s * 0.5), (x + s * 1.6, y + s * 0.5)], fill=c)
        d.ellipse([x - s * 0.6, y - 2, x - s * 0.6 + 4, y + 2], fill=(10, 20, 30))
    ridge(d, 600, 30, 0.01, 1, (40, 60, 70), jag=6, seed=2)
    for x in range(40, W, 70):
        h = rnd.randrange(60, 170)
        c = rnd.choice([(214, 92, 96), (230, 140, 70), (150, 100, 190), (200, 190, 90)])
        for k in range(5):
            a = -math.pi / 2 + (k - 2) * 0.35
            d.line([(x, 640), (x + math.cos(a) * h * 0.6, 640 + math.sin(a) * h)], fill=c, width=9)
    ridge(d, 680, 12, 0.02, 3, (24, 34, 40), jag=4, seed=3)
    return im


def aurora():
    im = grad((4, 10, 24), (12, 28, 44))
    stars(im, 700, 7, 0.7)
    for band, (col, off) in enumerate([((80, 240, 170), 0), ((110, 210, 255), 70), ((180, 130, 255), 140)]):
        layer = Image.new("RGBA", im.size, (0, 0, 0, 0))
        ld = ImageDraw.Draw(layer)
        top, bottom = [], []
        for x in range(0, W + 4, 4):
            y = 200 + off + 70 * math.sin(x * 0.006 + band) + 30 * math.sin(x * 0.017 + band * 2)
            top.append((x, y))
            bottom.append((x, y + 140 - band * 30))
        ld.polygon(top + bottom[::-1], fill=col + (150 - band * 30,))
        ld.line(top, fill=(220, 255, 235, 200 - band * 40), width=4)
        im = Image.alpha_composite(im.convert("RGBA"), layer.filter(ImageFilter.GaussianBlur(16))).convert("RGB")
    d = ImageDraw.Draw(im)
    ridge(d, 560, 70, 0.006, 0.3, (16, 26, 38), jag=10, seed=5)
    ridge(d, 640, 40, 0.009, 2.2, (8, 14, 22), jag=8, seed=6)
    return im


def mountains():
    im = grad((130, 170, 210), (232, 214, 190))
    im = glow(im, (300, 200), 90, (255, 240, 210), 180)
    d = ImageDraw.Draw(im)
    rnd = random.Random(9)
    x, pts = -40, [(-40, H)]
    peaks = []
    while x <= W + 80:
        valley = (x, rnd.randrange(330, 380))
        peak = (x + rnd.randrange(60, 110), rnd.randrange(200, 290))
        pts += [valley, peak]
        peaks.append((valley, peak))
        x = peak[0] + rnd.randrange(60, 110)
    pts += [(W + 80, 360), (W + 80, H)]
    d.polygon(pts, fill=(96, 112, 140))
    for i, (valley, peak) in enumerate(peaks):
        nxt = peaks[i + 1][0] if i + 1 < len(peaks) else (peak[0] + 80, 360)
        f = 0.35
        left = (peak[0] + (valley[0] - peak[0]) * f, peak[1] + (valley[1] - peak[1]) * f)
        right = (peak[0] + (nxt[0] - peak[0]) * f, peak[1] + (nxt[1] - peak[1]) * f)
        d.polygon([left, peak, right, (peak[0], (left[1] + right[1]) / 2 - 6)], fill=(240, 244, 250))
    ridge(d, 470, 40, 0.007, 1.1, (70, 86, 104), jag=12, seed=10)
    ridge(d, 580, 35, 0.009, 2.6, (46, 60, 70), jag=10, seed=11)
    ridge(d, 670, 20, 0.012, 0.4, (30, 40, 44), jag=8, seed=12)
    return im


def library():
    im = grad((14, 18, 30), (32, 26, 22))
    im = glow(im, (640, 520), 260, (227, 178, 82), 110)
    d = ImageDraw.Draw(im, "RGBA")
    # three arches with shelves of books behind them, and a hanging lamp
    for cx in (290, 640, 990):
        w, top = 260, 150
        d.rectangle([cx - w / 2, top + 130, cx + w / 2, H], fill=(24, 22, 26, 255))
        d.pieslice([cx - w / 2, top, cx + w / 2, top + 260], 180, 360, fill=(24, 22, 26, 255))
        rnd = random.Random(cx)
        for shelf in range(5):
            y = top + 150 + shelf * 92
            x = cx - w / 2 + 12
            while x < cx + w / 2 - 16:
                bw = rnd.randrange(8, 18)
                bh = rnd.randrange(52, 78)
                c = rnd.choice([(120, 50, 40), (60, 80, 110), (150, 110, 50), (70, 90, 60), (110, 90, 70)])
                d.rectangle([x, y - bh, x + bw, y], fill=c + (255,))
                x += bw + 2
            d.rectangle([cx - w / 2 + 6, y, cx + w / 2 - 6, y + 6], fill=(90, 66, 40, 255))
        d.arc([cx - w / 2 - 14, top - 14, cx + w / 2 + 14, top + 274], 180, 360, fill=(227, 178, 82, 255), width=5)
        d.line([(cx - w / 2 - 12, top + 130), (cx - w / 2 - 12, H)], fill=(227, 178, 82, 255), width=5)
        d.line([(cx + w / 2 + 12, top + 130), (cx + w / 2 + 12, H)], fill=(227, 178, 82, 255), width=5)
    d.line([(640, 0), (640, 70)], fill=(150, 120, 70, 255), width=3)
    d.polygon([(615, 70), (665, 70), (675, 120), (605, 120)], fill=(200, 150, 70, 255))
    return glow(im, (640, 100), 60, (255, 210, 130), 200)


for name, fn in [("saturn", saturn), ("dunes", dunes), ("reef", reef), ("aurora", aurora),
                 ("mountains", mountains), ("library", library)]:
    fn().save(OUT / f"{name}.jpg", quality=86, optimize=True)
    print(name)
