package com.hamzalabs.maktaba.core.api

import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonDecoder
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray

// What the Player browses (Epic 48, story 48.8): Home's rails, the libraries, a library's hub,
// its videos and series, the series, a video's page and search, as the web reads them. Every
// route scopes to the caller's libraries on the home (a video or library the caller may not see
// answers 404, as one that does not exist), so nothing here filters by role.
//
// Read tolerantly: an older home leaves fields and rows out, a newer one adds them. A missing
// or null list is empty, a list element that cannot be read is left out (one odd card never
// hides a row), and a kind or state is kept as the home's own string.

/** A 404 from a browse route: the thing is gone, or the caller may not see it (the same answer). */
class NotAvailable(val status: ApiException.Status) : ApiException("Not available on this home") {
    init {
        // errorKind() and errorDetails() read the home's answer through the cause
        initCause(status)
    }
}

/** A list read element by element: what cannot be read is left out; not a list is empty. */
class LenientList<T>(private val element: KSerializer<T>) : KSerializer<List<T>> {
    private val list = ListSerializer(element)
    override val descriptor: SerialDescriptor = list.descriptor

    override fun deserialize(decoder: Decoder): List<T> {
        val json = decoder as? JsonDecoder ?: return list.deserialize(decoder)
        val array = json.decodeJsonElement() as? JsonArray ?: return emptyList()
        return array.mapNotNull { runCatching { json.json.decodeFromJsonElement(element, it) }.getOrNull() }
    }

    override fun serialize(encoder: Encoder, value: List<T>) = list.serialize(encoder, value)
}

// ---------- cards ----------

/** A series named on a card (On Deck's next lesson, a grouped Recently Added entry). */
@Serializable
data class SeriesName(val id: String, val name: String = "")

/** A video card of a row (`GET /api/home`, a hub): the caller's progress rides along. */
@Serializable
data class VideoCard(
    val id: String,
    val title: String? = null,
    val filename: String = "",
    @SerialName("library_id") val libraryId: String = "",
    @SerialName("duration_sec") val durationSec: Double? = null,
    val state: String = "",
    @SerialName("created_at") val createdAt: String? = null,
    /** when it came out: its upload date (midnight UTC), else the file's time */
    @SerialName("published_at") val publishedAt: String? = null,
    @SerialName("position_sec") val positionSec: Double? = null,
    val completed: Boolean? = null,
    @SerialName("last_watched_at") val lastWatchedAt: String? = null,
    val plays: Long? = null,
    /** On Deck: the series this is the next lesson of */
    val series: SeriesName? = null,
) {
    /** What a person reads: the title, else the file's name. */
    val displayTitle: String get() = title?.takeIf { it.isNotBlank() } ?: filename

    /** How far in the caller is (0..1), or null when there is nothing to show (as the web). */
    val watchedFraction: Double? get() = watchedFraction(positionSec, durationSec, completed == true)
}

/** How far into a video (0..1), or null: finished, not started or of unknown length. */
fun watchedFraction(positionSec: Double?, durationSec: Double?, completed: Boolean): Double? {
    val pos = positionSec ?: 0.0
    val dur = durationSec ?: 0.0
    if (completed || pos <= 0 || dur <= 0) return null
    return (pos / dur).coerceAtMost(1.0)
}

/** Recently Added grouped by series: a video, or a series with [count] new lessons. */
@Serializable
data class AddedEntry(
    /** "video" or "series" (another kind is kept as sent) */
    val kind: String,
    val video: VideoCard,
    val series: SeriesName? = null,
    val count: Int = 0,
)

/** Home's hero: the newest series (from its first lesson) or the lesson an admin picked. */
@Serializable
data class Featured(
    val kind: String,
    val video: VideoCard,
    val series: FeaturedSeries? = null,
    val picked: Boolean = false,
)

@Serializable
data class FeaturedSeries(val id: String, val name: String = "", val episodes: Long = 0)

// ---------- Home ----------

/** A library named on a row (`videos` on a hub). */
@Serializable
data class LibraryRef(val id: String, val name: String = "", val videos: Long? = null)

/** A pinned library's rows on Home. */
@Serializable
data class PinnedRows(
    val library: LibraryRef,
    @Serializable(with = LenientList::class) @SerialName("recently_added") val recentlyAdded: List<VideoCard> = emptyList(),
    @Serializable(with = LenientList::class) @SerialName("continue_watching") val continueWatching: List<VideoCard> = emptyList(),
)

