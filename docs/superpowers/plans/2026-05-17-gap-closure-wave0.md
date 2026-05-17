# Gap-Closure Wave 0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the 5 systemic-trunk tracks (R1 pipeline dispatch, R2 gRPC transport, R3 auth gate, R4 web↔API contract, V real test gates) so Maktaba goes from "boots but does nothing / is unauthenticated / has false-green CI" to "end-to-end functional with a trustworthy merge gate."

**Architecture:** Each track is one isolated git worktree branched off `integration/gap-closure`, implemented by one agent, merged back after a diff review + real test run. **W0-V merges first** — until the test gates actually assert, no other track's "tests pass" is trustworthy. Then R1/R2/R3/R4 (mutually independent code areas) re-run suites against the real gates and merge.

**Tech Stack:** Python 3.12 (`pipeline`, pytest/pytest-asyncio), Go 1.23 (`api`, `streaming`, `go test`), TypeScript/React + Vite + Vitest (`web`), Make + GitHub Actions CI, docker compose e2e stack.

**Spec:** `docs/superpowers/specs/2026-05-17-gap-closure-design.md` (commit `a85e359`).

**Scope note (R2):** Pipeline already exposes a JSON-on-wire generic gRPC server (`pipeline/src/maktaba_pipeline/grpc_server.py`, service `maktaba.pipeline.v1.Pipeline`). W0-R2 matches that existing JSON-codec convention — it does NOT introduce protoc/buf (that is a later DevOps-epic decision). RPCs with no server side today (`pipeline.Transcribe` streaming, `pipeline.STTTest`) are explicitly **deferred to the Epic 03/07 waves** and left returning `ErrNotImplemented` in Wave 0.

---

## Branch setup (do once, before any track)

- [ ] **Step 1: Create the integration branch from approved spec commit**

```bash
git checkout main
git rev-parse HEAD            # expect a85e359 (or later if main advanced)
git checkout -b integration/gap-closure
git push -u origin integration/gap-closure   # ONLY if user has authorized push; otherwise keep local
```
Expected: branch `integration/gap-closure` exists at the spec commit.

> Each track below is executed in its own worktree off `integration/gap-closure` via `superpowers:using-git-worktrees`. Track ordering: **V first**, then R1/R2/R3/R4 in parallel. Each track ends by merging its branch into `integration/gap-closure` and re-running that track's suite.

---

## TRACK V — Make `test-e2e` / `perf-ci` real gates (MERGES FIRST)

**Why first:** `Makefile:290-317` makes both gates pass while asserting nothing (`test-e2e-inner` converts pytest exit-5 "no tests" to success; `perf-ci-inner` is `echo`). Until fixed, every other track's green is meaningless.

**Files:**
- Modify: `Makefile:290-317`
- Create: `tests/e2e/__init__.py`
- Create: `tests/e2e/conftest.py`
- Create: `tests/e2e/test_smoke.py`
- Create: `tests/perf/ci_subset.py`
- Create: `tests/perf/test_perf_ci.py`
- Reference (read): `shared/perf_budgets.yaml`, `deploy/compose/docker-compose.yml`, `.github/workflows/_e2e.yml`, `.github/workflows/_perf-ci.yml`

### Task V.1: e2e smoke test that fails when the stack is down

- [ ] **Step 1: Create the e2e package marker**

Create `tests/e2e/__init__.py`:
```python
```
(empty file — marks the directory as a package)

- [ ] **Step 2: Write the e2e fixture (base URL + readiness wait)**

Create `tests/e2e/conftest.py`:
```python
import os
import time
import urllib.error
import urllib.request

import pytest

API_BASE = os.environ.get("MAKTABA_E2E_API", "http://localhost:8080")
_READY_TIMEOUT_S = 60


def _get(url: str, timeout: float = 5.0) -> tuple[int, bytes]:
    req = urllib.request.Request(url, method="GET")
    with urllib.request.urlopen(req, timeout=timeout) as resp:  # noqa: S310
        return resp.status, resp.read()


@pytest.fixture(scope="session")
def api_base() -> str:
    deadline = time.monotonic() + _READY_TIMEOUT_S
    last_err: Exception | None = None
    while time.monotonic() < deadline:
        try:
            status, _ = _get(f"{API_BASE}/healthz")
            if status == 200:
                return API_BASE
        except (urllib.error.URLError, OSError) as exc:  # not ready yet
            last_err = exc
        time.sleep(2)
    raise RuntimeError(f"API not ready at {API_BASE} within {_READY_TIMEOUT_S}s: {last_err}")
```

- [ ] **Step 3: Write the failing smoke test**

Create `tests/e2e/test_smoke.py`:
```python
import json
import urllib.request

import pytest

pytestmark = pytest.mark.e2e


def _get_json(url: str) -> tuple[int, dict]:
    req = urllib.request.Request(url, method="GET")
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:  # noqa: S310
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as exc:  # noqa: F821
        return exc.code, {}


def test_api_health_is_green(api_base: str) -> None:
    status, body = _get_json(f"{api_base}/healthz")
    assert status == 200, f"/healthz returned {status}"
    assert body.get("status") in {"ok", "healthy", "pass"}, body


def test_unauthenticated_business_route_is_rejected(api_base: str) -> None:
    # After R3, a protected business route must NOT be reachable anonymously.
    req = urllib.request.Request(f"{api_base}/api/libraries", method="GET")
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:  # noqa: S310
            code = resp.status
    except urllib.error.HTTPError as exc:  # noqa: F821
        code = exc.code
    assert code in (401, 403), f"expected auth rejection, got {code}"
```

