# Implementation Plan — Story 20.4 Integration Tests with Real Backends

> Companion to [story-20-04-integration-tests.md](story-20-04-integration-tests.md).
> Real Postgres/ChromaDB/FFmpeg; cross-service event flow; replay tapes for SaaS.
> No mocks at our own service boundaries.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Postgres | `testcontainers-go` and `testcontainers-python`, image `postgres:16.4-alpine3.20` (canonical pin across all 20-x plans). |
| ChromaDB | Python integration tests run a `chromadb`-server subprocess; Go tests treat it as a side resource via the API. |
| FFmpeg | Real subprocess; version-checked at startup. |
| Cross-service | gRPC: the Pipeline service is implemented in Python (`grpc.aio`), so we cannot run real Pipeline handlers over a Go bufconn. Story AC4 forbids mocks at boundaries, so the Go integration tier launches the **real Python Pipeline binary as a subprocess** and dials it over TCP on a random port. The test's Postgres + ChromaDB are shared between both processes. |
| SaaS replay | `httptape` cassette format under `tests/integration/replays/`. |

## 1. Project layout

```
shared/integration/
├── containers/
│   ├── postgres.go                 # //go:build integration && !embedded
│   ├── postgres_state.go           # //go:build integration  (shared pgOnce/pgURL)
│   ├── chroma.py
│   └── pgembed_fallback.go         # //go:build integration && embedded (EC1)
├── ffmpeg_check.go
├── replay/
│   ├── tape.go
│   ├── tape_test.go
│   └── replays/
│       ├── openai_whisper_arabic_60s.yaml
│       └── ...
└── version_pins.go                 # postgres image tag, ffmpeg min ver

api/tests/integration/
├── transcribe_e2e_test.go          # API → real Python pipeline subprocess → DB → WS
└── pipelineserver_subprocess.go    # SpawnPython helper

pipeline/tests/integration/
├── transcribe_test.py
├── chroma_upsert_test.py
└── conftest.py

Makefile
```

## 2. Postgres reuse + per-test isolation

```go
// shared/integration/containers/postgres.go
//go:build integration && !embedded

// pgOnce / pgURL are declared in postgres_state.go (compiled in both
// build configurations) so they are shared with the embedded fallback.

func PostgresURL(t *testing.T) string {
    pgOnce.Do(func() {
        ctx := context.Background()
        pg, err := postgres.RunContainer(ctx,
            postgres.WithImage("postgres:16.4-alpine3.20"),
            postgres.WithDatabase("test"),
            postgres.WithUsername("test"),
            postgres.WithPassword("test"),
            postgres.BasicWaitStrategies(),
        )
        if err != nil { panic(err) }
        host, _ := pg.Host(ctx)
        port, _ := pg.MappedPort(ctx, "5432/tcp")
        pgURL = fmt.Sprintf("postgres://test:test@%s:%s/test?sslmode=disable", host, port.Port())
        runMigrations(pgURL)
    })
    return pgURL
}

// WithTx opens a transaction against the shared Postgres test container and
// rolls it back at end-of-test. Returns *sql.Tx directly — the previous
// `wrapTxAsDB(tx)` returned `*sql.DB`, which is type-incompatible (you can't
// promote a Tx into a DB without a custom interface). Consumers that
// previously took a *sql.DB are migrated to take a small `txnRunner`
// interface implemented by both *sql.DB and *sql.Tx.
func WithTx(t *testing.T) *sql.Tx {
    db, err := sql.Open("pgx", PostgresURL(t))
    if err != nil { t.Fatal(err) }
    tx, err := db.BeginTx(context.Background(), nil)
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { _ = tx.Rollback() })
    return tx
}

// txnRunner is the subset of *sql.DB / *sql.Tx that production code uses.
// All consumers in integration tests accept this interface.
type txnRunner interface {
    QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
    QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
    ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}
```

