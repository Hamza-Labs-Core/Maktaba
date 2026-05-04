# Implementation Plan — Story 7.17 GraphQL Schema + Resolvers

> Companion to [story-07-17-graphql-schema.md](story-07-17-graphql-schema.md).
> Schema-first via `gqlgen`; resolvers wrap the same domain code as REST.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Generator | `github.com/99designs/gqlgen` (schema-first; type-safe resolvers). |
| Endpoint | `POST /graphql` for queries + mutations; `WS /graphql` for subscriptions (graphql-ws subprotocol). |
| Domain reuse | Resolvers call into the same `service` packages the REST handlers use. No duplicate business logic. |
| Persisted queries | Apollo persisted-query protocol (`persistedQueryId` → SHA256 → query body lookup). |
| DataLoader | Per-request `dataloaden` instances for N+1 prevention. |
| Out of scope | The REST handlers themselves (covered by Stories 7.3–7.16); the auth flow (Epic 10). |

## 1. Architecture diagram

```
              ┌─────────────────────────────┐
              │ shared/graphql/schema.graphql│   (committed)
              └────────────┬────────────────┘
                           │ gqlgen generate
                           ▼
              ┌─────────────────────────────┐
              │ api/internal/graph/generated/│   (gitignored gen output)
              │  - models                    │
              │  - exec stubs                │
              └────────────┬────────────────┘
                           │
                           ▼
              ┌─────────────────────────────┐
              │ api/internal/graph/resolver/ │
              │  - queryResolver             │
              │  - mutationResolver          │
              │  - subscriptionResolver      │
              │  delegates to:               │
              │    libraries.Service         │
              │    videos.Service            │
              │    search.Service            │
              │    streaming.Service ...     │
              └────────────┬────────────────┘
                           ▼
              ┌─────────────────────────────┐
              │ DataLoader (per-request)    │
              │  - mediaInfoByVideo          │
              │  - audioTracksByVideo        │
              │  - tagsByVideo               │
              │  - playbackByUserVideo       │
              └─────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `shared/graphql/schema.graphql` | Source of truth for the schema. |
| `api/internal/graph/gqlgen.yml` | Generator config. |
| `api/internal/graph/resolver/*.go` | Hand-written resolvers (one file per top-level type). |
| `api/internal/graph/loaders/*.go` | DataLoader instances. |
| `api/internal/graph/parity_test.go` | Asserts REST ↔ GraphQL parity. |
| `api/internal/graph/persisted/store.go` | Persisted-query lookup (in-memory + on-disk). |
| `api/internal/graph/cost.go` | Field cost limit middleware. |
| `api/internal/graph/handler_test.go` | Integration. |

## 3. Schema (sketch)

`shared/graphql/schema.graphql` (truncated, full file is generated):

```graphql
scalar UUID
scalar DateTime
scalar JSON

type Library {
  id: UUID!
  name: String!
  roots: [String!]!
  settings: JSON!
  createdAt: DateTime!
  updatedAt: DateTime!
  videos(first: Int, after: String, filter: VideoFilter): VideoConnection!
  stats: LibraryStats!
}

type Video {
  id: UUID!
  library: Library!
  title: String!
  description: String
  detectedLanguage: String
  contentType: String
  state: VideoState!
  path: String!
  playable: Boolean!
  mediaInfo: MediaInfo
  audioTracks: [AudioTrack!]!
  chapters: [Chapter!]!
  tags: [Tag!]!
  transcript: Transcript
  segments(from: Float, to: Float, first: Int, after: String): SegmentConnection!
  subtitles: [Subtitle!]!
  playbackState: PlaybackState
  jobs(stage: Stage): [Job!]!
  createdAt: DateTime!
  updatedAt: DateTime!
}

# (... all other domain types as in AC-1)

type Query {
  library(id: UUID!): Library
  libraries(first: Int, after: String): LibraryConnection!
  video(id: UUID!): Video
  videos(first: Int, after: String, filter: VideoFilter): VideoConnection!
  search(input: SearchInput!): SearchResult!
  searchSuggest(q: String!): [String!]!
  savedSearches: [SavedSearch!]!
  collections: [Collection!]!
  collection(id: UUID!): Collection
  jobs(filter: JobFilter): [Job!]!
  queueStats: QueueStats!
  settings: JSON!
  sttBackends: [STTBackend!]!
  recommendations(surface: String, limit: Int): RecommendationResponse!
  me: User!
  device(id: UUID!): Device
}

type Mutation {
  createLibrary(input: CreateLibraryInput!): Library!
  patchLibrary(id: UUID!, input: PatchLibraryInput!): Library!
  deleteLibrary(id: UUID!, purge: Boolean, confirm: String): Boolean!
  scanLibrary(id: UUID!): ScanResponse!

  patchVideo(id: UUID!, input: PatchVideoInput!): Video!
  deleteVideo(id: UUID!, purge: Boolean, confirm: String): Boolean!
  processVideo(id: UUID!, input: ProcessInput): ProcessResult!
  reprocessVideo(id: UUID!, fromStage: Stage!): ReprocessResult!

  pauseJob(id: ID!, force: Boolean): Job!
  resumeJob(id: ID!): Job!
  cancelJob(id: ID!): Job!
  retryJob(id: ID!): Job!

  saveSearch(input: SaveSearchInput!): SavedSearch!
  deleteSavedSearch(id: UUID!): Boolean!

  patchVideoTags(id: UUID!, delta: TagDeltaInput!): [Tag!]!
  mergeSpeakers(input: MergeSpeakersInput!): MergeResult!
  renameSpeaker(id: UUID!, name: String!): Speaker!

  openStreamingSession(input: OpenSessionInput!): StreamingSession!
  closeStreamingSession(id: UUID!): Boolean!
  reportProgress(sessionId: UUID!, input: ProgressInput!): Boolean!

  patchSettings(input: JSON!): JSON!
  testSTT(input: STTTestInput!): STTTestResponse!

  registerDevice(input: RegisterDeviceInput!): Device!
  unregisterDevice(id: UUID!): Boolean!
}

type Subscription {
  jobUpdates: JobEvent!
  libraryEvents(libraryId: UUID!): LibraryEvent!
  playbackUpdates(videoId: UUID!): PlaybackEvent!
}
```

## 4. Resolver scaffolding

```go
// api/internal/graph/resolver/resolver.go
package resolver

type Resolver struct {
    Libraries  *libraries.Service
    Videos     *videos.Service
    Search     *search.Service
    Streaming  *streaming.Service
    Jobs       *jobs.Service
    Settings   *settings.Service
    Recs       *recs.Service
    Devices    *devices.Service
    Hub        *ws.Hub
    Loaders    func(ctx context.Context) *loaders.Loaders
}

// Query/Mutation/Subscription resolvers attach methods to the embedded
// Resolver via gqlgen's generated interfaces.
```

```go
// api/internal/graph/resolver/video_resolver.go
package resolver

func (r *queryResolver) Video(ctx context.Context, id uuid.UUID) (*model.Video, error) {
    v, err := r.Videos.GetDetail(ctx, id, userFromCtx(ctx))
    if err != nil { return nil, gqlError(err) }
    return toVideoModel(v), nil
}

func (r *videoResolver) MediaInfo(ctx context.Context, v *model.Video) (*model.MediaInfo, error) {
    return r.Loaders(ctx).MediaInfoByVideo.Load(v.ID)
}

func (r *videoResolver) AudioTracks(ctx context.Context, v *model.Video) ([]*model.AudioTrack, error) {
    return r.Loaders(ctx).AudioTracksByVideo.Load(v.ID)
}
```

## 5. DataLoaders

```go
// api/internal/graph/loaders/loaders.go
package loaders

type Loaders struct {
    MediaInfoByVideo   *MediaInfoLoader
    AudioTracksByVideo *AudioTracksLoader
    TagsByVideo        *TagsLoader
    PlaybackByVideo    *PlaybackLoader
}

// New returns a per-request set. Lifetime = single GraphQL operation.
func New(ctx context.Context, db DB, user User) *Loaders {
    return &Loaders{
        MediaInfoByVideo: NewMediaInfoLoader(MediaInfoLoaderConfig{
            Wait:     5 * time.Millisecond,
            MaxBatch: 100,
            Fetch: func(ids []uuid.UUID) ([]*model.MediaInfo, []error) {
                return db.MediaInfoByVideoIDs(ctx, ids)
            },
        }),
        // ... others.
    }
}
```

A middleware attaches a fresh `*Loaders` to each request context:

```go
func loadersMiddleware(db DB) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            l := loaders.New(r.Context(), db, userFromCtx(r.Context()))
            ctx := context.WithValue(r.Context(), loadersKey{}, l)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

## 6. Persisted queries

```go
// api/internal/graph/persisted/store.go
package persisted

import "sync"

type Store struct {
    mu sync.RWMutex
    m  map[string]string // sha256 → operation source
}

// Lookup returns the operation source for a persistedQueryId, or
// ("", ErrNotFound) so the gqlgen handler can return Apollo's
// PersistedQueryNotFound error code (clients then retry with full body).
func (s *Store) Lookup(id string) (string, error) {
    s.mu.RLock(); defer s.mu.RUnlock()
    src, ok := s.m[id]
    if !ok { return "", ErrNotFound }
    return src, nil
}

// Register adds a persisted query (called when a client retries with
// the full body and we want to remember it).
func (s *Store) Register(id, src string) { /* ... */ }
```

The handler integration uses gqlgen's `extension.AutomaticPersistedQuery`
extension wired with our `Store`.

## 7. Cost limiter

```go
// api/internal/graph/cost.go
package graph