/** `GET /api/home`: the hero and the rows (an empty row is for the screen to hide). */
@Serializable
data class HomeRails(
    val featured: Featured? = null,
    @Serializable(with = LenientList::class) @SerialName("continue_watching") val continueWatching: List<VideoCard> = emptyList(),
    @Serializable(with = LenientList::class) @SerialName("on_deck") val onDeck: List<VideoCard> = emptyList(),
    @Serializable(with = LenientList::class) val favorites: List<VideoCard> = emptyList(),
    @Serializable(with = LenientList::class) @SerialName("recently_added") val recentlyAdded: List<VideoCard> = emptyList(),
    @Serializable(with = LenientList::class) @SerialName("recently_added_grouped") val recentlyAddedGrouped: List<AddedEntry> = emptyList(),
    @Serializable(with = LenientList::class) @SerialName("recently_watched") val recentlyWatched: List<VideoCard> = emptyList(),
    @Serializable(with = LenientList::class) val pinned: List<PinnedRows> = emptyList(),
)

/** `GET /api/recommendations?surface=…`: the For You and From your library rails. */
@Serializable
data class Recommendations(
    @Serializable(with = LenientList::class) val rails: List<Rail> = emptyList(),
    @SerialName("generated_at") val generatedAt: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
    @SerialName("cache_hit") val cacheHit: Boolean = false,
)

@Serializable
data class Rail(
    val id: String = "",
    val title: String = "",
    /** what the rail is (its dismiss key): `DELETE /api/recommendations/rows/{reason_kind}` */
    @SerialName("reason_kind") val reasonKind: String = "",
    @SerialName("reason_args") val reasonArgs: JsonObject? = null,
    @Serializable(with = LenientList::class) val items: List<RecommendedVideo> = emptyList(),
)

@Serializable
data class RecommendedVideo(
    @SerialName("video_id") val videoId: String,
    @SerialName("position_sec") val positionSec: Double? = null,
    @SerialName("duration_sec") val durationSec: Double? = null,
    @SerialName("remaining_sec") val remainingSec: Double? = null,
    @SerialName("last_watched_at") val lastWatchedAt: String? = null,
    @SerialName("poster_url") val posterUrl: String? = null,
    val score: Double? = null,
)

// ---------- libraries ----------

/** A library card of `GET /api/libraries/summary`. */
@Serializable
data class LibrarySummary(
    val id: String,
    val name: String = "",
    val videos: Long = 0,
    @SerialName("duration_sec") val durationSec: Double = 0.0,
    @Serializable(with = LenientList::class) @SerialName("poster_video_ids") val posterVideoIds: List<String> = emptyList(),
    @SerialName("latest_added_at") val latestAddedAt: String? = null,
    val pinned: Boolean = false,
    @SerialName("pin_position") val pinPosition: Int? = null,
)

@Serializable
internal data class SummaryDoc(@Serializable(with = LenientList::class) val items: List<LibrarySummary> = emptyList())

/** `GET/PUT /api/me/pinned-libraries`: the caller's pins, in their order. */
@Serializable
data class PinnedLibraries(
    @Serializable(with = LenientList::class) @SerialName("library_ids") val libraryIds: List<String> = emptyList(),
    @Serializable(with = LenientList::class) val items: List<PinnedLibrary> = emptyList(),
)

@Serializable
data class PinnedLibrary(val id: String, val name: String = "", val position: Int = 0)

/** `GET /api/libraries/{id}/hub`. */
@Serializable
data class LibraryHub(val library: LibraryRef, val rows: HubRows = HubRows())

@Serializable
data class HubRows(
    @Serializable(with = LenientList::class) @SerialName("on_deck") val onDeck: List<VideoCard> = emptyList(),
    @Serializable(with = LenientList::class) @SerialName("continue_watching") val continueWatching: List<VideoCard> = emptyList(),
    @Serializable(with = LenientList::class) @SerialName("recently_added") val recentlyAdded: List<VideoCard> = emptyList(),
    @Serializable(with = LenientList::class) @SerialName("recently_added_grouped") val recentlyAddedGrouped: List<AddedEntry> = emptyList(),
    @Serializable(with = LenientList::class) @SerialName("recently_watched") val recentlyWatched: List<VideoCard> = emptyList(),
    @Serializable(with = LenientList::class) @SerialName("most_watched") val mostWatched: List<VideoCard> = emptyList(),
    @Serializable(with = LenientList::class) val series: List<HubSeries> = emptyList(),
    @Serializable(with = LenientList::class) val years: List<HubYear> = emptyList(),
)

