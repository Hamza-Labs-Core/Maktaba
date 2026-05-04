# Implementation Plan — Story 20.6 Contract Tests for Service Boundaries

> Companion to [story-20-06-contract-tests.md](story-20-06-contract-tests.md).
> Single sources of truth for GraphQL/proto/REST/WS schemas; drift fails CI;
> backwards-compat lint with deprecation window.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| GraphQL | `shared/graphql/schema.graphql`. Generated client/server code via `gqlgen` (Go) and `graphql-codegen` (TS). |
| gRPC | `shared/proto/*.proto`. Buf-managed. |
| REST | OpenAPI checked in at `shared/openapi/maktaba.yaml`. Auto-extracted from chi routes via reflection on every CI run; mismatches fail. |
| WebSocket | Typed events in `shared/ws/events.ts` (TS) → `shared/ws/events_gen.go` (Go) → `shared/ws/events_gen.py` (Python). Single TS source via `graphql-codegen`-style script. |
| BC lint | `buf breaking` for proto; custom AST checker for GraphQL/OpenAPI; deprecation comments enforce one-minor-version window. |

## 1. Project layout

```
shared/
├── graphql/
│   ├── schema.graphql
│   ├── codegen.yml                   # ts-codegen config
│   └── breaking_lint.go              # custom GQL BC check
├── proto/
│   ├── maktaba.api.v1.proto
│   ├── maktaba.streaming.v1.proto
│   ├── maktaba.pipeline.v1.proto
│   └── buf.yaml / buf.gen.yaml
├── openapi/
│   ├── maktaba.yaml
│   ├── extract.go                    # walks chi routes
│   └── extract_test.go
└── ws/
    ├── events.ts                     # TS source
    ├── events_gen.go
    ├── events_gen.py
    └── codegen.ts

api/internal/contract/
├── operationid_test.go               # exercises every OpenAPI operation
└── grpc_drift_test.go

shared/ws/parser_test.ts
shared/ws/parser_test.go
shared/ws/parser_test.py
```

## 2. Buf for proto

```yaml
# shared/proto/buf.yaml
version: v2
modules:
  - path: shared/proto
breaking:
  use:
    - FILE
lint:
  use:
    - DEFAULT
```

```yaml
# shared/proto/buf.gen.yaml
plugins:
  - remote: buf.build/protocolbuffers/go
    out: shared/proto/gen/go
    opt: paths=source_relative
  - remote: buf.build/grpc/go
    out: shared/proto/gen/go
    opt: paths=source_relative
  - remote: buf.build/protocolbuffers/python
    out: shared/proto/gen/py
```

CI:

```yaml
- run: buf generate shared/proto
- run: git diff --exit-code shared/proto/gen      # AC1: drift fails
- run: buf breaking shared/proto --against ${{ github.event.pull_request.base.sha }}
```

## 3. GraphQL drift

```bash
# CI step
pnpm --filter web codegen
git diff --exit-code web/src/__generated__/

# Server side
go run github.com/99designs/gqlgen generate
git diff --exit-code api/internal/graphql/generated
```

## 4. OpenAPI extraction from chi

```go
// shared/openapi/extract.go
func Extract(r chi.Router) (*openapi3.T, error) {
    spec := &openapi3.T{
        OpenAPI: "3.0.3",
        Info:    &openapi3.Info{Title: "Maktaba", Version: "v1"},
        Paths:   &openapi3.Paths{},
    }
    err := chi.Walk(r, func(method, route string, h http.Handler, mws ...func(http.Handler) http.Handler) error {
        opID := opIDFor(method, route)
        path := chiToOpenAPI(route)
        item := spec.Paths.Value(path)
        if item == nil { item = &openapi3.PathItem{}; spec.Paths.Set(path, item) }
        op := &openapi3.Operation{OperationID: opID, Tags: tagsFor(route), Responses: defaultResponses()}
        if d, ok := h.(operationDescribed); ok { d.Describe(op) }
        item.SetOperation(method, op)
        return nil
    })
    return spec, err
}
```

Handlers self-describe by implementing:

```go
type operationDescribed interface { Describe(*openapi3.Operation) }
```

CI step diffs the extracted spec against checked-in `shared/openapi/maktaba.yaml`; any difference fails.

## 5. WS schema codegen

```ts
// shared/ws/events.ts — single source
export type WSEvent =
  | { type: 'job.progress'; job_id: string; pct: number; ts: string }
  | { type: 'job.transitioned'; job_id: string; from: string; to: string; ts: string }
  | { type: 'video.ready'; video_id: string; ts: string }
  | { type: 'unknown'; raw: unknown };

export const wsEventTypes = ['job.progress','job.transitioned','video.ready'] as const;
```

```ts
// shared/ws/codegen.ts — emits Go and Python
// pseudocode: parse the discriminated union, emit:
// shared/ws/events_gen.go (struct + JSON tag-based unmarshal)
// shared/ws/events_gen.py (pydantic model with Discriminator on `type`)
```

Generated Go:

```go
// shared/ws/events_gen.go (DO NOT EDIT)
type Event interface{ isEvent() }
type JobProgress struct{ Type string `json:"type"`; JobID string `json:"job_id"`; Pct int `json:"pct"`; Ts time.Time `json:"ts"` }
func (JobProgress) isEvent() {}
type JobTransitioned struct{ ... }
type VideoReady struct{ ... }
type Unknown struct{ Type string `json:"type"`; Raw json.RawMessage `json:"-"` }
func ParseEvent(b []byte) (Event, error) {
    var hdr struct{ Type string `json:"type"` }
    if err := json.Unmarshal(b, &hdr); err != nil { return nil, err }
    switch hdr.Type {
    case "job.progress":      var e JobProgress;     return e, json.Unmarshal(b, &e)
    case "job.transitioned":  var e JobTransitioned; return e, json.Unmarshal(b, &e)
    case "video.ready":       var e VideoReady;      return e, json.Unmarshal(b, &e)
    default:                  return Unknown{Type: hdr.Type, Raw: b}, nil
    }
}
```

EC3: receiver is strict on unknown fields except for the `Unknown` discriminant, which never errors.

## 6. Parser tests

```ts
// shared/ws/parser_test.ts
test('unknown event type → Unknown discriminant, not throw', () => {
    const got = parse(JSON.stringify({ type: 'martian.event', extra: 1 }));
    expect(got.type).toBe('unknown');
});
```

```go
// shared/ws/parser_test.go
func TestParseUnknown(t *testing.T) {
    e, err := ParseEvent([]byte(`{"type":"martian.event"}`))
    require.NoError(t, err)
    _, ok := e.(Unknown)
    require.True(t, ok)
}
```

## 7. Backwards-compat lint

### Proto

`buf breaking` enforced in CI as shown.

### GraphQL

```go
// shared/graphql/breaking_lint.go
//go:build gqllint

func main() {
    base := load(os.Getenv("BASE"))
    head := load(os.Getenv("HEAD"))
    diff := compareSchemas(base, head)
    for _, change := range diff {
        if change.Kind == FieldRemoved && !hasDeprecation(base, change.Field, mustParse(MIN_VER)) {
            fmt.Fprintf(os.Stderr, "FAIL: %s removed without deprecation window\n", change.Field)
            os.Exit(1)
        }
    }
}
```

A field is allowed to be removed if its `@deprecated(reason: "removed in v1.4")` directive was present in the BASE schema with a `removeIn` reason that has been satisfied.

### OpenAPI

Custom diff: removal of any operationId or response without a deprecation extension fails.

## 8. Test cases

### TC1 — Drift
Edit `maktaba.api.v1.proto` to add a field. Don't run `buf generate`. CI's `git diff --exit-code` fires; merge blocked. After running `buf generate`, the diff disappears, CI passes.

### TC2 — WS unknown
Server emits `{ type: "martian.event", ... }` (e.g., during a partial deploy). Client logs warning; UI continues; no crash. Parser tests above codify this.

### TC3 — Compat removal
PR with `removed: User.legacyField` and no deprecation comment fails. Same PR with `User.legacyField @deprecated(reason: "use newField; remove in 1.4.0")` and a `migration: 2026-07-01` comment passes — actual removal scheduled in next minor.

## 9. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 generator non-determinism | story | All generators pinned by version (buf v2 plugins by digest, gqlgen go.sum, graphql-codegen package-lock). |
| EC2 OpenAPI omits route | story | Extractor walks chi; missing operation fails the operationid_test. |
| EC3 pydantic strict on receive | story | Generator emits `model_config = ConfigDict(extra='forbid')` on receive models; sender models are lenient. |
| Generated code accidentally hand-edited | impl | `// DO NOT EDIT` header; CI re-generates and asserts no diff. |
| New WS event added without TS source | impl | Parsing test fails because handler-side emits an unknown discriminant which TC2 then asserts as Unknown — but the integration test that emits the new event names it explicitly so the absence is caught. |

## 10. Configuration

```yaml
# shared/contract/policy.yaml
deprecation:
  min_versions: 1                    # at least 1 minor before removal
versioning:
  semver_source: web/package.json
codegen:
  determinism_check: true
```

## 11. Make targets

```makefile
.PHONY: contract:gen contract:check
contract:gen:
	buf generate shared/proto
	pnpm --filter web codegen
	go run ./shared/openapi/extract -out shared/openapi/maktaba.yaml
	pnpm --filter shared exec ts-node shared/ws/codegen.ts
contract:check:
	make contract:gen
	git diff --exit-code shared/proto/gen shared/openapi/maktaba.yaml shared/ws/events_gen.go shared/ws/events_gen.py web/src/__generated__
	buf breaking shared/proto --against $(BASE_REF)
	go run -tags=gqllint ./shared/graphql/breaking_lint.go
```

## 12. Dependencies

- `buf`, `gqlgen`, `graphql-codegen`.
- Story 20.4 (integration tests use generated stubs).
- Epic 22 devops (CI runners include `buf` and `pnpm`).