> Note: `import urllib.error` is needed in the smoke module too — add it at top:
> `import urllib.error` (place with the other imports).

- [ ] **Step 4: Verify it FAILS with no stack (proves the gate now asserts)**

Run:
```bash
cd /Users/mahmouddarwish/Code/Maktaba
uv run pytest -m e2e tests/e2e -q
```
Expected: FAIL/ERROR (RuntimeError: API not ready) — this is correct; a no-op gate would have exited 0.

- [ ] **Step 5: Verify it PASSES with the stack up**

Run:
```bash
docker compose -f deploy/compose/docker-compose.yml up -d --wait
uv run pytest -m e2e tests/e2e -q
docker compose -f deploy/compose/docker-compose.yml down
```
Expected: 2 passed (test_api_health_is_green, test_unauthenticated_business_route_is_rejected).

> If `test_unauthenticated_business_route_is_rejected` fails because R3 has not merged yet: that is expected ordering — V merges first, the assertion encodes the R3 contract and will go green once R3 lands. Keep the test; mark it `@pytest.mark.xfail(reason="enforced once W0-R3 merges", strict=False)` ONLY if it blocks V's own merge, and file a follow-up to remove the xfail in R3's merge step.

- [ ] **Step 6: Commit**

```bash
git add tests/e2e/__init__.py tests/e2e/conftest.py tests/e2e/test_smoke.py
git commit -m "test(e2e): real smoke gate — fails when stack down / route unauthed"
```

### Task V.2: Replace the false-green `test-e2e` recipe

- [ ] **Step 1: Read the current target**

Run: `sed -n '288,320p' Makefile`
Expected: shows `test-e2e`, `test-e2e-inner` (with the `[ $$rc -eq 5 ] || exit $$rc` exit-5 swallow), `perf-ci`, `perf-ci-inner` (echo stub).

- [ ] **Step 2: Rewrite `test-e2e-inner` to run the real suite and fail on empty**

In `Makefile`, replace the `test-e2e-inner` recipe body so it runs the new suite from repo root and treats "no tests collected" as failure:
```make
.PHONY: test-e2e-inner
test-e2e-inner:
	@uv run pytest -m e2e tests/e2e -q
```
(Remove the `|| { rc=$$?; [ $$rc -eq 5 ] || exit $$rc; }` swallow. pytest exit 5 = no tests = the gate is empty = FAIL, which is the desired behavior now that real tests exist.)

- [ ] **Step 3: Verify the gate fails when the stack is down**

Run: `docker compose -f deploy/compose/docker-compose.yml down 2>/dev/null; make test-e2e`
Expected: non-zero exit (RuntimeError / failed assertions). A green here would mean the gate is still fake.

- [ ] **Step 4: Verify the gate passes when the stack is up**

Run:
```bash
docker compose -f deploy/compose/docker-compose.yml up -d --wait
make test-e2e
docker compose -f deploy/compose/docker-compose.yml down
```
Expected: exit 0, pytest reports passed.

- [ ] **Step 5: Commit**

```bash
git add Makefile
git commit -m "build(ci): test-e2e runs real e2e suite; empty suite now fails"
```

### Task V.3: Real `perf-ci` consuming `shared/perf_budgets.yaml`

- [ ] **Step 1: Write the CI-subset budget loader test**

Create `tests/perf/test_perf_ci.py`:
```python
from pathlib import Path

import pytest

from tests.perf.ci_subset import ci_pr_budgets

REPO = Path(__file__).resolve().parents[2]


def test_ci_subset_is_nonempty_and_well_formed() -> None:
    budgets = ci_pr_budgets(REPO / "shared" / "perf_budgets.yaml")
    assert budgets, "perf-ci must assert at least one budget"
    for b in budgets:
        assert b.name
        assert b.p95_ms > 0
```

- [ ] **Step 2: Run it to verify failure (module missing)**

Run: `cd /Users/mahmouddarwish/Code/Maktaba && uv run pytest tests/perf/test_perf_ci.py -q`
Expected: FAIL — `ModuleNotFoundError: tests.perf.ci_subset`.

- [ ] **Step 3: Implement the CI-subset loader**

Create `tests/perf/__init__.py` (empty), then `tests/perf/ci_subset.py`:
```python
from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path

import yaml


@dataclass(frozen=True)
class Budget:
    name: str
    p95_ms: float


def ci_pr_budgets(path: Path) -> list[Budget]:
    """Return the endpoint budgets flagged `ci_pr: true` in perf_budgets.yaml.

    The PR-time perf gate only enforces this subset; full perf is a
    later (Epic 20.7) nightly job.
    """
    data = yaml.safe_load(path.read_text())
    out: list[Budget] = []
    for entry in (data or {}).get("endpoints", []):
        if not entry.get("ci_pr"):
            continue
        out.append(Budget(name=str(entry["name"]), p95_ms=float(entry["p95_ms"])))
    return out
```

> Read `shared/perf_budgets.yaml` first and adjust the key names (`endpoints`, `name`, `p95_ms`, `ci_pr`) to match its actual schema. The structure above reflects the gap-analysis description ("8 endpoint budgets p50/p95/p99, `ci_pr: true` flag"); confirm exact keys before implementing and use the real ones.

