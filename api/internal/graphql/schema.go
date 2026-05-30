// Package graphql provides the GraphQL surface for the API (Story 7.17).
//
// The schema is shipped as a single “.graphql“ SDL document
// (Schema) and parsed at boot. The schema contents define the domain
// types the resolvers fill in.
//
// We do NOT pull in gqlgen / 99designs/gqlgen at this stage — that
// codegen pipeline is gated by a separate task and a separate plan
// review. Instead, this package exposes:
//
//   - Schema: the parsed SDL source as a string,
//   - Handler: a thin HTTP handler that accepts “POST /graphql“
//     queries and dispatches to the registered resolver map. The
//     implementation is intentionally a pass-through that returns
//     “schema-only“ 501 today; the resolver table is wired by
//     subsequent stories.
//
// The point of this package is to lock in the schema shape so a
// parity test can assert "every REST endpoint has a counterpart in
// the SDL", per AC-2.
package graphql

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Schema is the SDL source for the API. Stories 7.17 AC-1 + AC-2
// list every domain type and root field this schema must expose.
const Schema = `
"""
Maktaba GraphQL schema (Story 7.17). Mirrors the REST routes 1:1 — the
parity test compares the chi router table against this SDL and fails on
diff.
"""

scalar Time
scalar JSON

# ---------- domain types ----------

type Library {
  id: ID!
  name: String!
  roots: [String!]!
  settings: JSON
  createdAt: Time!
  updatedAt: Time!
}

type MediaInfo {
  container: String
  durationSec: Float
  width: Int
  height: Int
  fps: Float
  bitrateKbps: Int
}

type AudioTrack {
  id: Int!
  language: String
  codec: String!
  channels: Int
  default: Boolean!
}

type Subtitle {
  id: ID!
  language: String!
  format: String!
  source: String!
  isDefault: Boolean!
  url: String!
}

type Chapter {
  seq: Int!
  startSec: Float!
  endSec: Float
  title: String!
  source: String!
}

type Word {
  seq: Int!
  startSec: Float!
  endSec: Float!
  text: String!
  confidence: Float
}

type Segment {
  id: ID!
  seq: Int!
  startSec: Float!
  endSec: Float!
  text: String!
  speaker: String
  words: [Word!]
}

type Transcript {
  id: ID!
  language: String
  segments(from: Float, to: Float, first: Int): [Segment!]!
  createdAt: Time!
}

type Tag { id: ID!  name: String! }

type Speaker {
  id: ID!
  name: String!
  clusterLabel: String
}

type Collection {
  id: ID!
  libraryId: ID!
  name: String!
  description: String
  isSmart: Boolean!
  smartQuery: JSON
  items: [Video!]!
}

type PlaybackState {
  positionSec: Float!
  completed: Boolean!
  updatedAt: Time!
}

type Job {
  id: ID!
  videoId: ID
  stage: String!
  state: String!
  priority: Int!
  attempts: Int!
  pauseRequested: Boolean!
  cancelRequested: Boolean!
  createdAt: Time!
  notBefore: Time
}

type Worker {
  id: String!
  host: String!
  lastHeartbeat: Time
  currentJobId: ID
}

type StreamingSession {
  id: ID!
  videoId: ID!
  mode: String!
  manifestUrl: String
  directUrl: String
  expiresAt: Time!
  openedAt: Time!
  closedAt: Time
}

type User {
  id: ID!
  username: String!
  isAdmin: Boolean!
}

type Video {
  id: ID!
  libraryId: ID!
  path: String!
  filename: String!
  title: String
  description: String
  state: String!
  detectedLanguage: String
  durationSec: Float
  sizeBytes: Int!
  contentHash: String!
  metadata: JSON
  createdAt: Time!
  updatedAt: Time!
  mediaInfo: MediaInfo
  audioTracks: [AudioTrack!]!
  subtitles: [Subtitle!]!
  chapters: [Chapter!]!
  tags: [Tag!]!
  latestTranscript: Transcript
  playbackState: PlaybackState
}

type SearchMatch {
  segmentId: ID!
  videoId: ID!
  startSec: Float!
  endSec: Float!
  snippet: String!
  score: Float!
}

type SearchHit {
  videoId: ID!
  bestMatch: SearchMatch!
  matches: [SearchMatch!]!
}

type SearchResult {
  hits: [SearchHit!]!
  total: Int!
  tookMs: JSON!
}

type Recommendation {
  videoId: ID!
  score: Float
  positionSec: Float
  durationSec: Float
}

type Rail {
  id: String!
  title: String!
  reasonKind: String!
  items: [Recommendation!]!
}

"""
A single Continue Watching / library card as consumed by the 10-foot
TV clients (Story 14.5 / 14.6). durationSec + positionSec drive the
progress bar; reason is the localizable rail reason key.
"""
type RailCard {
  id: ID!
  title: String!
  durationSec: Float!
  positionSec: Float
  remainingSec: Float
  posterUrl: String
  snippet: String
  reason: String
}

type Device {
  id: ID!
  platform: String!
  bundleId: String!
  appVersion: String
  locale: String
  registeredAt: Time!
  lastSeenAt: Time!
}

# ---------- input types ----------

input LibraryCreateInput {
  name: String!
  roots: [String!]!
  settings: JSON
}

input LibraryPatchInput {
  name: String
  roots: [String!]
  settings: JSON
}

input SearchFiltersInput {
  language: [String!]
  libraryId: [ID!]
  speaker: [String!]
}

input SearchInput {
  q: String!
  mode: String
  limit: Int
  filters: SearchFiltersInput
}

input VideoPatchInput {
  title: String
  description: String
  tags: [String!]
}

input DeviceRegisterInput {
  platform: String!
  pushToken: String!
  bundleId: String!
  appVersion: String
  locale: String
}

# ---------- query / mutation / subscription ----------

type Query {
  libraries: [Library!]!
  library(id: ID!): Library

  videos(library: ID, language: String, q: String, limit: Int): [Video!]!
  video(id: ID!): Video

  search(input: SearchInput!): SearchResult!
  searchSuggest(q: String!): [String!]!

  job(id: ID!): Job
  jobs(stage: String, state: String, limit: Int): [Job!]!
  queueStats: JSON!

  collections: [Collection!]!
  collection(id: ID!): Collection
  tags: [Tag!]!
  speakers(videoId: ID): [Speaker!]!

  settings: JSON!

  recommendations(surface: String, limit: Int): [RailCard!]!
  continueWatching(limit: Int): [RailCard!]!

  streamCapabilities: JSON!
  streamSession(id: ID!): StreamingSession

  devices: [Device!]!
}

type Mutation {
  createLibrary(input: LibraryCreateInput!): Library!
  patchLibrary(id: ID!, input: LibraryPatchInput!): Library!
  deleteLibrary(id: ID!, purge: Boolean): Boolean!
  scanLibrary(id: ID!): String!

  patchVideo(id: ID!, input: VideoPatchInput!): Video!
  deleteVideo(id: ID!, purge: Boolean): Boolean!
  processVideo(id: ID!, stage: String, priority: Int): String!
  reprocessVideo(id: ID!, fromStage: String!): [String!]!

  saveSearch(name: String!, query: JSON!): ID!

  openStreamSession(videoId: ID!, profile: String): StreamingSession!
  closeStreamSession(id: ID!): Boolean!
  postProgress(sessionId: ID!, positionSec: Float!, completed: Boolean): Boolean!

  pauseJob(id: ID!, force: Boolean): Job!
  resumeJob(id: ID!): Job!
  cancelJob(id: ID!): Job!
  retryJob(id: ID!): Job!

  patchSettings(input: JSON!): JSON!

  registerDevice(input: DeviceRegisterInput!): ID!
  unregisterDevice(id: ID!): Boolean!

  mergeSpeakers(keep: ID!, drop: ID!): Int!
}

type Subscription {
  jobUpdates: Job!
  libraryEvents(libraryId: ID!): JSON!
  playbackUpdates(videoId: ID!): JSON!
}
`

