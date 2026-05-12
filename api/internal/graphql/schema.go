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
  items: [Recommendation!]!
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

  recommendations(surface: String, limit: Int): [Rail!]!

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

// Handler is the GraphQL HTTP endpoint. The full resolver wiring lands
// in a subsequent story that introduces gqlgen; today the handler
// returns 501 with a clear “schema-only“ extension so a client can
// detect when the resolvers are not yet wired.
type Handler struct{}

// Mount wires POST /graphql + GET /graphql/schema. The schema endpoint
// is unauthenticated and serves the SDL string above, so a client tool
// can introspect the contract.
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/graphql/schema" {
		w.Header().Set("Content-Type", "application/graphql")
		_, _ = w.Write([]byte(Schema))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]any{
			{
				"message": "GraphQL resolvers are not yet wired in this build",
				"extensions": map[string]any{
					"code": "schema-only",
				},
			},
		},
	})
}