- [ ] **Step 4: Run the loader test to verify pass**

Run: `cd /Users/mahmouddarwish/Code/Maktaba && uv run pytest tests/perf/test_perf_ci.py -q`
Expected: PASS (1 passed).

- [ ] **Step 5: Rewrite `perf-ci-inner` to assert the subset loads**

In `Makefile`, replace `perf-ci-inner`:
```make
.PHONY: perf-ci-inner
perf-ci-inner:
	@uv run pytest tests/perf/test_perf_ci.py -q
```
(This makes `perf-ci` fail if the budget file is malformed/empty or the subset is missing. Measuring live endpoints against the budget is Epic 20.7 — out of W0 scope, but the gate now asserts something real instead of `echo`.)

- [ ] **Step 6: Verify**

Run: `make perf-ci`
Expected: exit 0, `1 passed`. Then temporarily break it: `mv shared/perf_budgets.yaml /tmp/pb && make perf-ci; mv /tmp/pb shared/perf_budgets.yaml` → expect non-zero (proves it asserts).

- [ ] **Step 7: Commit**

```bash
git add tests/perf/__init__.py tests/perf/ci_subset.py tests/perf/test_perf_ci.py Makefile
git commit -m "build(ci): perf-ci asserts the ci_pr budget subset instead of echo"
```

### Task V.4: Merge V into integration first

- [ ] **Step 1: Run the full local gate**

Run: `make test-unit && make test-e2e && make perf-ci` (with compose up for e2e).
Expected: all exit 0 with real assertions executed.

- [ ] **Step 2: Diff review + merge**

```bash
git checkout integration/gap-closure
git merge --no-ff <V-branch> -m "merge(W0-V): real e2e + perf-ci gates"
```
Expected: clean merge (V touches Makefile + new test dirs only — no overlap with R1-R4).

---

## TRACK R1 — Real pipeline stage dispatch

**Files:**
- Modify: `pipeline/src/maktaba_pipeline/runtime.py:188-215` (dispatch table → real handlers)
- Modify: `pipeline/src/maktaba_pipeline/__main__.py:35-42` (restore SCAN), `:118` (pass overrides)
- Create: `pipeline/src/maktaba_pipeline/pipeline/handlers.py` (adapter handlers)
- Test: `pipeline/tests/pipeline/test_handlers.py`
- Reference (read): `scanner/service.py`, `audio/probe.py`, `audio/extract.py`, `stt/segment_commit.py`, `subtitle/generator.py`, `search/embedder.py`, `db/jobs.py:63-77`

> Each stage's real logic exists but has a non-`(db, job)` signature. R1 writes thin adapter handlers (`async def handler(db, job)` → load inputs from job → call existing lib → persist → mark done/failed), then registers them via the already-built `dispatch_overrides` path (`runtime.py:198`).

### Task R1.1: Adapter for the PROBE stage (worked example pattern)

- [ ] **Step 1: Write the failing handler test**

Create `pipeline/tests/pipeline/test_handlers.py`:
```python
import uuid

import pytest

from maktaba_pipeline.db.jobs import Job, Stage
from maktaba_pipeline.pipeline.handlers import build_real_dispatch


@pytest.mark.asyncio
async def test_probe_handler_persists_and_advances(fake_probe_db) -> None:
    handlers = build_real_dispatch()
    job = Job(id=1, stage=Stage.PROBE, video_id=uuid.uuid4())
    await handlers[Stage.PROBE](fake_probe_db, job)
    assert fake_probe_db.committed_video_id == job.video_id
    assert fake_probe_db.marked_done == 1
```

> Add a `fake_probe_db` fixture to `pipeline/tests/pipeline/conftest.py` modeled on the existing fake in `tests/audio/test_probe.py` (read that file; reuse its `_ProbeDB` fake shape so the adapter is tested against the same contract `commit_probe` already expects).

- [ ] **Step 2: Run to verify failure**

Run: `cd pipeline && uv run pytest tests/pipeline/test_handlers.py -q`
Expected: FAIL — `ModuleNotFoundError: maktaba_pipeline.pipeline.handlers`.

- [ ] **Step 3: Implement the PROBE adapter + registry skeleton**

Create `pipeline/src/maktaba_pipeline/pipeline/handlers.py`:
```python
from __future__ import annotations

from maktaba_pipeline.audio.probe import commit_probe, run_ffprobe
from maktaba_pipeline.db.jobs import DBConn, Job, Stage, StageHandler, mark_done


async def _probe(db: DBConn, job: Job) -> None:
    if job.video_id is None:
        raise ValueError("probe job missing video_id")
    result = await run_ffprobe(db, video_id=job.video_id)  # see note
    await commit_probe(db, video_id=job.video_id, result=result)
    await mark_done(db, job_id=job.id)


def build_real_dispatch() -> dict[Stage, StageHandler]:
    return {
        Stage.PROBE: _probe,
    }
```

> Read `audio/probe.py` to confirm the exact function that produces a `ProbeResult` from a video (the gap map shows `commit_probe(db, *, video_id, result)` is the persist step; find its companion that runs ffprobe — adjust `run_ffprobe` import/signature to the real one). Do NOT invent APIs; use what `audio/probe.py` actually exports.

- [ ] **Step 4: Run to verify pass**