import "github.com/99designs/gqlgen/graphql/handler/extension"

func costLimit(max int) extension.ComplexityLimit {
    return extension.ComplexityLimit{ // see gqlgen FixedComplexityLimit
        Func: func(ctx context.Context, rc *graphql.OperationContext) int {
            return max
        },
    }
}

// Per-field complexity is registered via gqlgen's directive-style cost
// table; e.g. `segments(first: Int)` has cost `first`.
```

The schema declares `@cost(complexity: 10)`-style directives where the
default 1-per-field is too cheap. Aggregating with `first: 100000` hits
the limit.

## 8. Subscriptions

```go
// api/internal/graph/resolver/subscription_resolver.go
func (r *subscriptionResolver) JobUpdates(ctx context.Context) (<-chan *model.JobEvent, error) {
    ch := make(chan *model.JobEvent, 8)
    user := userFromCtx(ctx)
    sub, err := r.Hub.SubscribeChannels(ctx, []string{"jobs.new","jobs.flag_set","jobs.progress","jobs.heartbeat","jobs.reaped"}, user)
    if err != nil { return nil, err }
    go func() {
        defer close(ch); defer sub.Close()
        for env := range sub.C {
            ch <- toJobEvent(env)
        }
    }()
    return ch, nil
}
```

The `Hub` exposes a `SubscribeChannels` helper that multiplexes one
Postgres LISTEN per replica per channel; the resolver only opens an
in-process channel.

## 9. Parity test

```go
// api/internal/graph/parity_test.go
func TestParity(t *testing.T) {
    // 1. Walk chi route table.
    routes := router.Routes()  // helper that lists every route + method.

    // 2. Walk schema.graphql top-level Query/Mutation/Subscription fields.
    schema := parseSchema(t, "../../shared/graphql/schema.graphql")

    // 3. Diff using a fixed map of REST → GraphQL counterparts.
    expected := map[string]string{
        "GET /api/libraries":                "Query.libraries",
        "GET /api/libraries/{id}":           "Query.library",
        "POST /api/libraries":               "Mutation.createLibrary",
        // ... full table.
    }

    for rest, gql := range expected {
        if !routes.Has(rest) { t.Errorf("missing REST: %s", rest) }
        if !schema.HasField(gql) { t.Errorf("missing GraphQL: %s", gql) }
    }

    // Symmetric check: any REST or GraphQL operation not in the map fails.
    for r := range routes.AllStateChanging() {
        if _, ok := expected[r]; !ok {
            t.Errorf("orphan REST endpoint: %s", r)
        }
    }
    for f := range schema.AllOperations() {
        found := false
        for _, want := range expected { if want == f { found = true; break } }
        if !found { t.Errorf("orphan GraphQL field: %s", f) }
    }
}
```

## 10. Test plan

### 10.1 Schema

| Test | What it pins |
|---|---|
| `TestSchemaCompiles` | `gqlgen generate` runs in CI; missing resolver fails the build. |
| `TestParity` | REST ↔ GraphQL bidirectional parity. |

### 10.2 Integration

| Test | What it pins |
|---|---|
| `TestQueryVideoDetail` | `query { video(id:"...") { id title mediaInfo { durationSec } audioTracks { lang } } }` → ≤3 SQL round trips. |
| `TestQuery1000VideosLimitsRoundTrips` | `query { videos(first:1000) { items { id mediaInfo { durationSec } } } }` → ≤4 SQL queries (DataLoader batches). |
| `TestSubscriptionJobUpdates` | Subscribe; trigger a `jobs.new` NOTIFY → client receives event with the same payload as the WS variant. |
| `TestSubscriptionMultiplexed` | Open 100 subscriptions on one WS conn → only 1 `LISTEN jobs.*` (counter on the listener). |
| `TestMutationFailureNoPartial` | `mutation { patchVideoTags(id:..., delta:{ add:["bad/with/slash"] }) }` → entire mutation rolls back; tags unchanged; error array per field. |
| `TestPersistedQueryHit` | POST `{persistedQueryId}` with a known SHA → resolves successfully. |
| `TestPersistedQueryMiss` | Unknown SHA → `PersistedQueryNotFound` (Apollo error code). |
| `TestCostLimitExceeded` | Query for `segments(first: 100000)` → `cost-limit-exceeded`. |
| `TestPlaybackStateNullableForNoHistory` | Video the user hasn't watched → `playbackState: null`, not error. |
| `TestErrorEnvelopeForGraphQL` | Malformed query → GraphQL error envelope (not problem+json) with `extensions.code`. |

## 11. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Query asks for `playbackState` on a not-watched video | Returns `null`. | `TestPlaybackStateNullableForNoHistory` |
| 100 subscriptions multiplexed on one connection | One Postgres LISTEN per channel per replica; in-proc fanout to per-subscription channels. | `TestSubscriptionMultiplexed` |
| Mutation with mixed valid + invalid fields | Whole mutation in one Tx; on validation failure no partial persist; per-field error array. | `TestMutationFailureNoPartial` |
| Field-level cost limit | Gqlgen `FixedComplexityLimit` rejects with `cost-limit-exceeded`. | `TestCostLimitExceeded` |
| Persisted query ids must match SHA256 of the operation | Mismatch → `PersistedQueryNotFound`; client retries with full body. | `TestPersistedQueryMiss` |
| Schema rename | Forbidden by parity test; CI fails. | `TestParity` |
| New REST endpoint without GraphQL counterpart | Fails parity test. | `TestParity` |
| Resolver throws panic | gqlgen recoverer maps to a GraphQL error with no stack leak. | Manual test |
| Subscriptions over a slow WS | Backpressure-close 1011 (Story 7.16's Hub semantics). | Inherited |

## 12. Acceptance checklist

- [ ] `shared/graphql/schema.graphql` compiles via gqlgen.
- [ ] All domain types from AC-1 are present.
- [ ] Query/Mutation parity matches every REST endpoint per the parity table.
- [ ] Subscription parity covers `jobUpdates`, `libraryEvents(libraryId)`, `playbackUpdates(videoId)`.
- [ ] DataLoader keeps the 1000-video query under 4 SQL round trips.
- [ ] Persisted queries supported; missing id returns the Apollo-shaped error.
- [ ] Cost limit enforced.
- [ ] All `Test*` cases pass.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.17.
