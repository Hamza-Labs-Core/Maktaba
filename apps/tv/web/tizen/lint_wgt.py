#!/usr/bin/env python3
"""The Samsung package's lint (Epic 58 story 58.10, T03).

    lint_wgt.py maktaba-tizen-0.3.141-unsigned.wgt [--version 0.3.141] [--signed]

Opens a built .wgt and checks its config.xml against what the story lists, exactly:

- the application ID and package, Tizen 5.5 as the oldest (2020 TVs, owner question Q10),
  the TV profile, the version (when given);
- the privileges, no more and no fewer (PRIVILEGES);
- access to any origin with subdomains and navigation anywhere, as the homes' and the relay's
  names are per home and cannot be listed;
- the CSP, directive by directive (CSP): frames and requests over https: and the LAN's http:
  only, scripts from the package alone;
- the start page and the icon exist in the package, index.html loads Samsung's webapis.js
  ahead of the launcher, and no source map ships;
- with --signed: the author and distributor signatures Tizen's CLI adds.

Fails, naming each problem, with exit 1. Standard library only.
"""

from __future__ import annotations

import argparse
import re
import struct
import sys
import xml.etree.ElementTree as ET
import zipfile
from pathlib import Path

W = "{http://www.w3.org/ns/widgets}"
TZ = "{http://tizen.org/ns/widgets}"

APP_ID = "HLMaktaba1.Maktaba"
PACKAGE = "HLMaktaba1"
REQUIRED_VERSION = "5.5"
PROFILE = "tv-samsung"
ICON_SIZE = (512, 423)

PRIVILEGES = {
    "http://tizen.org/privilege/internet",
    "http://tizen.org/privilege/tv.inputdevice",
    "http://tizen.org/privilege/application.launch",
    "http://tizen.org/privilege/network.get",
    "http://tizen.org/privilege/tv.display",
    # webapis.productinfo (the model, the UHD panel) and webapis.network (the TV's address)
    "http://developer.samsung.com/privilege/productinfo",
    "http://developer.samsung.com/privilege/network.public",
}

CSP = {
    "default-src": ["'self'"],
    "script-src": ["'self'", "file:"],
    "style-src": ["'self'", "'unsafe-inline'"],
    "font-src": ["'self'", "data:"],
    "img-src": ["'self'", "data:", "https:", "http:"],
    "connect-src": ["https:", "http:"],
    "frame-src": ["https:", "http:"],
    "media-src": ["https:", "http:", "blob:"],
    "object-src": ["'self'"],
}

WEBAPIS = "$WEBAPIS/webapis/webapis.js"


def parse_csp(text: str) -> dict[str, list[str]]:
    out: dict[str, list[str]] = {}
    for part in text.split(";"):
        words = part.split()
        if words:
            out[words[0]] = words[1:]
    return out


def png_size(data: bytes) -> tuple[int, int] | None:
    if data[:8] != b"\x89PNG\r\n\x1a\n" or data[12:16] != b"IHDR":
        return None
    w, h = struct.unpack(">II", data[16:24])
    return (w, h)