Run: `cd pipeline && uv run pytest tests/pipeline/test_handlers.py -q`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pipeline/src/maktaba_pipeline/pipeline/handlers.py pipeline/tests/pipeline/test_handlers.py pipeline/tests/pipeline/conftest.py
git commit -m "feat(pipeline): real PROBE stage adapter handler"
```

### Task R1.2: Adapters for SCAN, EXTRACT, TRANSCRIBE, SUBTITLE_GEN, INDEX

> One sub-task per stage, each following the R1.1 TDD shape (failing test → adapter → pass → commit). Exact lib entry points to wrap (confirm signatures by reading each module):

- [ ] **SCAN** — wrap `scanner.service.ScannerService.run(library_id, options)`. Job carries `library_id`; adapter constructs `ScannerService`, runs it, enqueues PROBE jobs for discovered videos, `mark_done`. Test: asserts probe jobs enqueued.
- [ ] **EXTRACT** — wrap `audio.extract.extract_to_file(path, track_index, ...)`. Adapter resolves source path + selected track from prior PROBE output, extracts, records artifact, enqueues TRANSCRIBE, `mark_done`. Test: asserts artifact path persisted + TRANSCRIBE enqueued.
- [ ] **TRANSCRIBE** — wrap `stt.segment_commit.commit_segment(db, *, transcript_id, segment)` driven by the STT backend stream. Adapter creates transcript row, iterates backend segments committing each, enqueues SUBTITLE_GEN + INDEX, `mark_done`. Test: asserts ≥1 segment committed + downstream enqueued.
- [ ] **SUBTITLE_GEN** — wrap `subtitle.generator.generate_srt(cues)` / `generate_vtt(cues)` (sync). Adapter loads committed segments as cues, renders SRT+VTT, persists subtitle artifacts, `mark_done`. Test: asserts SRT+VTT artifacts written.
- [ ] **INDEX** — wrap `search.embedder.index_segments(collection, docs, embed=...)` (sync). Adapter loads segments, builds `SegmentDoc`s, upserts into the video's Chroma collection, `mark_done`. Test: asserts upsert count > 0.

For each: add its handler fn to `handlers.py`, register in `build_real_dispatch()`, add a failing→passing test in `test_handlers.py`, commit `feat(pipeline): real <STAGE> stage adapter handler`.

> THUMBNAIL has no implementing module (confirmed by R1 investigation). Leave it bound to the existing placeholder; file a follow-up Linear note. Do not invent a thumbnail implementation in W0.

### Task R1.3: Wire the real dispatch into runtime + restore SCAN

- [ ] **Step 1: Write failing wiring test**

Add to `pipeline/tests/pipeline/test_runner.py` (new test):
```python
@pytest.mark.asyncio
async def test_run_uses_real_dispatch_not_placeholder() -> None:
    from maktaba_pipeline.pipeline.handlers import build_real_dispatch
    overrides = build_real_dispatch()
    assert Stage.PROBE in overrides
    assert Stage.SCAN in overrides
    assert Stage.INDEX in overrides
```

- [ ] **Step 2: Run — expect pass for handlers presence, then wire `__main__`**

Run: `cd pipeline && uv run pytest tests/pipeline/test_runner.py -q` → PASS for the new test.

- [ ] **Step 3: Restore SCAN to default stages**

In `pipeline/src/maktaba_pipeline/__main__.py:35-42`, add `Stage.SCAN,` as the first element of `_DEFAULT_STAGES`.

- [ ] **Step 4: Pass real overrides into `run()`**

In `__main__.py` near line 118, change `await run(cfg, db=database)` to:
```python
from maktaba_pipeline.pipeline.handlers import build_real_dispatch
await run(cfg, db=database, dispatch_overrides=build_real_dispatch())
```

- [ ] **Step 5: Run full pipeline unit suite**

Run: `cd pipeline && uv run pytest -m unit -q`
Expected: PASS (all existing tests + new handler/runner tests).

- [ ] **Step 6: Commit**

```bash
git add pipeline/src/maktaba_pipeline/__main__.py pipeline/tests/pipeline/test_runner.py
git commit -m "feat(pipeline): wire real dispatch table; restore SCAN to defaults"
```

### Task R1.4: Merge R1 into integration

- [ ] Run `cd pipeline && uv run pytest -m unit -q` then merge `<R1-branch>` into `integration/gap-closure` (no-ff). R1 touches only `pipeline/` — no overlap with R2/R3/R4/V.

---

## TRACK R2 — gRPC transport (JSON-codec convention)

**Files:**
- Modify: `api/internal/grpcclients/pipeline/realclient.go` (real JSON-gRPC calls)
- Modify: `api/internal/grpcclients/streaming/realclient.go` (real JSON-gRPC calls)
- Create: `api/internal/grpcclients/jsoncodec/codec.go` (identity/JSON codec matching pipeline server)
- Create: `streaming/internal/grpcsrv/serve.go` (gRPC listener wrapping existing `grpcsrv.Server`)
- Modify: `streaming/main.go` (start the gRPC server)
- Test: `api/internal/grpcclients/pipeline/realclient_test.go`, `streaming/internal/grpcsrv/serve_test.go`
- Reference (read): `pipeline/src/maktaba_pipeline/grpc_server.py` (the wire format to match), `api/.../pipeline/pipeline.go:60-67`, `streaming/.../streaming.go:63-69`, `streaming/internal/grpcsrv/server.go`

> **W0-R2 scope:** make working RPCs for everything that has a server side — pipeline `Embed`/`ListBackends`/`ExtractEmbeddedSubtitle`/`HealthCheck`, and streaming `OpenSession`/`CloseSession`/`EvictHashCache`/`GetCapabilities`/`HealthCheck`. `pipeline.Transcribe` (server-side streaming, no server impl) and `pipeline.STTTest` (no server impl) stay `ErrNotImplemented` — deferred to Epic 03/07 waves. Match the existing JSON-on-bytes identity-codec the pipeline server already uses; do NOT add protoc/buf.

### Task R2.1: JSON gRPC codec (Go) matching the pipeline server

- [ ] **Step 1: Read the server's wire contract**

Run: `sed -n '40,150p' pipeline/src/maktaba_pipeline/grpc_server.py`
Record: service path (`maktaba.pipeline.v1.Pipeline`), method names, that payloads are JSON-encoded dicts over a raw bytes identity serializer, and full method paths (`/maktaba.pipeline.v1.Pipeline/Embed`, `/ListBackends`, `/ExtractEmbeddedSubtitle`).

- [ ] **Step 2: Write failing codec test**

Create `api/internal/grpcclients/jsoncodec/codec_test.go`:
```go
package jsoncodec

