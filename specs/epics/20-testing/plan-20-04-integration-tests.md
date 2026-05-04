# Implementation Plan — Story 20.4 Integration Tests with Real Backends

> Companion to [story-20-04-integration-tests.md](story-20-04-integration-tests.md).
> Real Postgres/ChromaDB/FFmpeg; cross-service event flow; replay tapes for SaaS.
> No mocks at our own service boundaries.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Postgres | `testcontainers-go` and `testcontainers-python`, image `postgres:16` exact tag. |
| ChromaDB | Python integration tests run a `chromadb`-server subprocess; Go tests treat it as a side resource via the API. |
| FFmpeg | Real subprocess; version-checked at startup. |
| Cross-service | gRPC: API integration uses an in-process `bufconn` for the Pipeline server stub by default; one e2e flow uses TCP across two service binaries. |
| SaaS replay | `httptape` cassette format under `tests/integration/replays/`. |

## 1. Project layout

```
shared/integration/
├── containers/
│   ├── postgres.go
│   ├── chroma.py
│   └── pgembed_fallback.go        # EC1 fallback
├── ffmpeg_check.go
├── replay/
│   ├── tape.go
│   ├── tape_test.go
│   └── replays/
│       ├── openai_whisper_arabic_60s.yaml
│       └── ...
└── version_pins.go                 # postgres image tag, ffmpeg min ver

api/tests/integration/
├── transcribe_e2e_test.go          # API → pipeline gRPC → DB → WS
└── grpc_buffconn.go

pipeline/tests/integration/
├── transcribe_test.py
├── chroma_upsert_test.py
└── conftest.py

Makefile
```

## 2. Postgres reuse + per-test isolation

```go
// shared/integration/containers/postgres.go
//go:build integration

var (
    pgOnce sync.Once
    pgURL  string
)

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

func WithTx(t *testing.T) *sql.DB {
    db, _ := sql.Open("pgx", PostgresURL(t))
    tx, err := db.BeginTx(context.Background(), nil)
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { _ = tx.Rollback() })
    return wrapTxAsDB(tx)         // sqlx-style adapter
}
```

Per-test isolation is via savepoints; container is reused for the whole test binary.

## 3. ChromaDB subprocess (Python)

```python
# pipeline/tests/integration/conftest.py
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

```go
// shared/integration/containers/pgembed_fallback.go
//go:build integration && !docker

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

Build tag `nodocker` selects the embedded path. CI honours `CI_NO_DOCKER=1` env to set the tag.

## 6. Cross-service gRPC test

```go
// api/tests/integration/transcribe_e2e_test.go
//go:build integration

func TestTranscribeFlow(t *testing.T) {
    db := containers.WithTx(t)
    pipe := pipelineserver.NewBuf(t)            // bufconn-backed gRPC server with real handlers
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

    // DB rows.
    var n int
    require.NoError(t, db.Get(&n, `SELECT COUNT(*) FROM segments WHERE video_id=$1`, seed.Video.ID))
    require.GreaterOrEqual(t, n, 1)
}
```

## 7. Replay tapes for SaaS

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
    RespBody    string
    Header      map[string]string
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
        _, _ = w.Write([]byte(rr.RespBody))
    }))
}
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
`TestTranscribeFlow` (above) runs end-to-end without manual coordination; depends only on the `bufconn` plumbing.

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
  bufconn_buffer: 1048576
```

## 11. Make targets

```makefile
test-integration:
	go test -tags=integration -timeout=8m ./...
	pytest -m integration -q

test-integration-nodocker:
	CI_NO_DOCKER=1 go test -tags="integration nodocker" -timeout=10m ./...
	pytest -m integration -q --no-docker
```

## 12. Dependencies

- Story 20.1 (test tiers, runtime budget).
- Story 20.2 (fixtures used by integration).
- Story 20.6 (gRPC contract — `shared/proto/`).
- Epic 22 devops (CI runners).
