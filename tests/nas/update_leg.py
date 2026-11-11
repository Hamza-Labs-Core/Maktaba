"""Run one leg of the update rehearsal's container part (story 59.12): the image's updates in a
NAS shape.

    update_leg.py --leg NAME --prev IMAGE --cur IMAGE --next IMAGE --bad IMAGE
                  --fixtures DIR --out DIR [--keep]

The update rehearsal (.github/workflows/update-rehearsal.yml) proves the Mac app's update
paths with Go end-to-end tests; this proves the Docker image's on the shapes a NAS gives it
(tests/nas/update_legs.json): another uid with a read-only library, a 4 GB memory limit with
a large library, arm64, a container restarted in the middle of an update, an image older than
the volume's runtime. A NAS's catalog (TrueNAS app updates, Unraid's update check,
Watchtower, a household's pull) moves the image's tag, and the image puts a newer release on
its volume at the next start, under the launcher's guard (cmd/maktaba-server/cmd_container.go).

The images, each of this commit but the first:

  prev   the latest published release (ghcr.io/hamza-labs-core/maktaba-server:<tag>): N-1
  cur    this commit: N
  next   this commit with its version raised: N+1 (tests/nas/reversion.sh)
  bad    a release that cannot start (its server exits at once), versioned above next: N+2

Every leg, on its shape (cell.py's Cell: the same volumes, setup, readiness and logs):

  update    a home set up and scanned on prev, then started from cur: cur goes on the volume
            under the launcher's guard and is kept once its server has answered for three
            minutes; every video is still there
  rollback  started from bad: its server cannot start, the launcher goes back to cur, the
            image's release is recorded as refused and not tried again on the next start
  restore   after the rollback the home is as it was: the same videos, a sign-in, cur
            answering, Settings → Updates (the admin's container block) saying why

and the leg's own:

  restart (nas-restart)       `docker restart` at each step of the update: while the release
                              is put on the volume (staged), right after `current` moved
                              under the guard (moved), and once the new server answers but
                              before it is kept (before confirm); the next start resolves
                              each to a working runtime, with no manual step
  older (nas-image-older)     the volume moved on to next, then the container started from
                              cur: next runs, the log and Settings say the image is older
  no_oom, tier_light (nas-4g) no process OOM-killed under the limit through all of it

Everything is kept in --out (the cell's logs, docker logs of every container the leg ran,
report.json, summary.md). Exit 0 when every step that ran passed, 1 otherwise, 2 on a usage
error. The leg's update and rollback paths are proven here only, never on a household's box.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
import time
import traceback
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

HERE = Path(__file__).resolve().parent
LEGS = HERE / "update_legs.json"
sys.path.insert(0, str(HERE))

import cell as nas  # noqa: E402  (tests/nas/cell.py: the cell's shape, setup and logs)

# where the launcher keeps its guard (layout.GuardPath under the container's launcher root)
GUARD = f"{nas.VOLUME}/data/maktaba/updates/pending.json"
# how long a newer image's server must answer before it is kept (containerSettle), and more
KEEP_WAIT = 12 * 60
# a release that cannot start: the launcher's boot-loop rule goes back within minutes
ROLLBACK_WAIT = 15 * 60
# the bulk library's root (a large library: the nas-4g leg)
BULK = "bulk"


def load_legs(path: Path = LEGS) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def norm(v: str) -> str:
    """A version as the server compares it: without its leading v."""
    v = (v or "").strip()
    return v[1:] if len(v) > 1 and v[0] in "vV" and v[1].isdigit() else v


def image_version(image: str) -> str:
    """The release an image carries (its payload.json, what the entrypoint compares)."""
    p = nas.docker("run", "--rm", "--entrypoint", "cat", image, "/opt/maktaba/server/payload.json")
    return norm(json.loads(p.stdout)["version"])


def older_line(image: str, runtime: str) -> str:
    """The entrypoint's line for an image older than the volume's runtime (59.12's string)."""
    return f"The container image ({image}) is older than this home's runtime ({runtime}), so the runtime is running."


def bulk_script(d: str, items: int) -> str:
    """A shell line making `items` distinct small videos under d/bulk (one fixture clip, each
    copy given its own trailing bytes, so no two files are the same)."""
    return (f"mkdir -p '{d}/{BULK}' && src=\"$(ls {d}/en/*.mp4 | head -n 1)\""
            f" && i=0 && while [ $i -lt {items} ]; do i=$((i+1)); n=$(printf %05d $i);"
            f" sub='{d}/{BULK}/'$((i/1000)); mkdir -p \"$sub\";"
            f" cp \"$src\" \"$sub/clip $n.mp4\" && printf 'maktaba-%s' $n >> \"$sub/clip $n.mp4\"; done")


@dataclass
class UpdateLeg(nas.Cell):
    """A cell whose container is started from one image after another on the same volumes."""

    images: dict[str, str] = field(default_factory=dict)
    versions: dict[str, str] = field(default_factory=dict)
    baseline: dict[str, int] = field(default_factory=dict)
    starts: int = 0
    restarts: list[str] = field(default_factory=list)

    @property
    def container(self) -> str:
        return f"nasleg-{self.name}"

    def vol(self, what: str) -> str:
        return f"nasleg-{self.name}-{what}"

    @property
    def items(self) -> int:
        return int(self.spec.get("items", 0))

    # -- the volumes: the cell's, and the bulk library of a large-library leg

    def prepare(self) -> None:
        super().prepare()
        if self.items:
            log(f"{self.items} more videos in the bulk library")
            media = self.spec.get("media", {})
            nas.as_root(self.image, bulk_script("/d", self.items)
                        + f" && chown -R {media.get('owner', '1000:1000')} '/d/{BULK}'",
                        f"{self.vol('media')}:/d")

    @property
    def roots(self) -> list[str]:
        return super().roots + ([f"{self.media}/{BULK}"] if self.items else [])

    def make_libraries(self) -> None:
        super().make_libraries()
        if self.items:
            assert self.api is not None
            root = f"{self.media}/{BULK}"
            if not any(root in (lib.get("roots") or []) for lib in self.libraries()):
                s, d = self.api.call("POST", "/api/libraries", {"name": "Bulk", "roots": [root]})
                if s not in (200, 201):
                    raise AssertionError(f"POST /api/libraries {root}: {s} {d}")

    def counted(self) -> dict[str, int]:
        """Each library's videos: the fixture libraries' paged (40 clips), the bulk library's
        from its counts on GET /api/libraries (one request: paging 20,000 videos would hit the
        per-user rate limit at every poll)."""
        got = super().counted()
        if self.items:
            root = f"{self.media}/{BULK}"
            lib = next((x for x in self.libraries() if root in (x.get("roots") or [])), None)
            counts = (lib or {}).get("counts") or {}
            if "total" not in counts:
                raise AssertionError(f"GET /api/libraries gives the bulk library no counts: {lib}")
            got[BULK] = int(counts["total"]) - int(counts.get("missing", 0))
        return got

    def wanted(self) -> dict[str, int]:
        return {**nas.LIBRARIES, **({BULK: self.items} if self.items else {})}

    # -- the container, started from one image after another

    def keep_logs(self, why: str) -> None:
        """docker logs of the container that ran, before it is replaced."""
        self.starts += 1
        p = nas.docker("logs", "-t", self.container, check=False, timeout=120)
        (self.out / f"container-{self.starts:02d}-{why}.log").write_text((p.stdout or "") + (p.stderr or ""),
                                                                          encoding="utf-8")

    def start_from(self, which: str) -> None:
        """Stop the container (as `docker stop` does: its launcher drains) and start it again
        from the image `which`, on the same volumes."""
        log(f"the container from the {which} image ({self.versions[which]})")
        nas.docker("stop", "-t", "120", self.container, check=False, timeout=300)
        self.keep_logs(f"before-{which}")
        nas.docker("rm", "-f", self.container, check=False)
        self.image = self.images[which]
        self.start()

    def logs(self) -> str:
        p = nas.docker("logs", self.container, check=False, timeout=120)
        return (p.stdout or "") + (p.stderr or "")

    def wait_log(self, what: str, needle: str, seconds: float, every: float = 2, since: int = 0) -> None:
        """Wait for needle in docker logs (from offset since: what a start after a restart
        said). A version in needle matches with or without its v, as the entrypoint says it."""
        pat = re.compile(re.escape(needle).replace(r"Maktaba\ ", r"Maktaba\ v?"))
        nas.wait(what, lambda: pat.search(self.logs()[since:]) is not None, seconds, every=every)

    def version(self) -> str:
        assert self.api is not None
        s, d = self.api.get("/api/system/version", timeout=5)
        return norm(d.get("version", "")) if s == 200 and isinstance(d, dict) else ""

    def guard_left(self) -> bool:
        p = nas.docker("exec", self.container, "test", "-e", GUARD, check=False)
        return p.returncode == 0

    def container_block(self) -> dict[str, Any]:
        """GET /api/system/updates' container block (the admin's: story 59.12)."""
        assert self.api is not None
        s, d = self.api.get("/api/system/updates")
        if s != 200 or not isinstance(d, dict):
            raise AssertionError(f"GET /api/system/updates: {s} {d}")
        return d.get("container") or {}

    def same_library(self) -> str:
        got = self.counted()
        if got != self.baseline:
            raise AssertionError(f"the library is {got}, it was {self.baseline}")
        return ", ".join(f"{k}: {v}" for k, v in got.items())

    def kept(self, which: str) -> None:
        """The release of `which` runs and is kept (the guard cleared, the volume's own)."""
        v = self.versions[which]
        nas.wait(f"{v} answering", lambda: self.version() == v, KEEP_WAIT, every=5)
        self.wait_log(f"{v} kept", f"Maktaba {v} runs: kept", KEEP_WAIT, every=5)
        if self.guard_left():
            raise AssertionError(f"{v} was kept but the launcher's guard ({GUARD}) is still there")

    # -- the steps

    def home_on_prev(self) -> None:
        """A home set up, its libraries made and scanned, on the published release before."""
        self.image = self.images["prev"]
        self.prepare()
        self.setup()
        self.start()
        self.wait_ready()
        self.make_libraries()
        want = self.wanted()
        deadline = time.monotonic() + (50 if self.items else 12) * 60
        while True:
            self.scan_all()
            settle = time.monotonic() + (300 if self.items else 60)
            while time.monotonic() < settle:
                got = self.counted()
                if all(got.get(k, 0) >= n for k, n in want.items()):
                    break
                time.sleep(30)
            if all(got.get(k, 0) >= n for k, n in want.items()):
                break
            if time.monotonic() > deadline:
                raise AssertionError(f"the home on {self.versions['prev']} found {got} of {want}")
        self.baseline = got
        self.results.append({"name": "home", "ok": True,
                             "detail": f"set up on {self.versions['prev']}: {self.same_library()}"})

    def assert_update(self) -> str:
        """N-1 → N: cur goes on the volume under the guard and is kept; the library is whole."""
        self.start_from("cur")
        if self.spec.get("restart_at"):
            self.restart_through()
        self.wait_ready()
        self.kept("cur")
        c = self.container_block()
        if c.get("reason") != "same" or c.get("image") != self.versions["cur"]:
            raise AssertionError(f"Settings' container block after the update: {c}")
        return f"{self.versions['prev']} → {self.versions['cur']}, kept; {self.same_library()}"

    def restart_through(self) -> None:
        """`docker restart` at each step of the update (the leg's restart_at): staged (the
        release being put on the volume), moved (`current` moved under the guard) and before
        confirm (the new server answers, not yet kept). Each next start goes on by itself."""
        v = self.versions["cur"]
        marks = {
            "staged": f"putting Maktaba {v} in",
            "moved": f"Maktaba {v} replaces",
        }
        since = 0
        for step in self.spec["restart_at"]:
            if step == "before_confirm":
                nas.wait(f"{v} answering", lambda: self.version() == v, KEEP_WAIT, every=2)
                if f"Maktaba {v} runs: kept" in self.logs()[since:]:
                    raise AssertionError("kept before the restart: the step was missed")
            else:
                self.wait_log(f"the {step} step", marks[step], 15 * 60, every=0.5, since=since)
            since = len(self.logs())
            log(f"docker restart at {step}")
            self.keep_logs(f"restart-{step}")
            nas.docker("restart", "-t", "30", self.container, timeout=300)
            self.restarts.append(step)

    def assert_rollback(self) -> str:
        """A forced bad release: the launcher goes back to cur and the image is refused."""
        self.start_from("bad")
        bad, cur = self.versions["bad"], self.versions["cur"]
        self.wait_log("the launcher going back", f"Maktaba {bad} could not start here", ROLLBACK_WAIT, every=5)
        self.wait_ready()
        nas.wait(f"{cur} answering again", lambda: self.version() == cur, 5 * 60, every=5)
        if self.guard_left():
            raise AssertionError(f"the launcher went back but its guard ({GUARD}) is still there")
        # the same image again: not tried a second time
        log("docker restart: the refused image is not tried again")
        self.keep_logs("rolled-back")
        nas.docker("restart", "-t", "120", self.container, timeout=300)
        self.wait_log("the refused image passed by", f"this image's Maktaba {bad} could not start here before", 5 * 60)
        self.wait_ready()
        if self.version() != cur:
            raise AssertionError(f"after the refused image's restart {self.version()} runs, not {cur}")
        return f"{bad} could not start; back on {cur}, not tried again"

    def assert_restore(self) -> str:
        """After the rollback the home is as it was, and Settings says why."""
        lib = self.same_library()
        c = self.container_block()
        if (c.get("reason") != "image_refused" or c.get("running") != "runtime"
                or c.get("image") != self.versions["bad"] or c.get("runtime") != self.versions["cur"]):
            raise AssertionError(f"Settings' container block after the rollback: {c}")
        return f"{lib}; Settings: {c}"

    def assert_older(self) -> str:
        """The volume on N+1, the image N started: N+1 runs, and the log and Settings say why."""
        self.start_from("next")
        self.wait_ready()
        self.kept("next")
        self.start_from("cur")
        self.wait_log("the older image's line", older_line(self.versions["cur"], self.versions["next"]), 5 * 60)
        self.wait_ready()
        if self.version() != self.versions["next"]:
            raise AssertionError(f"{self.version()} runs, not the volume's {self.versions['next']}")
        c = self.container_block()
        if c.get("reason") != "image_older" or c.get("running") != "runtime":
            raise AssertionError(f"Settings' container block: {c}")
        return f"{self.versions['next']} runs from the volume; Settings: {c}; {self.same_library()}"

    def assert_restart(self) -> str:
        if self.restarts != list(self.spec.get("restart_at", [])):
            raise AssertionError(f"restarted at {self.restarts}, not {self.spec.get('restart_at')}")
        return f"restarted at {', '.join(self.restarts)}; each next start went on by itself"

    # -- the whole leg

    def run(self) -> bool:
        self.out.mkdir(parents=True, exist_ok=True)
        try:
            self.versions = {k: image_version(v) for k, v in self.images.items()}
            self.results.append({"name": "images", "ok": True,
                                 "detail": ", ".join(f"{k} {v}" for k, v in self.versions.items())})
            order = [self.versions[k] for k in ("prev", "cur", "next", "bad")]
            if not order_ok(order):
                raise AssertionError(f"the images are not in order N-1 < N < N+1 < N+2: {order}")
            self.home_on_prev()
            for step in self.spec.get("steps", []):
                self.check(step)
                if self.results[-1]["ok"] is False and step in ("update", "rollback"):
                    break  # what follows stands on it
            for a in self.spec.get("asserts", []):
                self.check(a)
        except Exception as e:  # noqa: BLE001
            self.results.append({"name": "run", "ok": False, "detail": f"{type(e).__name__}: {e}"})
            log(f"FAILED: {e}")
            (self.out / "harness-error.txt").write_text(traceback.format_exc(), encoding="utf-8")
        finally:
            try:
                self.keep_logs("end")
                self.collect()
            finally:
                self.report()
        return all(r["ok"] is not False for r in self.results)

    def how(self) -> str:
        return f"update leg, images {self.versions}; `docker run {' '.join(self.shape_or_error())}`"


_NUM = re.compile(r"^(\d+)\.(\d+)\.(\d+)(?:-(\d+)-g[0-9a-f]+)?$")


def order_ok(versions: list[str]) -> bool:
    """Whether versions rise (a release, or a `git describe` past it: commits since the tag)."""
    keys = []
    for v in versions:
        m = _NUM.match(norm(v))
        if not m:
            return False
        keys.append(tuple(int(x or 0) for x in m.groups()))
    return all(a < b for a, b in zip(keys, keys[1:]))


def log(msg: str) -> None:
    nas.log(msg)


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--leg", required=True)
    for k in ("prev", "cur", "next", "bad"):
        ap.add_argument(f"--{k}", required=True, metavar="IMAGE")
    ap.add_argument("--fixtures", required=True, type=Path)
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--keep", action="store_true", help="leave the container and volumes")
    args = ap.parse_args(argv)
    legs = load_legs()
    spec = next((c for c in legs["legs"] if c["name"] == args.leg), None)
    if spec is None:
        print(f"no leg {args.leg!r} in {LEGS}", file=sys.stderr)
        return 2
    landed = list(nas.load_cells().get("landed", []))
    leg = UpdateLeg(spec=spec, image=args.cur, fixtures=args.fixtures.resolve(), out=args.out.resolve(),
                    local_port=None, landed=landed,
                    images={"prev": args.prev, "cur": args.cur, "next": args.next, "bad": args.bad})
    try:
        ok = leg.run()
    finally:
        if not args.keep:
            leg.teardown(quiet=True)
    print((leg.out / "summary.md").read_text(encoding="utf-8"))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