import "testing"

func TestCodecRoundTrip(t *testing.T) {
	c := Codec{}
	in := map[string]any{"text": "hello"}
	b, err := c.Marshal(in)
	if err != nil { t.Fatal(err) }
	var out map[string]any
	if err := c.Unmarshal(b, &out); err != nil { t.Fatal(err) }
	if out["text"] != "hello" { t.Fatalf("got %v", out) }
	if c.Name() != "json" { t.Fatalf("name=%s", c.Name()) }
}
```

- [ ] **Step 3: Run — expect fail (package missing)**

Run: `cd api && go test ./internal/grpcclients/jsoncodec/ -run TestCodecRoundTrip`
Expected: build error / no package.

- [ ] **Step 4: Implement the codec**

Create `api/internal/grpcclients/jsoncodec/codec.go`:
```go
// Package jsoncodec implements the JSON-on-bytes gRPC codec the
// Python pipeline server uses (identity serializer + JSON payloads),
// so Go clients can speak it without a protobuf toolchain.
package jsoncodec

import "encoding/json"

type Codec struct{}

func (Codec) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (Codec) Unmarshal(b []byte, v any) error    { return json.Unmarshal(b, v) }
func (Codec) Name() string                       { return "json" }
```

- [ ] **Step 5: Run — expect pass**

Run: `cd api && go test ./internal/grpcclients/jsoncodec/`
Expected: ok.

- [ ] **Step 6: Commit**

```bash
git add api/internal/grpcclients/jsoncodec/
git commit -m "feat(api): JSON gRPC codec matching pipeline server wire format"
```

### Task R2.2: Real pipeline client (Embed / ListBackends / ExtractEmbeddedSubtitle / HealthCheck)

- [ ] **Step 1: Write failing client test against an in-test gRPC server**

Create `api/internal/grpcclients/pipeline/realclient_test.go` that: starts a `grpc.NewServer(grpc.ForceServerCodec(jsoncodec.Codec{}))` registering a fake `maktaba.pipeline.v1.Pipeline/Embed` returning `{"embedding":[0.1,0.2]}` on a bufconn or `127.0.0.1:0` listener; constructs the real client pointed at it; asserts `Embed(ctx,"hi")` returns `[]float32{0.1,0.2}`.

> Use `google.golang.org/grpc/test/bufconn` (already in the grpc module graph). Model the fake server registration on the method path recorded in R2.1 Step 1.

- [ ] **Step 2: Run — expect fail (`ErrNotImplemented`)**

Run: `cd api && go test ./internal/grpcclients/pipeline/ -run TestRealClientEmbed`
Expected: FAIL — current `Embed` returns `ErrNotImplemented` (`realclient.go:53`).

- [ ] **Step 3: Implement real dial + invoke**

In `api/internal/grpcclients/pipeline/realclient.go`: replace the bare `net.DialTimeout` health-only stub with a real `grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(jsoncodec.Codec{})))`; store the `*grpc.ClientConn`; implement `Embed`, `ListBackends`, `ExtractEmbeddedSubtitle`, `HealthCheck` via `conn.Invoke(ctx, "/maktaba.pipeline.v1.Pipeline/<Method>", reqMap, &respMap)` and map JSON → the existing Go return types (`[]float32`, `[]Backend`, `string`, `Status`). Keep `Transcribe` and `STTTest` returning `ErrNotImplemented` with a `// deferred: Epic 03/07 wave` comment. Preserve the existing `Breaker` wrapping.

- [ ] **Step 4: Run — expect pass**

Run: `cd api && go test ./internal/grpcclients/pipeline/`
Expected: ok.

- [ ] **Step 5: Commit**

```bash
git add api/internal/grpcclients/pipeline/
git commit -m "feat(api): real pipeline gRPC client (Embed/ListBackends/Extract/Health)"
```

### Task R2.3: Streaming gRPC server + real streaming client

- [ ] **Step 1: Failing server test**

