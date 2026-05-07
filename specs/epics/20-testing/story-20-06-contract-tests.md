# Story 20.6 — Contract tests for service boundaries

GraphQL, REST, gRPC, and WebSocket schemas are versioned; client and
server are tested against the same contract.

## Acceptance criteria

- AC1. `shared/graphql/schema.graphql` and `shared/proto/*.proto` are
  the single source of truth; CI fails if generated code drifts from
  the schema.
- AC2. REST contract is captured in OpenAPI (auto-generated from chi
  routes via reflection or a manual checked-in file); a contract test
  exercises every operationId.
- AC3. WebSocket events have a typed schema (TypeScript discriminated
  unions, Go structs, Python pydantic); a payload that doesn't match
  the schema fails the parser tests.
- AC4. Backwards-compatibility lint: removing a field from the schema
  fails CI; renaming requires a deprecation window of one minor
  version.

## Test cases

- TC1. Drift: edit a `.proto` field, regenerate; CI passes only when
  generated code is committed.
- TC2. WS schema: an unknown event type from the server is logged and
  surfaced as a typed `unknown` discriminant on the client, not a
  parse error that crashes the page.
- TC3. Compat: a removal-only PR fails; the same PR with a deprecation
  comment + a migration date passes (the deprecation is enforced at
  the next minor).

## Edge cases

- EC1. Generated code re-hash collision: code-gen tools must produce
  byte-stable output; a non-deterministic generator is replaced or
  pinned.
- EC2. OpenAPI auto-gen omits a route — the contract test fails because
  a documented operation is missing.
- EC3. Pydantic strict-mode mismatches — the WS schema parser is
  strict on receive; the sender is lenient.
