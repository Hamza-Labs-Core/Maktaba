#!/usr/bin/env python3
"""The -setup.exe's NSIS template (story 13.14) outside a tauri build.

tauri renders apps/desktop/src-tauri/windows/installer.nsi with Handlebars and compiles it
with makensis. This renders it the same way for the few Handlebars forms the template uses
({{x}}, {{#if}}, {{#each ... as |n|}}, {{this.[1]}}, @key and tauri's helpers), with a
server build's or a client build's data, so

  - tools/ci/tests/test_installers.py reads the script tauri would compile (its pages, its
    languages, every string in English and Arabic) on any OS, and
  - `nsis_template.py --makensis PATH` compiles both builds with stand-in files, the way
    test-hooks.sh compiles the hooks (makensis 3.13 runs on Linux and macOS too; the plugin
    nsis_tauri_utils.dll is read, never run: pass its folder with --plugins; tauri's own
    utils.nsh, FileAssociation.nsh and language files come from tauri-bundler's source, --tauri).

The real compile is CI's win-app gate (`make win-app`: tauri renders this template and its
makensis builds the installer).

  nsis_template.py --render server|client OUT.nsi
  nsis_template.py --makensis makensis --tauri BUNDLER_NSIS_DIR --plugins DIR [--keep DIR]
"""

from __future__ import annotations

import argparse
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SRC_TAURI = ROOT / "apps" / "desktop" / "src-tauri"
TEMPLATE = SRC_TAURI / "windows" / "installer.nsi"

_TOKEN = re.compile(r"\{\{(~?)(.*?)(~?)\}\}", re.S)


def _parse(text: str):
    """Handlebars text -> a tree of ("text", s) / ("var", expr) / ("if"|"each", expr, body, else)."""
    pos = 0
    stack: list[tuple[str, str, list, list | None]] = [("root", "", [], None)]

    def body():
        kind, expr, main, alt = stack[-1]
        return alt if alt is not None else main

    for m in _TOKEN.finditer(text):
        chunk = text[pos : m.start()]
        if m.group(1):
            chunk = chunk.rstrip()
        body().append(("text", chunk))
        pos = m.end()
        inner = m.group(2).strip()
        if inner.startswith("#"):
            word, _, expr = inner[1:].partition(" ")
            stack.append((word, expr.strip(), [], None))
        elif inner == "else":
            kind, expr, main, _ = stack.pop()
            stack.append((kind, expr, main, []))
        elif inner.startswith("/"):
            kind, expr, main, alt = stack.pop()
            if kind != inner[1:]:
                raise ValueError(f"{{{{/{inner[1:]}}}}} closes {{{{#{kind}}}}}")
            body().append((kind, expr, main, alt or []))
        else:
            body().append(("var", inner))
        if m.group(3):
            # ~}}: the whitespace after the tag goes
            rest = text[pos:]
            pos += len(rest) - len(rest.lstrip())
    body().append(("text", text[pos:]))
    if len(stack) != 1:
        raise ValueError(f"unclosed {{{{#{stack[-1][0]}}}}}")
    return stack[0][2]


def _lookup(expr: str, scope: list[dict]):
    expr = expr.strip()
    if expr.startswith('"'):
        return expr.strip('"')
    for frame in reversed(scope):
        head, _, rest = expr.partition(".")
        if head in frame:
            value = frame[head]
            for part in filter(None, rest.split(".")):
                if part.startswith("[") and part.endswith("]"):
                    value = value[int(part[1:-1])]
                else:
                    value = value.get(part) if isinstance(value, dict) else None
            return value
    return None


def _escape(s: str) -> str:
    # tauri's escape function (nsis/mod.rs register_escape_fn)
    table = {'"': '$\\"', "$": "$$", "`": "$\\`", "\n": "$\\n", "\t": "$\\t", "\r": "$\\r"}
    return "".join(table.get(c, c) for c in s)


def _eval(expr: str, scope: list[dict]) -> str:
    words = expr.split()
    if words[0] == "no-escape":
        return str(_lookup(words[1], scope) or "")
    if words[0] == "or":
        return _escape(str(_lookup(words[1], scope) or _lookup(words[2], scope) or ""))
    if words[0] == "association-description":
        return _escape(str(_lookup(words[1], scope) or f"{_lookup(words[2], scope)} File"))
    value = _lookup(expr, scope)
    return "" if value is None else _escape(str(value))


def _render(tree, scope: list[dict]) -> str:
    out = []
    for node in tree:
        kind = node[0]
        if kind == "text":
            out.append(node[1])
        elif kind == "var":
            out.append(_eval(node[1], scope))
        elif kind == "if":
            out.append(_render(node[2] if _lookup(node[1], scope) else node[3], scope))
        elif kind == "each":
            m = re.fullmatch(r"(\S+)(?:\s+as\s+\|(\w+)\|)?", node[1])
            items = _lookup(m.group(1), scope) or []
            pairs = items.items() if isinstance(items, dict) else enumerate(items)
            for key, value in pairs:
                frame = {"this": value, "@key": key}
                if m.group(2):
                    frame[m.group(2)] = value
                out.append(_render(node[2], scope + [frame]))
        else:
            raise ValueError(f"no Handlebars block {kind}")
    return "".join(out)


