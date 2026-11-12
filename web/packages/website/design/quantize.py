"""Shrinks the mockup renders (story 60.01): each PNG to a 256-colour palette, no dithering.

The pages are flat colour and text, so the palette keeps them sharp at about 40% of the
size. Pillow writes no text or time chunk. Run by render.mjs through
`uvx --with pillow python -I quantize.py <dir>`.
"""

import sys
from pathlib import Path

from PIL import Image

for path in sorted(Path(sys.argv[1]).glob("*.png")):
    with Image.open(path) as im:
        q = im.convert("RGB").quantize(
            colors=256, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE
        )
    q.save(path, optimize=True)