// Handler is the GraphQL HTTP endpoint.
//
// We do not pull in gqlgen / a full GraphQL engine (still gated by a
// separate plan). Instead this is a focused operation dispatcher: it
// recognises the specific root fields the 10-foot TV clients query —
// `recommendations`, `continueWatching`, `search`, `searchSuggest` —
// runs them against real DB-backed resolvers, and returns a
// spec-shaped `{"data": …}` body. Any other root field falls back to
// the honest `schema-only` 501 so callers can tell which fields are
// live versus still pending the full engine.
//
// Resolvers is optional: a zero-value Handler (no DB) keeps serving
// the SDL on GET /graphql/schema and answers POST /graphql with 501,
// preserving the pre-existing behaviour for callers that wire the
// handler without a DB.
type Handler struct {
	Resolvers *Resolvers
}

// gqlRequest is the standard GraphQL-over-HTTP POST body.
type gqlRequest struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName"`
	Variables     map[string]any `json:"variables"`
}

// rootFieldRE pulls the first selected root field name out of the
// query body. Good enough for the fixed set of single-root operations
// the TV clients send; it is intentionally not a general parser.
var rootFieldRE = regexp.MustCompile(`(?s)\{\s*([A-Za-z_][A-Za-z0-9_]*)`)

// ServeHTTP wires POST /graphql + GET /graphql/schema. The schema
// endpoint is unauthenticated and serves the SDL string above so a
// client tool can introspect the contract.
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/graphql/schema" {
		w.Header().Set("Content-Type", "application/graphql")
		_, _ = w.Write([]byte(Schema))
		return
	}
	w.Header().Set("Content-Type", "application/json")

	if h.Resolvers == nil {
		h.write501(w)
		return
	}

	var req gqlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Query) == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(gqlErrors("malformed GraphQL request body", "bad-request"))
		return
	}

	field := h.rootField(&req)
	if field == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(gqlErrors("could not determine root field", "bad-request"))
		return
	}

	// Every TV-facing query requires an authenticated principal —
	// the resolvers are user-scoped (no cross-user leak).
	if principal.FromContext(r.Context()) == nil {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(gqlErrors("authentication required", "forbidden"))
		return
	}

	data, code, err := h.Resolvers.resolve(r, field, req.Variables)
	if err != nil {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(gqlErrors(err.Error(), gqlCode(code)))
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{field: data}})
}

func (h Handler) rootField(req *gqlRequest) string {
	m := rootFieldRE.FindStringSubmatch(req.Query)
	if len(m) < 2 {
		return ""
	}
	// `query`/`mutation`/`subscription` keyword captured first? skip it.
	switch m[1] {
	case "query", "mutation", "subscription":
		body := req.Query[strings.Index(req.Query, m[1])+len(m[1]):]
		if idx := strings.Index(body, "{"); idx >= 0 {
			if mm := rootFieldRE.FindStringSubmatch(body[idx:]); len(mm) >= 2 {
				return mm[1]
			}
		}
		return ""
	}
	return m[1]
}

func (h Handler) write501(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(gqlErrors(
		"GraphQL resolvers are not yet wired in this build", "schema-only"))
}

func gqlErrors(msg, code string) map[string]any {
	return map[string]any{
		"errors": []map[string]any{
			{
				"message":    msg,
				"extensions": map[string]any{"code": code},
			},
		},
	}
}

func gqlCode(httpStatus int) string {
	switch httpStatus {
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusBadRequest:
		return "bad-request"
	case http.StatusNotImplemented:
		return "schema-only"
	default:
		return "internal"
	}
}