Per-test isolation is via savepoints; container is reused for the whole test binary.

## 3. Pipeline integration fixtures (Python)

The Python pipeline integration tier brings up ChromaDB **and** the same
real Postgres + FFmpeg the Go side uses (story-20-04 AC2: integration tests
must touch the real backends, not stand-ins). Postgres is shared with the
Go suite via `testcontainers-python` pointing at the same image pin
(`postgres:16.4-alpine3.20`); FFmpeg is invoked from `$PATH` with a
version check matching `MustFFmpegSupported` in §4.

```python
# pipeline/tests/integration/conftest.py
import shutil
import subprocess
from urllib.parse import urlparse

import chromadb
import pytest
from testcontainers.postgres import PostgresContainer

from shared.integration.versions import POSTGRES_IMAGE, FFMPEG_MIN


@pytest.fixture(scope="session")
def postgres_url():
    """Real Postgres via testcontainers, pinned image."""
    with PostgresContainer(POSTGRES_IMAGE) as pg:
        run_migrations(pg.get_connection_url())
        yield pg.get_connection_url()


@pytest.fixture(scope="session")
def ffmpeg_bin():
    """FFmpeg from PATH; assert version >= FFMPEG_MIN."""
    path = shutil.which("ffmpeg")
    if not path:
        pytest.skip("ffmpeg not in PATH")
    out = subprocess.check_output([path, "-version"], text=True)
    # e.g. "ffmpeg version 6.1.2 ..."
    ver = out.split()[2]
    if ver < FFMPEG_MIN:
        pytest.fail(f"ffmpeg {ver} < {FFMPEG_MIN}")
    return path


@pytest.fixture(scope="session")
def chroma_server(tmp_path_factory):
    data = tmp_path_factory.mktemp("chroma")
    p = subprocess.Popen([
        "chroma", "run", "--path", str(data), "--port", "0",
    ], stdout=subprocess.PIPE)
    port = _wait_for_port(p)
    yield f"http://localhost:{port}"
    p.terminate(); p.wait()


@pytest.fixture
def chroma(chroma_server):
    return chromadb.HttpClient(host="localhost", port=urlparse(chroma_server).port)
```

## 4. FFmpeg version check (EC3)

```go
// shared/integration/ffmpeg_check.go
const minFFmpeg = "6.1"

func MustFFmpegSupported(t *testing.T) {
    out, err := exec.Command("ffmpeg", "-version").Output()
    if err != nil { t.Fatalf("ffmpeg not in PATH: %v", err) }
    re := regexp.MustCompile(`ffmpeg version (\d+\.\d+)`)
    m := re.FindStringSubmatch(string(out))
    if m == nil { t.Fatalf("can't parse ffmpeg version") }
    if semverLess(m[1], minFFmpeg) {
        t.Fatalf("ffmpeg %s < %s", m[1], minFFmpeg)
    }
}
```

## 5. EC1 — pg-embed fallback

The two implementations of `PostgresURL` (testcontainers vs. embedded
Postgres) MUST be guarded by mutually exclusive build tags so the two
files never compile together. We use the `embedded` tag — `postgres.go`
gets `!embedded` and the fallback gets `embedded`. The `pgOnce`/`pgURL`
package-level variables are declared in a third file (`postgres_state.go`)
that compiles unconditionally so both implementations share them without
duplicate declarations.

```go
// shared/integration/containers/postgres.go
//go:build integration && !embedded

// (the testcontainers-backed PostgresURL above)
```

```go
// shared/integration/containers/pgembed_fallback.go
//go:build integration && embedded

func PostgresURL(t *testing.T) string {
    pgOnce.Do(func() {
        cfg := embeddedpostgres.DefaultConfig().Version(embeddedpostgres.V16).Port(0)
        pg := embeddedpostgres.NewDatabase(cfg)
        if err := pg.Start(); err != nil { panic(err) }
        pgURL = cfg.GetConnectionURL()
        runMigrations(pgURL)
    })
    return pgURL
}
```

