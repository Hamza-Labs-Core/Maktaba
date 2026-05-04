# Implementation Plan — Story 14.6 Recommendations UI

> Companion to [story-14-06-recommendations-ui.md](story-14-06-recommendations-ui.md).
> The story states *what* and *why*; this plan states *how*.
> The data source is owned by [Story 14.7](story-14-07-recommendations-api.md);
> this story owns only the **client surfaces** on tvOS and AndroidTV.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| tvOS files | `apps/tvos/Sources/Features/Home/{RecommendationsRows.swift,RecommendationRowModel.swift,RowReasonHeader.swift}`. Reuses `FocusableCardStyle` and `FocusGrid` primitives from [Story 14.3](story-14-03-10-foot-ui.md). |
| AndroidTV files | `apps/androidtv/.../home/{RecommendationsRows.kt,RecommendationsViewModel.kt}`. |
| GraphQL operation | Single query `Recommendations` defined as `query Recommendations { recommendations { rows { title reasonKind reasonArgs items { ... } } expiresAt } }`, resolved by [Story 14.7](story-14-07-recommendations-api.md). |
| Local "Not interested" state | Optimistic local hide with rollback on server error; persists via `DELETE /api/recommendations/...`. |
| Out of scope | The recommender itself, dismiss endpoints, refresh logic ([Story 14.7](story-14-07-recommendations-api.md)); Continue Watching row ([Story 14.5](story-14-05-continue-watching.md)). |

## 1. Data flow

```
Home.onAppear           ┌────────────────────┐
   ↓                    │ RecommendationsAPI │
   load() ─────────────►│  (Apollo)          │
   ↓                    └─────────┬──────────┘
   render rows                    │
                          GET /api/recommendations
                                  │
                                  ▼
                       ┌──────────────────────┐
                       │ recommendation_runs  │  (cached 24 h, Story 14.7)
                       └──────────────────────┘
```

When the user taps "Not interested":
- Optimistic: hide the row/item from local state.
- Network: `DELETE /api/recommendations/rows/{reason_kind}` or `/items/{video_id}`.
- On error: re-insert with a snackbar ("Couldn't update preferences. Try again.").

## 2. Type definitions

```swift
// tvOS — RecommendationRowModel.swift
struct RecommendationRow: Identifiable, Hashable {
    let id: String                   // e.g. "more_from_speaker:abc123"
    let title: String
    let reasonKind: ReasonKind
    let reasonArgs: [String: String]
    var items: [VideoCard]
}

enum ReasonKind: String, Codable {
    case moreFromSpeaker = "more_from_speaker"
    case similarToVideo  = "similar_to_video"
    case newlyAdded      = "newly_added"
    case editorPicks     = "editor_picks"
    case libraryRecap    = "library_recap"
    case speakersYouFollow = "speakers_you_follow"
}
```

```kotlin
// AndroidTV — RecommendationsViewModel.kt
data class RecommendationRow(
    val id: String,
    val title: String,
    val reasonKind: ReasonKind,
    val reasonArgs: Map<String, String>,
    val items: List<VideoCard>,
)

enum class ReasonKind(val raw: String) {
    MORE_FROM_SPEAKER("more_from_speaker"),
    SIMILAR_TO_VIDEO("similar_to_video"),
    NEWLY_ADDED("newly_added"),
    EDITOR_PICKS("editor_picks"),
    LIBRARY_RECAP("library_recap"),
    SPEAKERS_YOU_FOLLOW("speakers_you_follow");

    // `Enum.values()` is deprecated in Kotlin 2.0; use `entries` (the
    // Apollo Kotlin 4 / K2 toolchain emits the deprecation warning).
    companion object { fun fromRaw(s: String) = entries.firstOrNull { it.raw == s } }
}
```

## 3. Composition

### 3.1 tvOS

```swift
struct RecommendationsRows: View {
    @StateObject var model = RecommendationRowModel()
    var body: some View {
        ForEach(model.visibleRows) { row in
            VStack(alignment: .leading) {
                RowReasonHeader(row: row, onDismiss: { model.dismissRow(row.id) })
                ScrollView(.horizontal, showsIndicators: false) {
                    LazyHStack(spacing: TVTokens.Spacing.md) {
                        ForEach(row.items) { item in
                            VideoCardView(item: item, onLongPress: {
                                model.dismissItem(rowId: row.id, videoId: item.videoId)
                            })
                            .buttonStyle(FocusableCardStyle())
                        }
                    }
                }.focusSection()
            }
        }
        .task { await model.load() }
    }
}
```

`RowReasonHeader` renders the localized title plus a "Not interested" focusable affordance accessible via the menu button on the Siri Remote (mapped to `onMenuPress`).

### 3.2 AndroidTV