@Serializable
data class HubSeries(
    val id: String,
    val name: String = "",
    val episodes: Long = 0,
    /** its first visible episode: the tile shows that video's poster */
    @SerialName("poster_video_id") val posterVideoId: String? = null,
)

@Serializable
data class HubYear(val year: Int, val videos: Long = 0)

/** `GET /api/libraries/{id}/series`: a page of the library's series (by offset). */
@Serializable
data class LibrarySeriesPage(
    val library: LibraryRef? = null,
    val sort: String = "",
    @Serializable(with = LenientList::class) val items: List<LibrarySeriesItem> = emptyList(),
    val total: Int = 0,
    /** the next page's offset; null on the last page */
    val next: Int? = null,
)

@Serializable
data class LibrarySeriesItem(
    val id: String,
    val name: String = "",
    @SerialName("poster_path") val posterPath: String? = null,
    @SerialName("poster_video_id") val posterVideoId: String? = null,
    val episodes: Long = 0,
    val watched: Long = 0,
    @SerialName("in_progress") val inProgress: Long = 0,
    @SerialName("latest_added_at") val latestAddedAt: String? = null,
    @SerialName("last_watched_at") val lastWatchedAt: String? = null,
)

/** The Series tab's sorts. */
enum class SeriesSort(val wire: String) { Name("name"), Newest("newest"), Recent("recent") }

/** `GET /api/videos/facets`: what a library's grid can be narrowed by. */
@Serializable
data class VideoFacets(
    @Serializable(with = LenientList::class) val languages: List<LanguageFacet> = emptyList(),
    @Serializable(with = LenientList::class) val tags: List<TagFacet> = emptyList(),
    @Serializable(with = LenientList::class) val speakers: List<SpeakerFacet> = emptyList(),
    @Serializable(with = LenientList::class) val letters: List<LetterFacet> = emptyList(),
    @Serializable(with = LenientList::class) val durations: List<BandFacet> = emptyList(),
    @Serializable(with = LenientList::class) val added: List<BandFacet> = emptyList(),
    @Serializable(with = LenientList::class) val resolutions: List<BandFacet> = emptyList(),
)

@Serializable data class LanguageFacet(val code: String, val videos: Long = 0)
@Serializable data class TagFacet(val id: String, val name: String = "", val videos: Long = 0)
@Serializable data class SpeakerFacet(val name: String, val videos: Long = 0)
@Serializable data class LetterFacet(val letter: String, val videos: Long = 0)
@Serializable data class BandFacet(val value: String, val videos: Long = 0)

/** A library grid's sorts (the home answers 400 to anything else). */
enum class VideoSort(val wire: String) { Newest("created_at"), Title("title"), Published("published_at") }

/**
 * A page of the video list (`GET /api/videos`): a library's grid, narrowed by facets. With
 * [favorite] the list is the caller's own (`/api/me/videos`, as the web).
 */
data class VideoQuery(
    val library: String? = null,
    val sort: VideoSort = VideoSort.Newest,
    /** "asc" or "desc"; null is the home's default for the sort */
    val order: String? = null,
    val language: String? = null,
    val speaker: String? = null,
    val tag: String? = null,
    val series: String? = null,
    /** "watched", "in_progress" or "unwatched" */
    val watched: String? = null,
    val favorite: Boolean = false,
    val cursor: String? = null,
    val limit: Int = 60,
) {
    val path: String get() = if (favorite) "/api/me/videos" else "/api/videos"

    val params: Map<String, String>
        get() = buildMap {
            put("limit", limit.toString())
            put("sort", sort.wire)
            order?.let { put("order", it) }
            library?.let { put("library", it) }
            language?.let { put("language", it) }
            speaker?.let { put("speaker", it) }
            tag?.let { put("tag", it) }
            series?.let { put("series", it) }
            watched?.let { put("watched", it) }
            if (favorite) put("favorite", "true")
            cursor?.let { put("cursor", it) }
        }
}

