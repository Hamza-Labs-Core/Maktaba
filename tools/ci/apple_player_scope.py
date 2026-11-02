#!/usr/bin/env python3
"""Whether a change to the Apple project file touches the Player (ci.yml's changes job).

The Player's UI tests (nine macOS jobs) run when a file the Player's build or tests read
changes (.github/ci-paths.yml, filter `apple_player_ui`). One file every app shares is
apps/apple/Maktaba.xcodeproj/project.pbxproj: renaming the Remote changes it, and so does
any Player change. This reads the file at both commits, finds the objects that changed, and
says whether one belongs to the Player:

- an object a Player target reaches (its build phases, files, build settings, dependencies,
  the targets it depends on, MaktabaKit's package reference), at either commit;
- the project object, its build settings or its package references (every target's);
- a group holding one of the Player's files (a group's path decides where they are);
- the file's header (archive or object version).

An object only another target reaches (the Remote's settings, the TV app's files) is not
the Player's. project.yml is not read: the builds use the checked-in project file, which
xcodegen writes from it.

    apple_player_scope.py [--base REV] [--head REV]   # default HEAD^1 and HEAD

Prints `player_ui=true|false` (to $GITHUB_OUTPUT too, when set) and why. Stdlib only: it
runs before anything is installed.
"""

from __future__ import annotations

import argparse
import os
import subprocess
import sys
from collections.abc import Iterable, Iterator

PBXPROJ = "apps/apple/Maktaba.xcodeproj/project.pbxproj"
PLAYER_PREFIX = "Maktaba Player"
GROUP_ISAS = {"PBXGroup", "PBXVariantGroup", "XCVersionGroup"}

Value = str | list["Value"] | dict[str, "Value"]

_BARE = set("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_$./:-+")


class ParseError(ValueError):
    pass


class _Parser:
    """An OpenStep property list (the project file's format): dicts, arrays, strings."""

    def __init__(self, text: str) -> None:
        self.s = text
        self.i = 0

    def _skip(self) -> None:
        s, n = self.s, len(self.s)
        while self.i < n:
            c = s[self.i]
            if c in " \t\r\n":
                self.i += 1
            elif s.startswith("/*", self.i):
                end = s.find("*/", self.i + 2)
                if end < 0:
                    raise ParseError("unterminated comment")
                self.i = end + 2
            elif s.startswith("//", self.i):
                end = s.find("\n", self.i)
                self.i = n if end < 0 else end + 1
            else:
                return

    def _expect(self, c: str) -> None:
        self._skip()
        if self.s[self.i : self.i + 1] != c:
            raise ParseError(f"expected {c!r} at {self.i}")
        self.i += 1

    def value(self) -> Value:
        self._skip()
        c = self.s[self.i : self.i + 1]
        if c == "{":
            self.i += 1
            d: dict[str, Value] = {}
            while True:
                self._skip()
                if self.s[self.i : self.i + 1] == "}":
                    self.i += 1
                    return d
                key = self.value()
                if not isinstance(key, str):
                    raise ParseError(f"a key that is not a string at {self.i}")
                self._expect("=")
                d[key] = self.value()
                self._expect(";")
        if c == "(":
            self.i += 1
            items: list[Value] = []
            while True:
                self._skip()
                if self.s[self.i : self.i + 1] == ")":
                    self.i += 1
                    return items
                items.append(self.value())
                self._skip()
                if self.s[self.i : self.i + 1] == ",":
                    self.i += 1
        if c == '"':
            self.i += 1
            out: list[str] = []
            while True:
                ch = self.s[self.i : self.i + 1]
                if ch == "":
                    raise ParseError("unterminated string")
                if ch == '"':
                    self.i += 1
                    return "".join(out)
                if ch == "\\":
                    out.append(self.s[self.i : self.i + 2])
                    self.i += 2
                    continue
                out.append(ch)
                self.i += 1
        start = self.i
        while self.i < len(self.s) and self.s[self.i] in _BARE:
            self.i += 1
        if self.i == start:
            raise ParseError(f"unexpected {c!r} at {self.i}")
        return self.s[start : self.i]


def parse(text: str) -> dict[str, Value]:
    p = _Parser(text)
    v = p.value()
    if not isinstance(v, dict) or not isinstance(v.get("objects"), dict):
        raise ParseError("not a project file (no objects)")
    return v


def _strings(v: Value) -> Iterator[str]:
    if isinstance(v, str):
        yield v
    elif isinstance(v, list):
        for x in v:
            yield from _strings(x)
    else:
        for x in v.values():
            yield from _strings(x)


def _obj(objects: dict[str, Value], oid: str) -> dict[str, Value]:
    o = objects.get(oid)
    return o if isinstance(o, dict) else {}