```kotlin
@Composable
fun RecommendationsRows(viewModel: RecommendationsViewModel = hiltViewModel()) {
    val rows by viewModel.visibleRows.collectAsStateWithLifecycle()
    LaunchedEffect(Unit) { viewModel.load() }
    Column {
        rows.forEach { row ->
            RowReasonHeader(row, onDismiss = { viewModel.dismissRow(row.id) })
            LazyRow(modifier = Modifier.focusRestorer()) {
                items(row.items, key = { it.videoId }) { item ->
                    VideoCard(item, modifier = Modifier
                        .focusableCard { onPlay(item) }
                        .onKeyEvent { key ->
                            // Long-press mapped to KEYCODE_DPAD_CENTER held
                            if (key.isLongCenter) { viewModel.dismissItem(row.id, item.videoId); true } else false
                        })
                }
            }
        }
    }
}
```

## 4. Reason title localization

The server returns a localized `title` per AC of [Story 14.7](story-14-07-recommendations-api.md), so the client renders it verbatim. We **never** templatize on the client because that requires shipping speaker / video names to the client just for formatting; the server already has them.

## 5. Hide-row / hide-item persistence

Local cache key `recommendations_dismissals_v1` (UserDefaults / DataStore) holds a set of dismissed `(kind, key)` pairs; the cache is the optimistic source while the API request is in flight. On API success, the entry is dropped from the cache; on API error, it's restored. Server-side persistence is in `recommendation_dismissals` ([Story 14.7](story-14-07-recommendations-api.md)).

## 6. Edge cases — composition

The story EC requires:

- All rows ≤ 1 item → row hidden. Implementation: filter rows whose `items.count <= 1`.
- "Speakers you follow" with no follow → server omits; client just renders what it gets.
- Cold-start (no history) → server returns `newly_added` and `editor_picks` only.

```swift
extension RecommendationRowModel {
    var visibleRows: [RecommendationRow] {
        rows.filter { row in
            row.items.count > 1 && !dismissedRowIds.contains(row.id)
        }
    }
}
```

## 7. Test plan

### 7.1 tvOS

| Test | What it pins |
|---|---|
| `testRecommendationsRowRender` | A two-row response renders two `LazyHStack`s with correct titles. |
| `testHiddenWhenItemsTooFew` | A row with 1 item is omitted from `visibleRows`. |
| `testDismissRowOptimistic` | Tap dismiss → row disappears immediately; on stub error, snackbar + row re-appears. |
| `testColdStartFallback` | Server returns only `newly_added` + `editor_picks` → those two rows render; no personalized rows. |
| `testFocusGeometry` | D-pad across a row of 20: focus moves card-by-card; up/down moves to the next row at the same column. |

### 7.2 AndroidTV

| Test | What it pins |
|---|---|
| `recommendationsLoadAndRender` | Compose test rule asserts row titles for a 3-row stubbed response. |
| `dismissRowPersistsAcrossRefresh` | Dismiss → reload → row absent (server returned without it because dismissal was persisted). |
| `noRowWithSingleItem` | Single-item row collapsed locally even before server-side filter catches up. |

### 7.3 Snapshot

- "Cold-start" home: only Continue Watching (empty-hidden), Newly Added, Editor's Picks.
- "Personalized" home: 5 rows.
- After-dismiss: 4 rows.

## 8. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| All rows have ≤ 1 item | All hidden; section omitted from Home. | `testHiddenWhenItemsTooFew` |
| "Speakers you follow" with no follows | Server omits; client doesn't need to handle. | n/a (server side) |
| Cold-start | Server returns only `newly_added` + `editor_picks`. | `testColdStartFallback` |
| Dismissed row reappears next launch | Should not. Verified by re-load test. | `dismissRowPersistsAcrossRefresh` |
| Server `expires_at < now()` mid-session | Background refresh fetches a new payload; rows update without losing focus. | `testRefreshPreservesFocus` |
| "Not interested" tap on focused card | Card animates out; focus moves to its successor. | `testDismissPreservesFocus` |
| API error on load | Show empty Recommendations section silently (Continue Watching above + Newly Added below from a separate API). | `testApiErrorSilent` |
| Partial dismissal API failure | Local cache rolls back; snackbar message; user can retry. | `testDismissApiErrorRollback` |

## 9. Acceptance checklist

**Composition**
- [ ] tvOS and AndroidTV render up to 5 rows, each up to 20 items.
- [ ] Rows with ≤ 1 item are hidden client-side.

**Interaction**
- [ ] Long-press / context dismisses row or item; persists across app restart.
- [ ] Cold-start fallback renders newly-added + editor-picks only.

**Tests**
- [ ] All §7 tests pass on the simulators.

**Docs**
- [ ] `specs/epics/14-tv-apps/README.md` ticks story 14.6.