/** A video of the list and its page (`GET /api/videos/{id}`): the caller's state rides along. */
@Serializable
data class VideoDetail(
    val id: String,
    @SerialName("library_id") val libraryId: String = "",
    val filename: String = "",
    val title: String? = null,
    @SerialName("display_title") val displayTitleOverride: String? = null,
    val description: String? = null,
    val state: String = "",
    @SerialName("detected_language") val detectedLanguage: String? = null,
    @SerialName("duration_sec") val durationSec: Double? = null,
    @SerialName("created_at") val createdAt: String? = null,
    @SerialName("published_at") val publishedAt: String? = null,
    val playable: Boolean? = null,
    @Serializable(with = LenientList::class) val tags: List<String> = emptyList(),
    @SerialName("playback_state") val playbackState: PlaybackState? = null,
    val plays: Long? = null,
    val favorite: Boolean? = null,
) {
    /** What a person reads: the home's display title, the title, else the file's name. */
    val displayTitle: String
        get() = displayTitleOverride?.takeIf { it.isNotBlank() } ?: title?.takeIf { it.isNotBlank() } ?: filename

    /** Where Resume starts: the saved position, unless it is finished or not started. */
    val resumeAtSec: Double?
        get() = playbackState?.takeIf { !it.completed && it.positionSec > 0 }?.positionSec
}

@Serializable
data class PlaybackState(
    @SerialName("position_sec") val positionSec: Double = 0.0,
    val completed: Boolean = false,
    @SerialName("updated_at") val updatedAt: String? = null,
)

/** `GET /api/me/videos/{id}`: the caller's own state of a video. */
@Serializable
data class MyVideo(
    @SerialName("video_id") val videoId: String,
    @SerialName("position_sec") val positionSec: Double = 0.0,
    val completed: Boolean = false,
    val favorite: Boolean = false,
    @Serializable(with = LenientList::class) val playlists: List<PlaylistRef> = emptyList(),
    val series: SeriesName? = null,
)

@Serializable
data class PlaylistRef(val id: String, val name: String = "")

/** A chapter of `GET /api/videos/{id}/chapters`: plays from [startSec]. */
@Serializable
data class Chapter(
    val id: Long = 0,
    val seq: Int = 0,
    @SerialName("start_sec") val startSec: Double,
    @SerialName("end_sec") val endSec: Double? = null,
    val title: String = "",
    val source: String = "",
)

@Serializable
internal data class ChaptersDoc(@Serializable(with = LenientList::class) val items: List<Chapter> = emptyList())

/** `GET /api/videos/{id}/segments`: the transcript's lines in a time window. */
@Serializable
data class Segments(
    @Serializable(with = LenientList::class) val items: List<Segment> = emptyList(),
    /** the window holds more than was sent */
    val partial: Boolean = false,
)

@Serializable
data class Segment(
    val id: Long = 0,
    val seq: Int = 0,
    @SerialName("start_sec") val startSec: Double,
    @SerialName("end_sec") val endSec: Double = 0.0,
    val text: String = "",
    val speaker: String? = null,
)

// ---------- series ----------

/** A series of `GET /api/series` and `GET /api/series/{id}`, with the caller's progress. */
@Serializable
data class SeriesItem(
    val id: String,
    val name: String = "",
    @SerialName("poster_path") val posterPath: String? = null,
    @SerialName("poster_video_id") val posterVideoId: String? = null,
    @SerialName("library_id") val libraryId: String? = null,
    val year: Int? = null,
    /** "season", "absolute", "date", ... (kept as sent) */
    val numbering: String = "",
    @SerialName("season_count") val seasonCount: Int = 0,
    @SerialName("episode_count") val episodeCount: Int = 0,
    @SerialName("watched_count") val watchedCount: Int = 0,
    @SerialName("in_progress") val inProgress: Int = 0,
) {
    /** The caller's progress through it (0..1). */
    val progress: Double get() = if (episodeCount <= 0) 0.0 else (watchedCount.toDouble() / episodeCount).coerceIn(0.0, 1.0)
}

@Serializable
internal data class SeriesDoc(@Serializable(with = LenientList::class) val items: List<SeriesItem> = emptyList())

/** `GET /api/series/{id}/episodes`: the seasons, and Continue's episode. */
@Serializable
data class SeriesEpisodes(
    @Serializable(with = LenientList::class) val seasons: List<Season> = emptyList(),
    val numbering: String = "",
    val sort: String = "",
    /** the caller's next episode (Continue); null when there is none */
    @SerialName("next_episode") val nextEpisode: Episode? = null,
)

@Serializable
data class Season(
    val season: Int = 0,
    @Serializable(with = LenientList::class) val episodes: List<Episode> = emptyList(),
)

