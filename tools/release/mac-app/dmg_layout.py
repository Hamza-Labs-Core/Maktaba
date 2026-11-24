"""The .dmg's Finder window (Epic 13 story 13.14, decision D3): its background, its size, where
the app and the Applications link stand, written into the mounted image's .DS_Store without
Finder. dmg-layout.sh mounts the image and runs this with the two pinned wheels that write a
.DS_Store and the background's alias (ds_store and mac_alias, the libraries dmgbuild writes
them with).

tauri's dmg script lays the window out by asking Finder over AppleScript, which needs a GUI
session and hangs without one, so tauri-build.sh runs it with CI=true and the layout is
skipped; this writes the same records Finder would.

  python3 -I dmg_layout.py write|check MOUNT CONFIG WHEEL...

CONFIG is apps/desktop/src-tauri/tauri.conf.json: bundle.macOS.dmg's background, windowSize,
appPosition and applicationFolderPosition, read here so the window and the art agree.
"""

from __future__ import annotations

import json
import os
import sys

ICON_SIZE = 128.0
TEXT_SIZE = 13.0
WINDOW_ORIGIN = (200, 120)


def layout(config_path: str) -> dict:
    conf = json.load(open(config_path, encoding="utf-8"))
    dmg = conf["bundle"]["macOS"]["dmg"]
    product = conf["productName"]
    return {
        # tauri names the image's volume after the product (bundle_dmg --volname)
        "volume": product,
        "background": os.path.basename(dmg["background"]),
        "size": (dmg["windowSize"]["width"], dmg["windowSize"]["height"]),
        "icons": {
            f"{product}.app": (dmg["appPosition"]["x"], dmg["appPosition"]["y"]),
            "Applications": (dmg["applicationFolderPosition"]["x"], dmg["applicationFolderPosition"]["y"]),
        },
    }


def write(mount: str, want: dict) -> None:
    from ds_store import DSStore
    from mac_alias import Alias

    background = os.path.join(mount, ".background", want["background"])
    if not os.path.isfile(background):
        raise SystemExit(f"dmg_layout: no {background} (tauri copies bundle.macOS.dmg.background there)")
    alias = Alias.for_file(background)
    w, h = want["size"]
    x, y = WINDOW_ORIGIN
    with DSStore.open(os.path.join(mount, ".DS_Store"), "w+") as d:
        d["."]["vSrn"] = ("long", 1)
        d["."]["vstl"] = ("type", b"icnv")
        d["."]["bwsp"] = {
            "ShowStatusBar": False,
            "ShowToolbar": False,
            "ShowTabView": False,
            "ShowPathbar": False,
            "ShowSidebar": False,
            "ContainerShowSidebar": False,
            "PreviewPaneVisibility": False,
            "SidebarWidth": 0,
            "WindowBounds": f"{{{{{x}, {y}}}, {{{w}, {h}}}}}",
        }
        d["."]["icvp"] = {
            "viewOptionsVersion": 1,
            "backgroundType": 2,
            "backgroundImageAlias": alias.to_bytes(),
            "backgroundColorRed": 1.0,
            "backgroundColorGreen": 1.0,
            "backgroundColorBlue": 1.0,
            "gridOffsetX": 0.0,
            "gridOffsetY": 0.0,
            "gridSpacing": 100.0,
            "arrangeBy": "none",
            "showIconPreview": True,
            "showItemInfo": False,
            "labelOnBottom": True,
            "textSize": TEXT_SIZE,
            "iconSize": ICON_SIZE,
            "scrollPositionX": 0.0,
            "scrollPositionY": 0.0,
        }
        for name, (ix, iy) in want["icons"].items():
            d[name]["Iloc"] = (ix, iy)


def alias_problems(alias, mount: str, want: dict) -> list[str]:
    """Whether the background's alias resolves to the background in this image: its volume's
    name, its path within the volume, and the file's and its folder's catalog ids (an HFS+
    image keeps them from one mount to the next)."""
    problems = []
    background = os.path.join(mount, ".background", want["background"])
    target, volume = alias.target, alias.volume
    if volume.name != want["volume"]:
        problems.append(f"the background's alias is on the volume {volume.name!r}, not {want['volume']!r}")
    path = (target.posix_path or "").lstrip("/")
    if path != f".background/{want['background']}":
        problems.append(f"the background's alias names {path!r} in its volume, not .background/{want['background']}")
    if target.cnid != os.stat(background).st_ino:
        problems.append(f"the background's alias names file id {target.cnid}, not the background's {os.stat(background).st_ino}")
    if target.folder_cnid != os.stat(os.path.dirname(background)).st_ino:
        problems.append("the background's alias names another folder than .background")
    return problems


def check(mount: str, want: dict) -> list[str]:
    from ds_store import DSStore
    from mac_alias import Alias

    problems = []
    store = os.path.join(mount, ".DS_Store")
    background = os.path.join(mount, ".background", want["background"])
    if not os.path.isfile(background):
        return [f"no .background/{want['background']}"]
    if not os.path.isfile(store):
        return ["no .DS_Store: the window has no layout"]
    with DSStore.open(store, "r") as d:
        icvp = d["."]["icvp"]
        if icvp.get("backgroundType") != 2:
            problems.append(f"the window's background is not a picture (backgroundType {icvp.get('backgroundType')})")
        else:
            problems += alias_problems(Alias.from_bytes(icvp["backgroundImageAlias"]), mount, want)
        if icvp.get("iconSize") != ICON_SIZE:
            problems.append(f"icon size {icvp.get('iconSize')}, not {ICON_SIZE}")
        bounds = d["."]["bwsp"].get("WindowBounds", "")
        w, h = want["size"]
        if not bounds.endswith(f"{{{w}, {h}}}}}"):
            problems.append(f"the window is {bounds}, not {w} x {h}")
        for name, pos in want["icons"].items():
            if not os.path.lexists(os.path.join(mount, name)):
                problems.append(f"no {name} in the image")
            try:
                got = tuple(d[name]["Iloc"])
            except KeyError:
                got = None
            if got != pos:
                problems.append(f"{name} stands at {got}, not {pos}")
    return problems


def main(argv: list[str]) -> int:
    if len(argv) < 4 or argv[0] not in ("write", "check"):
        print(__doc__, file=sys.stderr)
        return 2
    mode, mount, config, wheels = argv[0], argv[1], argv[2], argv[3:]
    sys.path[:0] = wheels  # pure-Python wheels import from the zip
    want = layout(config)
    if mode == "write":
        write(mount, want)
        print(f"dmg_layout: {want['size'][0]} x {want['size'][1]} window, background {want['background']}, " + ", ".join(f"{n} at {p}" for n, p in want["icons"].items()))
        return 0
    problems = check(mount, want)
    for p in problems:
        print(f"dmg_layout: {p}", file=sys.stderr)
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