def data(build: str, work: Path) -> dict:
    """The data tauri gives the template, for a server build (the hooks, a payload) or a
    client build (installer-ui.nsh as the hooks, no payload). Files are stand-ins in WORK."""
    exe = work / "Maktaba.exe"
    exe.write_bytes(b"MZ stand-in")
    resources = {}
    if build == "server":
        payload = work / "server" / "payload.json"
        payload.parent.mkdir(parents=True, exist_ok=True)
        payload.write_text("{}\n")
        resources = {str(payload): ["server", "server\\payload.json"]}
        hooks = SRC_TAURI / "windows" / "installer-hooks.nsh"
    else:
        hooks = SRC_TAURI / "windows" / "installer-ui.nsh"
    langs = ["English", "Arabic"]
    return {
        "arch": "x64",
        "bundle_id": "com.hamzalabs.maktaba",
        "manufacturer": "hamzalabs",
        "product_name": "Maktaba",
        "short_description": "Maktaba",
        "copyright": "© 2026 Hamza Labs",
        "version": "0.3.113",
        "version_with_build": "0.3.113.0",
        "install_mode": "currentUser",
        "installer_hooks": str(hooks),
        "languages": langs,
        "display_language_selector": "false",
        "language_files": [str(work / f"{lang}.nsh") for lang in langs],
        "main_binary_name": "Maktaba",
        "main_binary_path": str(exe),
        "out_file": str(work / "setup.exe"),
        "resources_dirs": ["server"] if resources else [],
        "resources_ancestors": ["server"] if resources else [],
        "resources": resources,
        "binaries": {},
        "estimated_size": "1468006" if build == "server" else "12000",
        "deep_link_protocols": ["maktaba"],
        "allow_downgrades": "true",
        "install_webview2_mode": "downloadBootstrapper",
        "webview2_installer_args": "/silent",
        "additional_plugins_path": str(work / "plugins"),
        "compression": "lzma",
    }


def render(build: str, work: Path) -> str:
    return _render(_parse(TEMPLATE.read_text(encoding="utf-8")), [data(build, work)])


def _tauri_files(work: Path, tauri: Path, plugins: Path) -> None:
    """What tauri writes beside the script: utils.nsh, FileAssociation.nsh, its language
    files (from tauri-bundler's src/bundle/windows/nsis, --tauri) and the plugin."""
    for name in ("utils.nsh", "FileAssociation.nsh"):
        shutil.copy(tauri / name, work / name)
    for lang in ("English", "Arabic"):
        shutil.copy(tauri / "languages" / f"{lang}.nsh", work / f"{lang}.nsh")
    (work / "plugins").mkdir(exist_ok=True)
    for dll in plugins.glob("*.dll"):
        shutil.copy(dll, work / "plugins" / dll.name)


# makensis's warnings about what only its Windows build does (File /a keeps file times)
PLATFORM_WARNINGS = {"5050"}


def compile_both(makensis: str, tauri: Path, plugins: Path, keep: Path | None) -> int:
    failed = 0
    for build in ("server", "client"):
        with tempfile.TemporaryDirectory() as tmp:
            work = Path(tmp)
            _tauri_files(work, tauri, plugins)
            script = work / "installer.nsi"
            script.write_text("﻿" + render(build, work), encoding="utf-8")
            res = subprocess.run(
                # as tauri runs it (nsis/mod.rs); -OUTPUTCHARSET is Windows' only
                [makensis, "-INPUTCHARSET", "UTF8", *(["-OUTPUTCHARSET", "UTF8"] if sys.platform == "win32" else []),
                 "-V2", str(script)],
                capture_output=True,
                text=True,
            )
            # every warning is a failure but File /a's, which only Windows' makensis honours
            warnings = [w for w in re.findall(r"^warning (\d+): .*$", res.stdout, re.M) if w not in PLATFORM_WARNINGS]
            lines = [l for l in res.stdout.splitlines() if l.startswith("warning ") and l.split()[1].rstrip(":") not in PLATFORM_WARNINGS]
            print(f"nsis_template: {build}: makensis exit {res.returncode}, {len(warnings)} warnings")
            if res.returncode != 0 or warnings:
                print("\n".join(lines) or res.stdout[-6000:], res.stderr[-3000:], sep="\n")
                failed += 1
            if keep:
                keep.mkdir(parents=True, exist_ok=True)
                shutil.copy(script, keep / f"installer-{build}.nsi")
                if (work / "setup.exe").exists():
                    shutil.copy(work / "setup.exe", keep / f"setup-{build}.exe")
    return failed


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--render", nargs=2, metavar=("BUILD", "OUT"))
    ap.add_argument("--makensis")
    ap.add_argument("--tauri", type=Path, help="tauri-bundler's src/bundle/windows/nsis folder")
    ap.add_argument("--plugins", type=Path, help="a folder holding nsis_tauri_utils.dll")
    ap.add_argument("--keep", type=Path, help="keep the rendered scripts and installers here")
    a = ap.parse_args(argv)
    if a.render:
        build, out = a.render
        with tempfile.TemporaryDirectory() as tmp:
            Path(out).write_text(render(build, Path(tmp)), encoding="utf-8")
        return 0
    if a.makensis:
        if not a.plugins or not a.tauri:
            ap.error("--makensis needs --tauri and --plugins")
        return 1 if compile_both(a.makensis, a.tauri, a.plugins, a.keep) else 0
    ap.print_help()
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
