"""Cut the Maktaba app icon into every size the apps ship.

    uv run --no-project --with pillow python tools/icons/make-icons.py

Reads tools/icons/source/maktaba-icon.png: a square, full-bleed master
(the gold mark on the navy ground, at least 1024 px) generated on Magnific
with the owner's account. Every file below is derived from it, so a new
icon is a new master plus one run of this script. Needs Pillow and, for
the .icns, macOS `iconutil`.
"""

from __future__ import annotations

import json
import pathlib
import shutil
import subprocess
import tempfile

from PIL import Image, ImageChops, ImageDraw, ImageFilter, ImageFont

ROOT = pathlib.Path(__file__).resolve().parents[2]
SOURCE = ROOT / "tools/icons/source/maktaba-icon.png"
NAVY = (11, 17, 28)  # #0B111C, the dark theme's ground
GOLD = (227, 178, 82)  # #E3B252, the brand gold
AMIRI = ROOT / "apps/tv/tvos/MaktabaTV/Resources/Fonts/Amiri-Bold.ttf"

DESKTOP = ROOT / "apps/desktop/src-tauri/icons"
# the desktop Player for Windows and Linux (Epic 42.4): the same .ico and a 256 px PNG
DESKTOP_PLAYER = ROOT / "apps/desktop-player/icons"
WEB = ROOT / "web/public/icons"
WEB_ROOT = ROOT / "web/public"
TV_ANDROID = ROOT / "apps/tv/android/app/src/main/res"
TV_OS = ROOT / "apps/tv/tvos/MaktabaTV/Resources/Assets.xcassets/AppIcon.brandassets"
MOBILE = ROOT / "apps/mobile/icons"
ANDROID_NATIVE = ROOT / "apps/android/core/src/main/res"
LIBRARY_ART = ROOT / "web/public/art/library/dark-wide-1.jpg"

# Android density buckets: launcher icons are 48 dp, adaptive layers 108 dp.
DENSITIES = {"mdpi": 1, "hdpi": 1.5, "xhdpi": 2, "xxhdpi": 3, "xxxhdpi": 4}


def load_master() -> Image.Image:
    im = Image.open(SOURCE).convert("RGB")
    side = min(im.size)
    left, top = (im.width - side) // 2, (im.height - side) // 2
    return im.crop((left, top, left + side, top + side)).resize(
        (1024, 1024), Image.LANCZOS
    )


def rounded_mask(size: int, radius: float, scale: int = 4) -> Image.Image:
    """An anti-aliased rounded-square mask (drawn large, then scaled down)."""
    big = Image.new("L", (size * scale, size * scale), 0)
    ImageDraw.Draw(big).rounded_rectangle(
        (0, 0, size * scale - 1, size * scale - 1), radius=radius * scale, fill=255
    )
    return big.resize((size, size), Image.LANCZOS)


def mark_layer(master: Image.Image) -> Image.Image:
    """The mark alone on transparency: key out the navy ground by colour distance.

    Glow fades to partial alpha, so the mark sits on any background (Android
    adaptive foreground, the tvOS parallax front layer).
    """
    bg = Image.new("RGB", master.size, master.getpixel((8, 8)))
    diff = ImageChops.difference(master, bg).convert("L")
    alpha = diff.point(lambda v: min(255, v * 3))
    out = master.convert("RGBA")
    out.putalpha(alpha)
    return out


def macos_icon(master: Image.Image) -> Image.Image:
    """Apple's macOS grid: an 824 px rounded square centred on a 1024 canvas, with a soft shadow."""
    canvas = Image.new("RGBA", (1024, 1024), (0, 0, 0, 0))
    body, off, radius = 824, 100, 185.4
    mask = rounded_mask(body, radius)
    shadow = Image.new("RGBA", (1024, 1024), (0, 0, 0, 0))
    shadow_alpha = Image.new("L", (1024, 1024), 0)
    shadow_alpha.paste(mask.point(lambda v: v * 0.45), (off, off + 12))
    shadow.putalpha(shadow_alpha.filter(ImageFilter.GaussianBlur(14)))
    canvas.alpha_composite(shadow)
    tile = master.resize((body, body), Image.LANCZOS).convert("RGBA")
    tile.putalpha(mask)
    canvas.alpha_composite(tile, (off, off))
    return canvas