Create `streaming/internal/grpcsrv/serve_test.go`: start `Serve(lis, existingServer)` on a bufconn; dial with the JSON codec; call `/maktaba.streaming.v1.Streaming/GetCapabilities`; assert a non-error response with the capabilities shape from `grpcsrv.Server.GetCapabilities`.

- [ ] **Step 2: Run — expect fail (no `Serve`)**

Run: `cd streaming && go test ./internal/grpcsrv/ -run TestServeCapabilities`
Expected: build error (no `Serve`).

- [ ] **Step 3: Implement `Serve`**

Create `streaming/internal/grpcsrv/serve.go`: a `Serve(lis net.Listener, s *Server) (*grpc.Server, error)` that builds `grpc.NewServer(grpc.ForceServerCodec(jsoncodec.Codec{}))`, registers a `grpc.ServiceDesc` for `maktaba.streaming.v1.Streaming` whose handlers JSON-decode into the existing `Server` method args and JSON-encode results (`OpenSession`, `CloseSession`, `EvictHashCache`, `GetCapabilities`, `HealthCheck`), and `go srv.Serve(lis)`.

> Reuse the `jsoncodec` package from R2.1 (import path `.../api/internal/grpcclients/jsoncodec`) only if it is in a shared module; if `streaming` is a separate Go module, duplicate the ~10-line codec into `streaming/internal/grpcsrv/jsoncodec.go` rather than cross-module import. Confirm module boundaries via `go.mod` files first.

- [ ] **Step 4: Start it from `streaming/main.go`**

In `streaming/main.go` after the reaper start (~line 243, per R2 investigation), open a listener on `$MAKTABA_STREAMING_GRPC_ADDR` (default `:9050`) and call `grpcsrv.Serve(lis, server)`; log the bound addr; ensure graceful stop on shutdown alongside the HTTP servers.

- [ ] **Step 5: Implement the streaming client**

In `api/internal/grpcclients/streaming/realclient.go`: same pattern as R2.2 — real `grpc.NewClient` + JSON codec; implement `OpenSession`/`CloseSession`/`EvictHashCache`/`GetCapabilities`/`HealthCheck` via `conn.Invoke` to `/maktaba.streaming.v1.Streaming/<Method>`, mapping to the existing Go types in `streaming.go:18-48`.

- [ ] **Step 6: Failing→passing client test**

Add `api/internal/grpcclients/streaming/realclient_test.go` mirroring R2.2 against an in-test streaming server (or the real `grpcsrv.Serve` on bufconn). Run `cd api && go test ./internal/grpcclients/streaming/` → ok. Run `cd streaming && go test ./internal/grpcsrv/` → ok.

- [ ] **Step 7: Commit**

```bash
git add streaming/internal/grpcsrv/ streaming/main.go api/internal/grpcclients/streaming/
git commit -m "feat(streaming): gRPC server + real api streaming client (JSON codec)"
```

### Task R2.4: Merge R2 into integration

- [ ] Run `cd api && make -C .. test-unit-go` (or `go test ./...` for api+streaming). Then `git merge --no-ff <R2-branch>` into `integration/gap-closure`. R2 touches `api/internal/grpcclients/**` + `streaming/**` — no overlap with R1/R3/R4/V.

---

## TRACK R3 — Auth gate (wire existing `RequireAuth` / `CookieAuth` / `ACLStore`)

**Files:**
- Modify: `api/auth_bootstrap.go:91-106` (mount RequireAuth on business routes)
- Modify: `api/main.go:253-261` (use the returned `p9` handler — install `CookieAuth`), `:285` (ordering)
- Modify: `api/internal/handlers/auth/auth.go:94-102` (mount `GET /api/auth/me`), `:195-204` & `:315-324` (populate `Lib[]` via `ACLStore`)
- Create: `api/internal/handlers/auth/me.go` (the `/me` handler) if not present
- Test: `api/internal/handlers/auth/auth_test.go`, `api/internal/auth/middleware/middleware_test.go`, an integration test for anonymous rejection
- Reference (read): `api/internal/auth/principal/principal.go:127-135` (`RequireAuth` — already exists), `api/internal/auth/authz/acl.go:17-33` (`ACLStore.LibrariesFor` — already exists)

### Task R3.1: Populate JWT `Lib[]` from `ACLStore` at mint time

- [ ] **Step 1: Failing test**

In `api/internal/handlers/auth/auth_test.go`, add a test: a user with two `library_acl` rows, call `Login`, decode the minted access token, assert `claims.Lib` == those two library IDs (not empty).

- [ ] **Step 2: Run — expect fail**

Run: `cd api && go test ./internal/handlers/auth/ -run TestLoginPopulatesLibClaim`
Expected: FAIL — `Lib` is `[]string{}` (`auth.go:201`).

- [ ] **Step 3: Implement**

Add an `ACL *authz.ACLStore` field to the auth `Handler`; wire it in `router.MountP9` deps (`api/internal/router/p9.go`). In `Login` (`auth.go:~195`) and `Refresh` (`auth.go:~315`), replace `Lib: []string{}` with:
```go
libs, err := h.ACL.LibrariesFor(r.Context(), u.ID)
if err != nil { /* writeProblem 500 */ return }
// ...
Lib: libs,
```

- [ ] **Step 4: Run — expect pass**

Run: `cd api && go test ./internal/handlers/auth/ -run TestLoginPopulatesLibClaim`
Expected: ok. Also run the streaming claims test to confirm `LibIDs()`/`CoversLibrary()` now succeed for a real token.