@Serializable
data class Episode(
    @SerialName("video_id") val videoId: String,
    val season: Int? = null,
    val episode: Int? = null,
    @SerialName("absolute_number") val absoluteNumber: Int? = null,
    val title: String = "",
    val thumbnail: String? = null,
    @SerialName("duration_sec") val durationSec: Double? = null,
    @SerialName("progress_pct") val progressPct: Double = 0.0,
    val watched: Boolean = false,
)

/** A series' episode orders. */
enum class EpisodeSort(val wire: String) { Episode("episode"), Added("added") }

// ---------- search ----------

/** `POST /api/search`: Titles and In the transcript, each a first page of a kept result set. */
@Serializable
data class SearchResults(
    /** the kept result set (pages: [searchTitles], [searchTranscripts]); empty when not kept */
    @SerialName("search_id") val searchId: String = "",
    @SerialName("expires_at") val expiresAt: String? = null,
    val q: String = "",
    val mode: String = "",
    /** the semantic half did not answer: these are text matches only */
    val degraded: Boolean = false,
    @SerialName("page_size") val pageSize: Int = 0,
    @SerialName("passages_page_size") val passagesPageSize: Int = 0,
    @Serializable(with = LenientList::class) val videos: List<TitleHit> = emptyList(),
    @SerialName("videos_total") val videosTotal: Int = 0,
    @Serializable(with = LenientList::class) @SerialName("transcript_videos") val transcriptVideos: List<TranscriptHit> = emptyList(),
    @SerialName("transcript_total") val transcriptTotal: Int = 0,
)

/** A video of Titles. */
@Serializable
data class TitleHit(
    @SerialName("video_id") val videoId: String,
    val title: String = "",
    @SerialName("duration_sec") val durationSec: Double? = null,
    @SerialName("library_id") val libraryId: String = "",
    @SerialName("library_name") val libraryName: String = "",
    @SerialName("poster_url") val posterUrl: String = "",
    val score: Double = 0.0,
    @SerialName("in_transcripts") val inTranscripts: Boolean = false,
    /** the title with `<mark>` around the query's words */
    @SerialName("title_snippet") val titleSnippet: String = "",
)

/** A video of In the transcript, with its best passages. */
@Serializable
data class TranscriptHit(
    @SerialName("video_id") val videoId: String,
    val title: String = "",
    @SerialName("duration_sec") val durationSec: Double? = null,
    @SerialName("library_id") val libraryId: String = "",
    @SerialName("library_name") val libraryName: String = "",
    @SerialName("poster_url") val posterUrl: String = "",
    @SerialName("best_score") val bestScore: Double = 0.0,
    val matches: Int = 0,
    @Serializable(with = LenientList::class) val passages: List<Passage> = emptyList(),
    @SerialName("in_title") val inTitle: Boolean = false,
)

/** A matching transcript line: plays from [startSec]; [snippet] marks the words with `<mark>`. */
@Serializable
data class Passage(
    @SerialName("segment_id") val segmentId: Long = 0,
    @SerialName("start_sec") val startSec: Double,
    @SerialName("end_sec") val endSec: Double = 0.0,
    val snippet: String = "",
    val score: Double = 0.0,
)

/** `GET /api/search/results/{sid}?list=…`: one more page of Titles or In the transcript. */
@Serializable
data class SearchPage<T>(
    @SerialName("search_id") val searchId: String = "",
    val q: String = "",
    val list: String = "",
    val offset: Int = 0,
    val limit: Int = 0,
    val total: Int = 0,
    @Serializable(with = LenientList::class) val items: List<T> = emptyList(),
) {
    /** The next page's offset; null on the last page. */
    val next: Int? get() = (offset + items.size).takeIf { items.isNotEmpty() && it < total }
}

/** A recent search (`GET /api/search/recent`). */
@Serializable
data class RecentSearch(
    @SerialName("search_id") val searchId: String,
    val q: String = "",
    val mode: String = "",
    @SerialName("last_used_at") val lastUsedAt: String? = null,
    @SerialName("title_count") val titleCount: Int = 0,
    @SerialName("transcript_count") val transcriptCount: Int = 0,
    /** its results are still kept: open it by [searchId]; otherwise search again */
    val live: Boolean = false,
)

@Serializable
internal data class RecentDoc(@Serializable(with = LenientList::class) val items: List<RecentSearch> = emptyList())