def _is_target(o: Value | None) -> bool:
    return isinstance(o, dict) and str(o.get("isa", "")).endswith("Target")


def _reach(objects: dict[str, Value], start: Iterable[str], stop: set[str]) -> set[str]:
    """Every object reachable from start, not through the objects of stop."""
    seen: set[str] = set()
    todo = [s for s in start if s in objects]
    while todo:
        oid = todo.pop()
        if oid in seen or oid in stop:
            continue
        seen.add(oid)
        todo.extend(s for s in _strings(objects[oid]) if s in objects and s not in seen)
    return seen


def _root(project: dict[str, Value]) -> str:
    r = project.get("rootObject")
    return r if isinstance(r, str) else ""


def _player_targets(objects: dict[str, Value]) -> list[str]:
    return [
        oid
        for oid, o in objects.items()
        if _is_target(o) and isinstance(o, dict) and str(o.get("name", "")).startswith(PLAYER_PREFIX)
    ]


def project_object(project: dict[str, Value]) -> dict[str, Value]:
    """The project object as the Player sees it: without its list of targets and without
    the other targets' attributes (adding or renaming the Remote changes those)."""
    objects = project["objects"]
    assert isinstance(objects, dict)
    proj = dict(_obj(objects, _root(project)))
    proj.pop("targets", None)
    attrs = proj.get("attributes")
    if isinstance(attrs, dict) and isinstance(attrs.get("TargetAttributes"), dict):
        mine = set(_player_targets(objects))
        ta = attrs["TargetAttributes"]
        assert isinstance(ta, dict)
        proj["attributes"] = {**attrs, "TargetAttributes": {k: v for k, v in ta.items() if k in mine}}
    return proj


def player_objects(project: dict[str, Value]) -> set[str]:
    """The objects the Player's targets reach, those the project object holds for every
    target (its build settings, its packages), and the groups holding one of them."""
    objects = project["objects"]
    assert isinstance(objects, dict)
    root = _root(project)
    mine = _reach(objects, _player_targets(objects), {root})
    # the project's own: not its targets, nor its groups (those are checked below)
    proj = project_object(project)
    shared = [v for k, v in proj.items() if k not in ("mainGroup", "productRefGroup", "attributes")]
    others = {oid for oid, o in objects.items() if _is_target(o)}
    mine |= _reach(objects, (s for v in shared for s in _strings(v)), {root} | others)
    # a group holding one of mine (its path places them)
    parent: dict[str, str] = {}
    for oid, o in objects.items():
        if isinstance(o, dict) and o.get("isa") in GROUP_ISAS:
            for child in _strings(o.get("children", [])):
                parent[child] = oid
    for oid in list(mine):
        p = parent.get(oid)
        while p is not None and p not in mine:
            mine.add(p)
            p = parent.get(p)
    return mine


def touches_player(base_text: str | None, head_text: str | None) -> tuple[bool, str]:
    if base_text == head_text:
        return False, "the project file did not change"
    if base_text is None or head_text is None:
        return True, "the project file was added or removed"
    base, head = parse(base_text), parse(head_text)
    for key in sorted(set(base) | set(head)):
        if key != "objects" and base.get(key) != head.get(key):
            return True, f"the project file's {key} changed"
    if _root(base) != _root(head) or project_object(base) != project_object(head):
        return True, "the project object changed"
    bo, ho = base["objects"], head["objects"]
    assert isinstance(bo, dict) and isinstance(ho, dict)
    changed = {oid for oid in set(bo) | set(ho) if oid != _root(head) and bo.get(oid) != ho.get(oid)}
    mine = player_objects(base) | player_objects(head)
    hit = sorted(changed & mine)
    if hit:
        o = _obj(ho, hit[0]) or _obj(bo, hit[0])
        return True, f"{len(hit)} changed object(s) are the Player's (first: {hit[0]}, {o.get('isa', '?')})"
    return False, f"{len(changed)} changed object(s), none the Player's"


def _show(rev: str) -> str | None:
    r = subprocess.run(["git", "show", f"{rev}:{PBXPROJ}"], capture_output=True, text=True)
    return r.stdout if r.returncode == 0 else None


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--base", default="HEAD^1")
    ap.add_argument("--head", default="HEAD")
    a = ap.parse_args(argv)
    try:
        player, why = touches_player(_show(a.base), _show(a.head))
    except ParseError as e:
        player, why = True, f"could not read the project file ({e}): counted as the Player's"
    line = f"player_ui={'true' if player else 'false'}"
    print(f"{line} ({why})")
    out = os.environ.get("GITHUB_OUTPUT")
    if out:
        with open(out, "a", encoding="utf-8") as f:
            f.write(line + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
