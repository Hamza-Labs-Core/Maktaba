# Story 7.17 — GraphQL schema + resolvers

Schema-first via `gqlgen`; resolvers wrap the same domain code as REST
(§9 intro). Subscriptions are WebSocket-based and reuse the §7.16
fan-out.

**AC-1 — Domain types in the schema.**
- **Given** the `shared/graphql/schema.graphql`,
- **When** parsed,
- **Then** it contains `Library`, `Video`, `MediaInfo`, `AudioTrack`,
  `Transcript`, `Segment`, `Word`, `Subtitle`, `Chapter`, `Tag`,
  `Collection`, `Speaker`, `Job`, `StreamingSession`, `User`,
  `PlaybackState`, `SearchResult`, `SearchHit`, `SearchMatch`,
  `Recommendation`, `Device` and a matching set of input types for
  mutations.

**AC-2 — Query + Mutation parity with REST.**
- **Given** every REST endpoint listed in §9.1–9.7 plus the endpoints
  introduced by Stories 7.21 (`recommendations`), 7.22 (`devices`), and
  the auth-pair endpoint owned by Epic 10 Story 10.17,
- **When** the schema is read,
- **Then** there is a corresponding `Query` field (for reads) or
  `Mutation` field (for writes) implemented by the same domain function
  the REST handler calls. Parity is enforced by a `parity_test` that
  compares the `chi` route table against the schema's resolver list and
  fails on additions to either side without a matching counterpart.

**AC-3 — Subscription parity with WebSocket.**
- **Given** every channel in story 7.16,
- **When** the schema is read,
- **Then** there are `Subscription.jobUpdates`, `Subscription.libraryEvents
  (libraryId)`, and `Subscription.playbackUpdates(videoId)` resolvers
  that filter the same NOTIFY stream.

**AC-4 — DataLoader against N+1.**
- **Given** a query asking for 100 videos with `media_info` and `audio_tracks`,
- **When** the resolver runs,
- **Then** at most 3 SQL queries are issued (videos, media_info bulk,
  audio_tracks bulk), proven by a query-counting test.

**AC-5 — Persisted queries.**
- **Given** a client that POSTs `{persistedQueryId}` instead of `{query}`,
- **When** the API has the query in its persisted-store,
- **Then** the server resolves it; otherwise returns
  `PersistedQueryNotFound` with the standard Apollo shape and the client
  retries with the full query.

**Test cases:**
- Schema test: `gqlgen` generation runs in CI; a missing resolver fails
  the build.
- Integration: a query returning 1000 videos uses ≤4 SQL round trips.
- Integration: subscription receives the same job-progress events as
  the REST WS endpoint over the same fixture.
- Integration: a malformed query returns a GraphQL error envelope (not
  problem+json) with `extensions.code` set.
- Parity: adding a new REST endpoint without a matching GraphQL
  counterpart causes the parity test to fail in CI.

**Edge cases:**
- A field selection that asks for `playbackState` on a video the user
  hasn't watched — return `playbackState: null`, not an error.
- Subscriptions over the same WS connection share one Postgres listener
  per channel (multiplexed) — confirmed by a load test that opens 100
  subscriptions and asserts only one `LISTEN jobs.*` per replica.
- Mutation that fails partway (e.g. tag patch with one valid + one
  invalid tag) — entire mutation is one transaction; on failure no tags
  are changed and the response carries the per-tag error array.
- Field-level cost limit — a query that requests `transcripts.segments`
  with `first: 100000` is rejected with `cost-limit-exceeded`.