@Serializable
internal data class SuggestDoc(@Serializable(with = LenientList::class) val suggestions: List<String> = emptyList())

@Serializable
internal data class UpdatedDoc(val updated: Int = 0)

// ---------- the calls ----------

/** [s] as one encoded path segment. */
private fun seg(s: String): String = java.net.URLEncoder.encode(s, Charsets.UTF_8.name()).replace("+", "%20")

/** [block], with a 404 as [NotAvailable]. A 401 the client could not refresh is already [ApiException.SignedOut]. */
private inline fun <T> browsing(block: () -> T): T = try {
    block()
} catch (e: ApiException.Status) {
    if (e.code == 404) throw NotAvailable(e) else throw e
}

private suspend inline fun <reified T> ApiClient.read(path: String, query: Map<String, String> = emptyMap()): T =
    browsing { get(path, query) }

private suspend inline fun <reified T> ApiClient.write(method: String, path: String, body: JsonObject? = null): T =
    browsing { decode(send(request(method, path, body = body))) }

private suspend fun ApiClient.call(method: String, path: String, query: Map<String, String> = emptyMap()) {
    browsing { send(request(method, path, query)) }
}

/** `GET /api/home`. */
suspend fun ApiClient.homeRails(): HomeRails = read("/api/home")

/** `GET /api/recommendations?surface=…` (the phone's Home: `mobile-home`). */
suspend fun ApiClient.recommendations(surface: String = "mobile-home"): Recommendations =
    read("/api/recommendations", mapOf("surface" to surface))

/** Not interested: `DELETE /api/recommendations/items/{video_id}`. */
suspend fun ApiClient.dismissRecommendation(videoId: String) =
    call("DELETE", "/api/recommendations/items/${seg(videoId)}")

/** Hides a whole rail: `DELETE /api/recommendations/rows/{reason_kind}`. */
suspend fun ApiClient.dismissRail(reasonKind: String) =
    call("DELETE", "/api/recommendations/rows/${seg(reasonKind)}")

/** `GET /api/libraries/summary`: one card per library the caller may see. */
suspend fun ApiClient.librarySummaries(): List<LibrarySummary> = read<SummaryDoc>("/api/libraries/summary").items

/** `GET /api/me/pinned-libraries`. */
suspend fun ApiClient.pinnedLibraries(): PinnedLibraries = read("/api/me/pinned-libraries")

/** `PUT /api/me/pinned-libraries`: [libraryIds] are the pins, in order (an empty list unpins all). */
suspend fun ApiClient.setPinnedLibraries(libraryIds: List<String>): PinnedLibraries =
    write("PUT", "/api/me/pinned-libraries", buildJsonObject { putJsonArray("library_ids") { libraryIds.forEach { add(JsonPrimitive(it)) } } })

/** `GET /api/libraries/{id}/hub`: the library's rows. */
suspend fun ApiClient.libraryHub(library: String): LibraryHub = read("/api/libraries/${seg(library)}/hub")

/** `GET /api/libraries/{id}/series`: a page of its series. */
suspend fun ApiClient.librarySeries(library: String, sort: SeriesSort = SeriesSort.Name, offset: Int = 0, limit: Int? = null): LibrarySeriesPage =
    read("/api/libraries/${seg(library)}/series", buildMap {
        put("sort", sort.wire)
        if (offset > 0) put("offset", offset.toString())
        limit?.let { put("limit", it.toString()) }
    })

/** `GET /api/videos/facets`, for one library or all the caller may see. */
suspend fun ApiClient.videoFacets(library: String? = null): VideoFacets =
    read("/api/videos/facets", if (library != null) mapOf("library" to library) else emptyMap())

/** A page of the video list ([VideoQuery]): follow `next` as the cursor. */
suspend fun ApiClient.videoPage(query: VideoQuery): Page<VideoDetail> = read(query.path, query.params)

/** `GET /api/videos/{id}`: a video's page, with the caller's `playback_state`. */
suspend fun ApiClient.videoDetail(id: String): VideoDetail = read("/api/videos/${seg(id)}")

/** `GET /api/me/videos/{id}`: the caller's own state of a video (favorite, playlists, series). */
suspend fun ApiClient.myVideo(id: String): MyVideo = read("/api/me/videos/${seg(id)}")

/** `GET /api/videos/{id}/chapters`. */
suspend fun ApiClient.chapters(video: String): List<Chapter> = read<ChaptersDoc>("/api/videos/${seg(video)}/chapters").items