```go
// shared/integration/containers/postgres_state.go
//go:build integration

// Shared state for both PostgresURL implementations. Compiled in either
// build configuration; only one PostgresURL is compiled at a time so there
// is no duplicate type / variable declaration.
var (
    pgOnce sync.Once
    pgURL  string
)
```

Build tag `embedded` selects the in-process embedded Postgres path. CI
honours `CI_NO_DOCKER=1` env, which sets `-tags="integration embedded"`.

## 6. Cross-service gRPC test

```go
// api/tests/integration/transcribe_e2e_test.go
//go:build integration

// We cannot use bufconn against the Pipeline service because Pipeline is
// Python (grpc.aio). Story-20-04 AC4 forbids mocks at service boundaries,
// so we launch the real Python pipeline binary as a subprocess and dial it
// over TCP. PipelineSubprocess.Conn() returns a *grpc.ClientConn pointed at
// the spawned process; t.Cleanup terminates it.
func TestTranscribeFlow(t *testing.T) {
    db := containers.WithTx(t)
    pipe := pipelineserver.SpawnPython(t,        // exec uvicorn maktaba_pipeline.grpc_main
        pipelineserver.WithChromaURL(chromaURL),
        pipelineserver.WithPostgres(containers.PostgresURL(t)),
    )
    api  := apiserver.New(db, pipe.Conn())

    // Open WS client subscribed to job events.
    ws := wsclient.Dial(t, api.WSURL())
    defer ws.Close()

    // Enqueue.
    res := api.Post(t, "/api/jobs", &api.EnqueueIn{Kind: "transcribe", VideoID: seed.Video.ID})
    require.Equal(t, "accepted", res.Status)

    // Wait for done event with deadline.
    ev := ws.WaitFor(t, "job.transitioned",
        func(e wsclient.Event) bool { return e.JobID == res.JobID && e.State == "done" },
        30*time.Second)
    require.Equal(t, "done", ev.State)

    // DB rows. The canonical schema is `transcript_segments(transcript_id,...)`
    // joined to `videos` via `transcripts.video_id` — there is no
    // `segments` table and no `video_id` column on `transcript_segments`.
    var n int
    require.NoError(t, db.Get(&n, `
        SELECT COUNT(*)
        FROM transcript_segments ts
        JOIN transcripts t ON t.id = ts.transcript_id
        WHERE t.video_id = $1
    `, seed.Video.ID))
    require.GreaterOrEqual(t, n, 1)
}
```

## 7. Replay tapes for SaaS

The tape format stores response bytes base64-encoded so that binary payloads
(e.g. Whisper streaming responses) round-trip through YAML without escape
fragility. For streaming responses (Whisper transcription, OpenAI SSE) the
tape records one entry per chunk with a `ts_offset_ms` so the replay server
can re-emit chunks at their original cadence:

```go
// shared/integration/replay/tape.go
type Tape struct {
    Name     string
    Requests []ReqResp
}

type ReqResp struct {
    Method, URL string
    ReqBody     string
    Status      int
    Header      map[string]string

    // For non-streaming responses: a single base64-encoded body.
    RespBodyB64 string `yaml:"resp_body_b64,omitempty"`

    // For streaming responses (Whisper, SSE): a list of chunks, each with
    // its offset (ms) from the start of the response. The replay server
    // emits each chunk and pauses for the delta between offsets so the
    // SUT observes timing similar to the real upstream.
    Chunks []Chunk `yaml:"chunks,omitempty"`
}

type Chunk struct {
    TsOffsetMs int    `yaml:"ts_offset_ms"`
    ChunkB64   string `yaml:"chunk_b64"`
}

func Server(t *testing.T, tapeFile string) *httptest.Server {
    t.Helper()
    tape := Load(tapeFile)
    idx := 0
    return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if idx >= len(tape.Requests) { http.Error(w, "tape exhausted", 500); return }
        rr := tape.Requests[idx]; idx++
        for k, v := range rr.Header { w.Header().Set(k, v) }
        w.WriteHeader(rr.Status)

        if len(rr.Chunks) > 0 {
            // Streaming response: emit chunks at recorded offsets.
            flusher, _ := w.(http.Flusher)
            start := time.Now()
            for _, c := range rr.Chunks {
                target := start.Add(time.Duration(c.TsOffsetMs) * time.Millisecond)
                if d := time.Until(target); d > 0 { time.Sleep(d) }
                b, _ := base64.StdEncoding.DecodeString(c.ChunkB64)
                _, _ = w.Write(b)
                if flusher != nil { flusher.Flush() }
            }
            return
        }

        // Non-streaming: single body.
        b, _ := base64.StdEncoding.DecodeString(rr.RespBodyB64)
        _, _ = w.Write(b)
    }))
}
```

Sample tape entry for a Whisper streaming response:

```yaml
# tests/integration/replays/openai_whisper_arabic_60s.yaml
name: openai_whisper_arabic_60s
requests:
  - method: POST
    url: /v1/audio/transcriptions
    status: 200
    header:
      content-type: text/event-stream
    chunks:
      - ts_offset_ms: 0
        chunk_b64: ZGF0YTogeyJ0...   # event 1, base64 of the SSE frame
      - ts_offset_ms: 320
        chunk_b64: ZGF0YTogeyJ0...   # event 2
      - ts_offset_ms: 640
        chunk_b64: ZGF0YTogW0RPTkVdCg==
```

Recording flag:

```text
MAKTABA_RECORD_TAPES=1   # passes through to real OpenAI; writes new tape files
```

PR review: re-recording is gated on a CI check that fails if any tape file was modified outside `tests/integration/replays/` and the PR description does not contain `re-recording: yes`.

## 8. Test cases

### TC1 — Spin-up budget
A fresh CI runner brings up Postgres + ChromaDB in ≤ 30 s. Asserted by the per-tier total wall-clock metric in CI logs.

### TC2 — Cross-service flow
`TestTranscribeFlow` (above) runs end-to-end without manual coordination;
the Go API server dials the Python Pipeline subprocess over TCP on a
random port (no bufconn against Python). All glue is in
`pipelineserver.SpawnPython`.

### TC3 — Tape determinism
Replay tape `openai_whisper_arabic_60s.yaml` is loaded twice in a row; both yield identical outputs (same JSON bytes from the SUT). Re-recording without the flag fails the build.

## 9. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 no Docker on runner | story | `nodocker` build tag → embedded Postgres + Python-backed Chroma. |
| EC2 PG version drift | story | Image tag `postgres:16.4-alpine3.20` pinned. Mismatch fails image-pull. |
| EC3 FFmpeg version | story | `MustFFmpegSupported` runs in `TestMain`. |
| Container leak across PR | impl | testcontainers session reaper enabled. |
| Tape exhaustion | impl | 500 with "tape exhausted" message; failing test reports the missing request. |

## 10. Configuration

```yaml
integration:
  postgres_image: "postgres:16.4-alpine3.20"
  ffmpeg_min: "6.1"
  chroma_min: "0.5"
  pipeline_subprocess_grace_sec: 10
```

## 11. Make targets

```makefile
test-integration:
	go test -tags=integration -timeout=8m ./...
	pytest -m integration -q

test-integration-nodocker:
	CI_NO_DOCKER=1 go test -tags="integration embedded" -timeout=10m ./...
	pytest -m integration -q --no-docker
```

## 12. Dependencies

- Story 20.1 (test tiers, runtime budget).
- Story 20.2 (fixtures used by integration).
- Story 20.6 (gRPC contract — `shared/proto/`).
- Epic 22 devops (CI runners).
