---
schema: 1
kind: pr
pr: 1440
title: "Story 54.27: each first-party plugin is built as its own .mkplugin release package"
date: 2026-11-19
session: 9aa2417d-57dc-4c99-a4b6-462f5098d816
agent: claude-code
---

## What happened

Story 54.27 shipped in #1440 (merge 19f9c90eb).

- `tools/plugins/build.py` builds one `.mkplugin` per first-party plugin, with its `files.json`. Both go in the release's signed `checksums.txt`.
- `release.yml` has a `package-plugins` job. After signing, `api/tools/fp-packages` (`firstparty.VerifyReleasePackages`) checks every package against the bundle and against the release's own pipeline wheel, digest for digest both ways.
- The `_plugins.yml` first-party job runs the same check as a dry run on pull requests that touch the plugins' paths.
- A pre-release tag builds no packages, and the payload does not carry the packages: nothing on a home reads one before 54.45/54.46 build an install path. Every first-party folder was packaged, core included (13 packages); #1450 later stopped packaging core.

It took seven review rounds: six by Kimi K3.1 Pro, then one by GLM-5.3 after Kimi's usage limit ran out. They found:

- RECORD read as CSV with its sizes;
- an orphan `files.json` refused, and `CheckPackage` bound to the package's own name;
- the pipeline wheel's identity, by file name and by METADATA;
- the release job's Go too old for `api` (a second setup-go, and a test that each module download follows the right Go);
- per-member, RECORD, member-count and total limits, declared and read;
- bytecode refused;
- METADATA headers read as Python's `email.parser` reads them: case, folding, the header block only, and in round six a lone CR, which Python ends a line at and Go did not. That let an older pipeline wheel pass under this release's name.

Round seven found three minors, filed in #1456 and fixed or answered in #1450. The publish gap the first round found became #1447.

## What we learned

- Two readers of one format must agree on line ends as well as fields. Python splits headers at CR, LF and CRLF. A Go reader that splits on LF alone can be shown a different header than Python sees. Refuse a CR in the header block and any line Python would not read as a header, so Python's header block ends no later than Go's, on the same lines.
- A mutation that still passes means two checks share one test. Here the declared total and the read total did: a test that names the error message tells them apart.
- A test run leaves `__pycache__` in the source tree. A check that refuses bytecode needs the CI dry run to remove it before `uv build`, as the release build does.
- The review loop's cost fell once findings were rated: only blockers and majors earn another round, and minors go in one issue.

## Keep doing

- Run every new check's mutation (turn it off, see its test fail) before pushing, and reproduce the reviewer's scenario through the real command when it is a trust-chain finding.