/** `GET /api/videos/{id}/segments`: the transcript from [fromSec] to [toSec] (the home's default window when null). */
suspend fun ApiClient.segments(video: String, fromSec: Double = 0.0, toSec: Double? = null, limit: Int? = null): Segments =
    read("/api/videos/${seg(video)}/segments", buildMap {
        if (fromSec > 0) put("from", fromSec.toString())
        toSec?.let { put("to", it.toString()) }
        limit?.let { put("limit", it.toString()) }
    })

/** Mark watched (or not): `POST /api/me/watched`. The number of videos the home updated. */
suspend fun ApiClient.markWatched(videoIds: List<String>, watched: Boolean = true): Int =
    write<UpdatedDoc>("POST", "/api/me/watched", mark(videoIds, "watched", watched)).updated

/** Favorite (or not): `POST /api/me/favorites`. The number of videos the home updated. */
suspend fun ApiClient.setFavorite(videoIds: List<String>, favorite: Boolean = true): Int =
    write<UpdatedDoc>("POST", "/api/me/favorites", mark(videoIds, "favorite", favorite)).updated

private fun mark(ids: List<String>, key: String, value: Boolean) = buildJsonObject {
    putJsonArray("video_ids") { ids.forEach { add(JsonPrimitive(it)) } }
    put(key, value)
}

/** `GET /api/series`: every series the caller may see, [library] narrows it. */
suspend fun ApiClient.seriesList(library: String? = null): List<SeriesItem> =
    read<SeriesDoc>("/api/series", if (library != null) mapOf("library_id" to library) else emptyMap()).items

/** `GET /api/series/{id}`. */
suspend fun ApiClient.series(id: String): SeriesItem = read("/api/series/${seg(id)}")

/** `GET /api/series/{id}/episodes`: its seasons and Continue's episode. */
suspend fun ApiClient.seriesEpisodes(id: String, sort: EpisodeSort? = null): SeriesEpisodes =
    read("/api/series/${seg(id)}/episodes", if (sort != null) mapOf("sort" to sort.wire) else emptyMap())

/**
 * `POST /api/search` in [mode] (`hybrid`: text and meaning, Arabic and English alike). The query
 * is sent as typed (trimmed only); the home ranks it.
 */
suspend fun ApiClient.search(q: String, mode: String = "hybrid", libraryIds: List<String> = emptyList()): SearchResults =
    write("POST", "/api/search", buildJsonObject {
        put("q", q.trim())
        put("mode", mode)
        if (libraryIds.isNotEmpty()) {
            put("filters", buildJsonObject { putJsonArray("library_id") { libraryIds.forEach { add(JsonPrimitive(it)) } } })
        }
    })

/** The next page of Titles: `GET /api/search/results/{sid}?list=titles`. */
suspend fun ApiClient.searchTitles(searchId: String, offset: Int, limit: Int? = null): SearchPage<TitleHit> =
    read("/api/search/results/${seg(searchId)}", resultsQuery("titles", offset, limit))

/** The next page of In the transcript: `GET /api/search/results/{sid}?list=transcripts`. */
suspend fun ApiClient.searchTranscripts(searchId: String, offset: Int, limit: Int? = null): SearchPage<TranscriptHit> =
    read("/api/search/results/${seg(searchId)}", resultsQuery("transcripts", offset, limit))

private fun resultsQuery(list: String, offset: Int, limit: Int?) = buildMap {
    put("list", list)
    put("offset", offset.toString())
    limit?.let { put("limit", it.toString()) }
}

/** `GET /api/search/suggest?q=`: the caller's own past searches that start with [q]. */
suspend fun ApiClient.searchSuggestions(q: String): List<String> {
    val t = q.trim()
    if (t.isEmpty()) return emptyList()
    return read<SuggestDoc>("/api/search/suggest", mapOf("q" to t)).suggestions
}

/** `GET /api/search/recent`: the caller's recent searches, newest first. */
suspend fun ApiClient.recentSearches(): List<RecentSearch> = read<RecentDoc>("/api/search/recent").items

/** Forgets one recent search: `DELETE /api/search/recent/{sid}`. */
suspend fun ApiClient.forgetRecentSearch(searchId: String) = call("DELETE", "/api/search/recent/${seg(searchId)}")

/** Forgets every recent search: `DELETE /api/search/recent`. */
suspend fun ApiClient.clearRecentSearches() = call("DELETE", "/api/search/recent")
