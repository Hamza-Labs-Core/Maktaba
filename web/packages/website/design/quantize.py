"""Shrinks the mockup renders (story 60.01): each PNG to a 256-colour palette, no dithering.

The pages are flat colour and text, so the palette keeps them sharp at about 40% of the
size. Pillow writes no text or time chunk. Run by render.mjs through
`uvx --with pillow python -I quantize.py <dir> [file ...]`: only the files named (the ones
the run rendered), or every PNG in the directory when none is named, so a partial run
never rewrites a committed render it did not make.
"""

import sys
from pathlib import Path

from PIL import Image

root = Path(sys.argv[1])
names = sys.argv[2:]
for path in [root / n for n in names] if names else sorted(root.glob("*.png")):
    with Image.open(path) as im:
        q = im.convert("RGB").quantize(
            colors=256, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE
        )
    q.save(path, optimize=True)
