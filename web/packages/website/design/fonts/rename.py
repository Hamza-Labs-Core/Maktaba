# Renames a subset of IBM Plex Sans Arabic: the OFL's Reserved Font Name ("Plex") may not
# be used by a Modified Version, and a subset is one. With a fourth argument "display", it
# renames a subset of Amiri Bold "Maktaba Display" (Amiri reserves no name; the rename
# keeps the CSS family from matching a copy installed on the visitor's computer).
import sys

from fontTools.ttLib import TTFont

src, dst, style = sys.argv[1], sys.argv[2], sys.argv[3]
display = len(sys.argv) > 4 and sys.argv[4] == "display"
f = TTFont(src)
name = f["name"]
fam = "Maktaba Display" if display else "Maktaba Text"
ps = "MaktabaDisplay" if display else "MaktabaText"
origin = "Amiri 1.002" if display else "IBM Plex Sans Arabic 1.1"
vals = {
    1: fam if style in ("Regular",) else f"{fam} {style}",
    2: "Regular",
    3: f"{ps}-{style};subset of {origin}",
    4: f"{fam} {style}",
    6: f"{ps}-{style}",
    16: fam,
    17: style,
}
for rec in list(name.names):
    if rec.nameID in (1, 2, 3, 4, 6, 16, 17, 18, 21, 22):
        name.removeNames(nameID=rec.nameID)
for nid, v in vals.items():
    name.setName(v, nid, 3, 1, 0x409)
    name.setName(v, nid, 1, 0, 0)
name.setName(
    f"Modified Version of {'Amiri Bold' if display else 'IBM Plex Sans Arabic'} (subset for maktaba.tv), SIL Open Font License 1.1.",
    10,
    3,
    1,
    0x409,
)
f.save(dst)
print(dst)