- [ ] **Step 5: Commit**

```bash
git add api/internal/handlers/auth/auth.go api/internal/router/p9.go api/internal/handlers/auth/auth_test.go
git commit -m "fix(api): mint JWT lib[] from ACLStore (login + refresh)"
```

### Task R3.2: Mount `RequireAuth` on business routes + `CookieAuth` globally

- [ ] **Step 1: Failing integration test**

Add `api/serve_auth_test.go` (or extend existing serve test): boot the router, `GET /api/libraries` with no credentials → expect 401/403. With a valid bearer token → expect 200.

- [ ] **Step 2: Run — expect fail (anonymous reaches route → 200)**

Run: `cd api && go test . -run TestBusinessRoutesRequireAuth`
Expected: FAIL — anonymous currently gets through (no gate mounted).

- [ ] **Step 3: Wire `CookieAuth` and `RequireAuth`**

- In `main.go:253-261`, stop discarding `p9`: keep the `*auth.Handler` and install `publicMux`/router middleware `p9.CookieAuth` ahead of business handlers (after `applySecurity` attaches JWT/Admin principals, before route dispatch — confirm chi ordering).
- In `auth_bootstrap.go` / router setup, wrap the business route group with `principal.RequireAuth` (the helper at `principal.go:127-135`). Leave genuinely public routes (`/healthz`, `/api/auth/login`, `/api/auth/refresh`, signed-URL streaming endpoints) OUT of the gate — enumerate the public allowlist explicitly in code with a comment.

- [ ] **Step 4: Run — expect pass**

Run: `cd api && go test . -run TestBusinessRoutesRequireAuth` → ok.
Run full: `cd api && go test ./...` → ok (fix any tests that implicitly relied on the old anonymous access by adding a test principal).

- [ ] **Step 5: Commit**

```bash
git add api/main.go api/auth_bootstrap.go api/serve_auth_test.go
git commit -m "fix(api): mount RequireAuth on business routes; install CookieAuth"
```

### Task R3.3: Mount `GET /api/auth/me`

- [ ] **Step 1: Failing test** — `GET /api/auth/me` with valid token returns `{user_id, is_admin, libraries}`; without token returns 401.
- [ ] **Step 2: Run — expect 404** (route unmounted).
- [ ] **Step 3: Implement** `Handler.Me` reading `principal.FromContext`, returning the principal projection; register `r.Get("/api/auth/me", h.Me)` in `Handler.Mount` (`auth.go:94-102`), inside the `RequireAuth` group.
- [ ] **Step 4: Run — expect pass.**
- [ ] **Step 5: Commit** `feat(api): add GET /api/auth/me`.

### Task R3.4: Merge R3 into integration

- [ ] Run `cd api && go test ./...`. Merge `<R3-branch>` into `integration/gap-closure` (no-ff). If V's `test_unauthenticated_business_route_is_rejected` was xfail'd, remove the xfail now and confirm it passes against the integrated stack.

---

## TRACK R4 — Web↔API contract reconciliation

**Files:**
- Modify: `web/src/pages/Search.tsx:42` (POST + JSON body + response mapping)
- Modify: `web/src/pages/VideoPlayer.tsx:29` (perform `POST /api/stream/sessions` handshake)
- Modify: `web/src/lib/api.ts` (only if a typed helper is needed — keep changes minimal)
- Modify: `api/internal/handlers/devices/devices.go` (redact `push_token`)
- Create: `web/src/lib/api.test.ts`, `web/src/pages/Search.test.tsx` (first real web tests)
- Test (Go): `api/internal/handlers/devices/devices_test.go`
- Reference (read): `api/internal/handlers/search/search.go:109`, `streaming/.../streaming.go:113-123`, `api/internal/handlers/settings/settings.go:203-216` (redaction precedent)

### Task R4.1: Fix Search to POST JSON and map the response

- [ ] **Step 1: Failing web test**

Create `web/src/pages/Search.test.tsx` (Vitest + Testing Library): mock `api.post`, render `Search`, submit a query, assert it calls `api.post('/api/search', { q, mode, limit })` and renders hits from the server `Response { hits, total }` shape.

- [ ] **Step 2: Run — expect fail**

Run: `cd web && pnpm vitest run src/pages/Search.test.tsx`
Expected: FAIL — component currently calls `api.get('/api/search?...')`.

- [ ] **Step 3: Implement**

In `Search.tsx:42`, replace the `api.get(\`/api/search?${params}\`)` call with `api.post<SearchResponse>('/api/search', { q, mode, limit })`, and map the server `Hit` struct fields to the UI `SearchHit` type (read `search/search.go` for exact field names: `hits[].{...}`, `total`, `took_ms`, `mode`).

- [ ] **Step 4: Run — expect pass.** `cd web && pnpm vitest run src/pages/Search.test.tsx` → pass.
- [ ] **Step 5: Commit** `fix(web): search uses POST /api/search with JSON body + correct response mapping`.

### Task R4.2: VideoPlayer performs the streaming session handshake