def full_tile(
    master: Image.Image, size: int, margin: float = 0.03, radius: float = 0.2
) -> Image.Image:
    """A rounded square that nearly fills the canvas (Windows, Linux, web 'any')."""
    canvas = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    inner = round(size * (1 - 2 * margin))
    tile = master.resize((inner, inner), Image.LANCZOS).convert("RGBA")
    tile.putalpha(rounded_mask(inner, inner * radius))
    off = (size - inner) // 2
    canvas.alpha_composite(tile, (off, off))
    return canvas


def zoomed(master: Image.Image, zoom: float) -> Image.Image:
    """The master cropped in on its centre, so tiny sizes show the mark, not the margin."""
    side = round(1024 / zoom)
    off = (1024 - side) // 2
    return master.crop((off, off, off + side, off + side)).resize(
        (1024, 1024), Image.LANCZOS
    )


def sized(im: Image.Image, size: int) -> Image.Image:
    return im.resize((size, size), Image.LANCZOS)


def on_ground(
    mark: Image.Image, w: int, h: int, mark_h: float, ground: Image.Image | None = None
) -> Image.Image:
    """The keyed mark centred on a navy (or given) ground of w x h."""
    canvas = (ground.copy() if ground else Image.new("RGB", (w, h), NAVY)).convert(
        "RGBA"
    )
    side = round(h * mark_h)
    canvas.alpha_composite(sized(mark, side), ((w - side) // 2, (h - side) // 2))
    return canvas


def desktop(master: Image.Image) -> None:
    mac = macos_icon(master)
    small = zoomed(master, 1.18)
    with tempfile.TemporaryDirectory() as tmp:
        iconset = pathlib.Path(tmp) / "icon.iconset"
        iconset.mkdir()
        for pt in (16, 32, 128, 256, 512):
            for scale in (1, 2):
                px = pt * scale
                src = macos_icon(small) if px <= 32 else mac
                name = f"icon_{pt}x{pt}{'@2x' if scale == 2 else ''}.png"
                sized(src, px).save(iconset / name)
        subprocess.run(
            ["iconutil", "-c", "icns", str(iconset), "-o", str(DESKTOP / "icon.icns")],
            check=True,
        )
    ico_sizes = [16, 24, 32, 48, 64, 128, 256]
    big = full_tile(master, 256)
    big.save(
        DESKTOP / "icon.ico",
        sizes=[(s, s) for s in ico_sizes],
        append_images=[full_tile(small if s <= 32 else master, s) for s in ico_sizes],
    )
    full_tile(small, 32).save(DESKTOP / "32x32.png")
    full_tile(master, 128).save(DESKTOP / "128x128.png")
    full_tile(master, 256).save(DESKTOP / "128x128@2x.png")
    DESKTOP_PLAYER.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(DESKTOP / "icon.ico", DESKTOP_PLAYER / "maktaba-player.ico")
    full_tile(master, 256).save(DESKTOP_PLAYER / "maktaba-player.png")


def web(master: Image.Image, mark: Image.Image) -> None:
    WEB.mkdir(parents=True, exist_ok=True)
    small = zoomed(master, 1.18)
    full_tile(small, 32).save(WEB / "favicon-32.png")
    for s in (192, 512):
        full_tile(master, s).save(WEB / f"icon-{s}.png")
    # Maskable and Apple touch icons are square and opaque: the platform crops them.
    # A maskable icon may be cut to the inner 80% circle, so the mark sits smaller.
    on_ground(mark, 512, 512, 0.8).convert("RGB").save(WEB / "maskable-512.png")
    sized(master, 180).save(WEB / "apple-touch-icon.png")
    full_tile(small, 48).save(
        WEB_ROOT / "favicon.ico", sizes=[(16, 16), (32, 32), (48, 48)]
    )


def tv_android(master: Image.Image, mark: Image.Image) -> None:
    for bucket, scale in DENSITIES.items():
        d = TV_ANDROID / f"mipmap-{bucket}"
        d.mkdir(parents=True, exist_ok=True)
        full_tile(master, round(48 * scale), margin=0.04).save(d / "ic_launcher.png")
    # The leanback banner: 320x180 dp, drawn at xhdpi (640x360), mark and name.
    banner = Image.new("RGBA", (640, 360), NAVY + (255,))
    # Mark and name as one group, centred, inside the 5% side margins.
    draw = ImageDraw.Draw(banner)
    font = ImageFont.truetype(str(AMIRI), 84)
    box = draw.textbbox((0, 0), "Maktaba", font=font)
    side, gap = 230, 20
    left = (640 - (side + gap + box[2] - box[0])) // 2
    banner.alpha_composite(sized(mark, side), (left, (360 - side) // 2))
    draw.text(
        (left + side + gap - box[0], (360 - (box[3] - box[1])) // 2 - box[1]),
        "Maktaba",
        font=font,
        fill=GOLD,
    )
    d = TV_ANDROID / "drawable-xhdpi"
    d.mkdir(parents=True, exist_ok=True)
    banner.convert("RGB").save(d / "app_banner.png")


def imageset(path: pathlib.Path, images: dict[str, Image.Image]) -> None:
    """Write an .imageset with one image per scale (keys are '1x' / '2x')."""
    path.mkdir(parents=True, exist_ok=True)
    entries = []
    for scale, im in images.items():
        name = f"{path.stem}@{scale}.png"
        im.save(path / name)
        entries.append({"filename": name, "idiom": "tv", "scale": scale})
    write_json(
        path / "Contents.json",
        {"images": entries, "info": {"author": "xcode", "version": 1}},
    )


def write_json(path: pathlib.Path, obj: object) -> None:
    path.write_text(json.dumps(obj, indent=2) + "\n")


def imagestack(
    path: pathlib.Path, w: int, h: int, mark: Image.Image, scales: tuple[int, ...]
) -> None:
    """A two-layer tvOS parallax icon: the navy ground behind, the mark in front."""
    if path.exists():
        shutil.rmtree(path)
    path.mkdir(parents=True)
    layers = {"Back": None, "Front": mark}
    for layer, content in layers.items():
        lp = path / f"{layer}.imagestacklayer"
        lp.mkdir()
        write_json(lp / "Contents.json", {"info": {"author": "xcode", "version": 1}})
        images = {}
        for s in scales:
            if content is None:
                im = Image.new("RGB", (w * s, h * s), NAVY)
                glow = Image.new("L", im.size, 0)
                cx, cy, r = w * s // 2, h * s // 2, h * s // 2
                ImageDraw.Draw(glow).ellipse((cx - r, cy - r, cx + r, cy + r), fill=60)
                gold = Image.new("RGB", im.size, GOLD)
                im = Image.composite(
                    gold, im, glow.filter(ImageFilter.GaussianBlur(h * s // 6))
                )
            else:
                im = Image.new("RGBA", (w * s, h * s), (0, 0, 0, 0))
                side = round(h * s * 0.8)
                im.alpha_composite(
                    sized(content, side), ((w * s - side) // 2, (h * s - side) // 2)
                )
            images[f"{s}x"] = im
        imageset(lp / "Content.imageset", images)
    write_json(
        path / "Contents.json",
        {
            "layers": [{"filename": f"{n}.imagestacklayer"} for n in ("Front", "Back")],
            "info": {"author": "xcode", "version": 1},
        },
    )


def top_shelf(
    mark: Image.Image, w: int, scales: tuple[int, ...]
) -> dict[str, Image.Image]:
    """The library art behind the mark and name, for the tvOS top shelf."""
    art = Image.open(LIBRARY_ART).convert("RGB")
    out = {}
    for s in scales:
        W, H = w * s, 720 * s
        k = max(W / art.width, H / art.height)
        ground = art.resize(
            (round(art.width * k), round(art.height * k)), Image.LANCZOS
        )
        left, top = (ground.width - W) // 2, round((ground.height - H) * 0.35)
        ground = ground.crop((left, top, left + W, top + H))
        ground = Image.blend(ground, Image.new("RGB", ground.size, NAVY), 0.35)
        out[f"{s}x"] = on_ground(mark, W, H, 0.55, ground).convert("RGB")
    return out


def tv_os(mark: Image.Image) -> None:
    imagestack(TV_OS / "App Icon.imagestack", 400, 240, mark, (1, 2))
    imagestack(TV_OS / "App Icon - App Store.imagestack", 1280, 768, mark, (1,))
    for name, w in (("Top Shelf Image", 1920), ("Top Shelf Image Wide", 2320)):
        p = TV_OS / f"{name}.imageset"
        if p.exists():
            shutil.rmtree(p)
        imageset(p, top_shelf(mark, w, (1, 2)))
    assets = [
        {
            "filename": "App Icon - App Store.imagestack",
            "idiom": "tv",
            "role": "primary-app-icon",
            "size": "1280x768",
        },
        {
            "filename": "App Icon.imagestack",
            "idiom": "tv",
            "role": "primary-app-icon",
            "size": "400x240",
        },
        {
            "filename": "Top Shelf Image Wide.imageset",
            "idiom": "tv",
            "role": "top-shelf-image-wide",
            "size": "2320x720",
        },
        {
            "filename": "Top Shelf Image.imageset",
            "idiom": "tv",
            "role": "top-shelf-image",
            "size": "1920x720",
        },
    ]
    write_json(
        TV_OS / "Contents.json",
        {"assets": assets, "info": {"author": "xcode", "version": 1}},
    )


def mobile(master: Image.Image, mark: Image.Image) -> None:
    """Prebuilt icons the mobile build copies over Capacitor's defaults after `cap add`."""
    res = MOBILE / "android-res"
    for bucket, scale in DENSITIES.items():
        d = res / f"mipmap-{bucket}"
        d.mkdir(parents=True, exist_ok=True)
        full_tile(master, round(48 * scale), margin=0.04).save(d / "ic_launcher.png")
        circle = Image.new("RGBA", (round(48 * scale),) * 2, (0, 0, 0, 0))
        circle.alpha_composite(sized(master, circle.width).convert("RGBA"))
        m = Image.new("L", circle.size, 0)
        ImageDraw.Draw(m).ellipse((0, 0, circle.width - 1, circle.height - 1), fill=255)
        circle.putalpha(m)
        circle.save(d / "ic_launcher_round.png")
        # Adaptive foreground: 108 dp, the mark inside the 66 dp safe circle.
        fg_side = round(108 * scale)
        fg = Image.new("RGBA", (fg_side, fg_side), (0, 0, 0, 0))
        mk = round(fg_side * 0.62)
        fg.alpha_composite(sized(mark, mk), ((fg_side - mk) // 2, (fg_side - mk) // 2))
        fg.save(d / "ic_launcher_foreground.png")
    values = res / "values"
    values.mkdir(parents=True, exist_ok=True)
    (values / "ic_launcher_background.xml").write_text(
        '<?xml version="1.0" encoding="utf-8"?>\n<resources>\n'
        '    <color name="ic_launcher_background">#0B111C</color>\n</resources>\n'
    )
    ios = MOBILE / "ios"
    ios.mkdir(parents=True, exist_ok=True)
    master.save(ios / "AppIcon-512@2x.png")  # opaque 1024: the App Store rejects alpha


def android_native() -> None:
    """The native Android apps (Player and Remote) share the phone icons, from their core: the
    adaptive icon's layers only (minSdk 26 always takes the adaptive icon)."""
    src = MOBILE / "android-res"
    for d in src.glob("mipmap-*"):
        out = ANDROID_NATIVE / d.name
        out.mkdir(parents=True, exist_ok=True)
        shutil.copy2(d / "ic_launcher_foreground.png", out / "ic_launcher_foreground.png")
    (ANDROID_NATIVE / "values").mkdir(parents=True, exist_ok=True)
    shutil.copy2(src / "values" / "ic_launcher_background.xml", ANDROID_NATIVE / "values" / "ic_launcher_background.xml")


def main() -> None:
    master = load_master()
    mark = mark_layer(master)
    desktop(master)
    web(master, mark)
    tv_android(master, mark)
    tv_os(mark)
    mobile(master, mark)
    android_native()
    print("icons written from", SOURCE.relative_to(ROOT))


if __name__ == "__main__":
    main()