def lint(wgt: Path, version: str | None = None, signed: bool = False) -> list[str]:
    """The problems of a built .wgt ([] when it passes)."""
    problems: list[str] = []
    try:
        z = zipfile.ZipFile(wgt)
    except (OSError, zipfile.BadZipFile) as e:
        return [f"{wgt}: not a zip ({e})"]
    with z:
        names = set(z.namelist())
        if "config.xml" not in names:
            return ["config.xml: missing"]
        try:
            root = ET.fromstring(z.read("config.xml"))
        except ET.ParseError as e:
            return [f"config.xml: does not parse ({e})"]
        if root.tag != f"{W}widget":
            problems.append(f"config.xml: the root is {root.tag}, not a W3C widget")
        v = root.get("version", "")
        if not re.fullmatch(r"\d{1,3}\.\d{1,3}\.\d{1,5}", v):
            problems.append(f"version: {v!r} is not X.Y.Z")
        elif version is not None and v != version:
            problems.append(f"version: {v} (want {version})")

        app = root.find(f"{TZ}application")
        if app is None:
            problems.append("tizen:application: missing")
        else:
            for attr, want in (("id", APP_ID), ("package", PACKAGE), ("required_version", REQUIRED_VERSION)):
                if app.get(attr) != want:
                    problems.append(f"tizen:application {attr}: {app.get(attr)!r} (want {want!r})")
        prof = root.find(f"{TZ}profile")
        if prof is None or prof.get("name") != PROFILE:
            problems.append(f"tizen:profile: want {PROFILE!r}")

        privs = [p.get("name", "") for p in root.findall(f"{TZ}privilege")]
        for extra in sorted(set(privs) - PRIVILEGES):
            problems.append(f"privilege not listed: {extra}")
        for missing in sorted(PRIVILEGES - set(privs)):
            problems.append(f"privilege missing: {missing}")
        if len(privs) != len(set(privs)):
            problems.append("privileges: one is listed twice")

        access = [(a.get("origin"), a.get("subdomains")) for a in root.findall(f"{W}access")]
        if access != [("*", "true")]:
            problems.append(f"access: {access} (want exactly one: origin * with subdomains)")
        nav = [(n.text or "").strip() for n in root.findall(f"{TZ}allow-navigation")]
        if nav != ["*"]:
            problems.append(f"tizen:allow-navigation: {nav} (want *)")

        csps = root.findall(f"{TZ}content-security-policy")
        if len(csps) != 1:
            problems.append(f"tizen:content-security-policy: {len(csps)} (want one)")
        else:
            got = parse_csp(csps[0].text or "")
            for d in sorted(set(got) | set(CSP)):
                if got.get(d) != CSP.get(d):
                    problems.append(f"CSP {d}: {got.get(d)} (want {CSP.get(d)})")

        content = root.find(f"{W}content")
        start = content.get("src") if content is not None else None
        if start != "index.html":
            problems.append(f"content src: {start!r} (want index.html)")
        elif "index.html" not in names:
            problems.append("index.html: missing from the package")
        else:
            html = z.read("index.html").decode("utf-8", "replace")
            w = html.find(WEBAPIS)
            first = re.search(r"<script\b(?![^>]*webapis)", html)
            if w < 0:
                problems.append("index.html: does not load webapis.js")
            elif first and first.start() < w:
                problems.append("index.html: webapis.js loads after the launcher's script")
            for ref in re.findall(r'(?:src|href)="\./([^"]+)"', html):
                if ref not in names:
                    problems.append(f"index.html: {ref} is not in the package")

        icon = root.find(f"{W}icon")
        isrc = icon.get("src") if icon is not None else None
        if not isrc or isrc not in names:
            problems.append(f"icon: {isrc!r} missing from the package")
        else:
            size = png_size(z.read(isrc))
            if size != ICON_SIZE:
                problems.append(f"icon: {size} (want a {ICON_SIZE[0]}x{ICON_SIZE[1]} PNG)")

        for n in sorted(names):
            if n.endswith(".map"):
                problems.append(f"{n}: a source map in the package")
        if signed:
            for sig in ("author-signature.xml", "signature1.xml"):
                if sig not in names:
                    problems.append(f"{sig}: missing (the package is not signed)")
    return problems


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("wgt", type=Path)
    ap.add_argument("--version")
    ap.add_argument("--signed", action="store_true", help="also require the signatures")
    a = ap.parse_args(argv)
    problems = lint(a.wgt, a.version, a.signed)
    for p in problems:
        print(f"lint_wgt: {a.wgt.name}: {p}", file=sys.stderr)
    if problems:
        return 1
    print(f"lint_wgt: {a.wgt.name}: config.xml, privileges, CSP, access and files as listed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