- [ ] **Step 1: Failing test** — mock `api`, render `VideoPlayer`, assert it calls `api.post('/api/stream/sessions', { video_id, ... })` then uses `manifest_url`/`direct_url` from `OpenSessionResponse`; assert it does NOT call the old `GET /api/videos/{id}/stream`.
- [ ] **Step 2: Run — expect fail** (component calls unmounted GET).
- [ ] **Step 3: Implement** the handshake in `VideoPlayer.tsx:29`: `POST /api/stream/sessions` with `{ video_id, client_profile }`, read `session_id` + `manifest_url`/`direct_url` + `expires_at` from the response (fields per `streaming.go:33-39`), feed the URL to the player, and post progress to `/api/stream/sessions/{id}/progress`.
- [ ] **Step 4: Run — expect pass.**
- [ ] **Step 5: Commit** `fix(web): VideoPlayer performs POST /api/stream/sessions handshake`.

### Task R4.3: Redact `push_token` in `GET /api/devices`

- [ ] **Step 1: Failing Go test**

In `api/internal/handlers/devices/devices_test.go`: call `List` with a device whose `push_token="secrettoken"`; assert the JSON response has `push_token` == `"<redacted>"` and `push_token_present` == `true`, and the raw secret does not appear in the body.

- [ ] **Step 2: Run — expect fail** (plaintext token in body). `cd api && go test ./internal/handlers/devices/ -run TestListRedactsPushToken`.

- [ ] **Step 3: Implement**

In `devices.go` `List` (~171-202), stop serializing the raw `PushToken`. Either change the `Device` API struct so `PushToken` is replaced with `"<redacted>"` and add `PushTokenPresent bool json:"push_token_present"`, following the precedent in `settings.go:203-216` (`redactSecrets`). Do not change the DB query/storage — redaction is at the response boundary only.

- [ ] **Step 4: Run — expect pass.**
- [ ] **Step 5: Commit** `fix(api): redact push_token in GET /api/devices (Story 12.10)`.

### Task R4.4: Bootstrap real web test infra + merge R4

- [ ] **Step 1:** Add a `vitest.config.ts` with jsdom env + Testing Library setup if absent; change `web/package.json` `test:unit` from `vitest run --passWithNoTests` to `vitest run` (so an empty suite fails, mirroring V's philosophy). Confirm the two new tests are collected.
- [ ] **Step 2:** Run `cd web && pnpm run build && pnpm run test:unit` → build ok, tests pass.
- [ ] **Step 3:** Run `cd api && go test ./internal/handlers/devices/` → ok.
- [ ] **Step 4:** Merge `<R4-branch>` into `integration/gap-closure` (no-ff). R4 touches `web/**` + `api/internal/handlers/devices/**` — no overlap with R1/R2/R3/V (search/streaming handlers read-only).

---

## Wave 0 completion

- [ ] **Step 1: Full integration verification**

On `integration/gap-closure` with all 5 tracks merged:
```bash
docker compose -f deploy/compose/docker-compose.yml up -d --wait
make test-unit
make test-e2e
make perf-ci
docker compose -f deploy/compose/docker-compose.yml down
```
Expected: all green, with the e2e gate now asserting real flows (health + auth rejection) and perf-ci asserting the budget subset.

- [ ] **Step 2: Manual end-to-end sanity**

Bring up the stack, drop a sample video into a watched library, confirm: scanner enqueues → pipeline runs real stages → transcript + subtitles + index produced; `GET /api/libraries` requires auth; web search returns hits via POST; video plays via the session handshake; `GET /api/devices` shows `<redacted>`.

- [ ] **Step 3: Update Linear**

Move the Wave 0 issues to Done with evidence links (HLB-355/257/259/283/307/365/335 R1; HLB-325/293/298 R2; HLB-385/386/387/391/301/270 R3; HLB-262/266/274/299/313 R4; HLB-383/327/397 V). Per spec §5, do NOT open a per-wave PR — work accumulates on `integration/gap-closure` for the single final PR.

- [ ] **Step 4: Per spec execution policy (auto-continue), proceed to plan Wave 1** (Epic 17 design system + standalone correctness) using this same skill, branching new worktrees off the now-updated `integration/gap-closure`.

---

## Self-Review

**Spec coverage:** R1→§4 W0-R1, R2→§4 W0-R2, R3→§4 W0-R3, R4→§4 W0-R4, V→§4 W0-V; branch topology §5; V-first + per-track gate §6; one-final-PR + no Wave-0 checkpoint §5/§6 reflected in "Wave 0 completion" steps 3-4. No spec section for Wave 0 left unmapped.

**Placeholder scan:** No "TBD/TODO/implement later". Repetitive items (R1.2 five stage adapters, R4.2/R4.3 abbreviated TDD) give exact lib entry points, signatures, file:line, and the concrete pattern from a fully-worked sibling task (R1.1, R4.1) — not "similar to Task N". R2 explicitly scopes deferred RPCs rather than leaving them vague.

**Type consistency:** `build_real_dispatch() -> dict[Stage, StageHandler]` used consistently (R1.1, R1.3). `jsoncodec.Codec` methods `Marshal/Unmarshal/Name` consistent across R2.1/R2.2/R2.3. `ACLStore.LibrariesFor` signature matches the investigated `acl.go:17-33`. `principal.RequireAuth` / `Handler.CookieAuth` match investigated signatures.

**Known assumptions flagged inline:** exact `perf_budgets.yaml` keys (V.3), exact `audio/probe.py` ffprobe entrypoint (R1.1), Go module boundaries for codec reuse (R2.3) — each step instructs the implementer to read the real file and adjust, rather than asserting an unverified API.
