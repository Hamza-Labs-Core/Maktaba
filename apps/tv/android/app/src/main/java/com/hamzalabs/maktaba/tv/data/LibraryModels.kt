package com.hamzalabs.maktaba.tv.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/*
 * Epic 56: what the TV's library view reads. Cards (`GET /api/home`, a library's hub, a
 * playlist's or a collection's videos), collections, playlists, categories, a series'
 * episodes and the grid grouped by series.
 */

/** A video card of a row or a list, with the caller's playback where the row reads it. */
@Serializable
data class TvCard(
    val id: String,
    val title: String? = null,
    val filename: String = "",
    @SerialName("library_id") val libraryId: String = "",
    @SerialName("duration_sec") val durationSec: Double? = null,
    @SerialName("position_sec") val positionSec: Double? = null,
    val completed: Boolean? = null,
    val series: SeriesName? = null,
) {
    val name: String get() = title?.takeIf { it.isNotBlank() } ?: filename
    val progress: Float
        get() {
            val p = positionSec ?: return 0f
            val d = durationSec ?: return 0f
            return if (d > 0) (p / d).coerceIn(0.0, 1.0).toFloat() else 0f
        }
}

@Serializable
data class SeriesName(val id: String, val name: String = "")

@Serializable
data class CollectionRowRef(val id: String, val name: String = "", @SerialName("source_kind") val sourceKind: String? = null)

@Serializable
data class CollectionRow(val collection: CollectionRowRef, val items: List<TvCard> = emptyList())

/** `GET /api/home`. */
@Serializable
data class TvHome(
    @SerialName("continue_watching") val continueWatching: List<TvCard> = emptyList(),
    @SerialName("on_deck") val onDeck: List<TvCard> = emptyList(),
    @SerialName("recently_added") val recentlyAdded: List<TvCard> = emptyList(),
    @SerialName("recently_watched") val recentlyWatched: List<TvCard> = emptyList(),
    val favorites: List<TvCard> = emptyList(),
    @SerialName("collection_rows") val collectionRows: List<CollectionRow> = emptyList(),
)

@Serializable
data class HubRows(
    @SerialName("on_deck") val onDeck: List<TvCard> = emptyList(),
    @SerialName("continue_watching") val continueWatching: List<TvCard> = emptyList(),
    @SerialName("recently_added") val recentlyAdded: List<TvCard> = emptyList(),
    @SerialName("recently_watched") val recentlyWatched: List<TvCard> = emptyList(),
)

@Serializable
data class HubLibrary(val id: String = "", val name: String = "", val videos: Long = 0)

/** `GET /api/libraries/{id}/hub`: the Recommended tab. */
@Serializable
data class TvHub(
    val library: HubLibrary = HubLibrary(),
    val rows: HubRows = HubRows(),
    @SerialName("collection_rows") val collectionRows: List<CollectionRow> = emptyList(),
)

/** A card of a library's Collections tab. */
@Serializable
data class TvCollection(
    val kind: String,
    val subkind: String? = null,
    val id: String,
    val name: String = "",
    val videos: Long? = null,
    @SerialName("poster_video_ids") val posters: List<String> = emptyList(),
    val automatic: Boolean = false,
    @SerialName("source_kind") val sourceKind: String? = null,
) {
    /** The play queue's collection source: an admin's or a sourced one by id, the pipeline's groups as topic:/clip:. */
    val queueId: String get() = if (kind == "topic" || kind == "clip") "$kind:$id" else id
}

@Serializable
data class TvCollections(val items: List<TvCollection> = emptyList())

@Serializable
data class SmartRules(
    val libraries: List<String>? = null,
    val filters: Map<String, String> = emptyMap(),
    val sort: String? = null,
    val order: String? = null,
    val limit: Int = 200,
)

@Serializable
data class TvPlaylist(
    val id: String,
    val name: String = "",
    val videos: Long = 0,
    @SerialName("duration_sec") val durationSec: Double = 0.0,
    @SerialName("poster_video_ids") val posters: List<String> = emptyList(),
    val kind: String = "manual",
    @SerialName("smart_query") val smartQuery: SmartRules? = null,
) {
    val smart: Boolean get() = kind == "smart"
}

@Serializable
data class TvPlaylists(val items: List<TvPlaylist> = emptyList())

@Serializable
data class TvPlaylistDoc(val playlist: TvPlaylist, val items: List<TvCard> = emptyList())

@Serializable
data class TvCategory(
    val value: String,
    val label: String = "",
    val count: Long = 0,
    @SerialName("poster_video_id") val posterVideoId: String? = null,
)

/** `GET /api/libraries/{id}/categories`. */
@Serializable
data class TvCategories(
    val speakers: List<TvCategory> = emptyList(),
    val topics: List<TvCategory> = emptyList(),
    val languages: List<TvCategory> = emptyList(),
    val years: List<TvCategory> = emptyList(),
)

@Serializable
data class TvEpisode(
    @SerialName("video_id") val videoId: String,
    val title: String = "",
    val season: Int? = null,
    val episode: Int? = null,
    @SerialName("duration_sec") val durationSec: Double? = null,
    @SerialName("progress_pct") val progressPct: Double = 0.0,
    val watched: Boolean = false,
)

@Serializable
data class TvSeason(
    val season: Int,
    val episodes: List<TvEpisode> = emptyList(),
    /** the season's series_seasons row: a season queue's source */
    val id: String? = null,
    val name: String? = null,
    val kind: String? = null,
)

/** `GET /api/series/{id}/episodes?sort=episode`. */
@Serializable
data class TvEpisodes(
    val seasons: List<TvSeason> = emptyList(),
    @SerialName("next_episode") val nextEpisode: TvEpisode? = null,
)

@Serializable
data class TvSeries(val id: String, val name: String = "", val description: String? = null)

/** A card of the grid grouped by series (`GET /api/videos?group=series`). */
@Serializable
data class GroupedCard(
    val kind: String,
    val id: String,
    val name: String? = null,
    val title: String? = null,
    val filename: String? = null,
    @SerialName("poster_video_id") val posterVideoId: String? = null,
    @SerialName("episode_count") val episodeCount: Int = 0,
    @SerialName("unwatched_count") val unwatchedCount: Int? = null,
    @SerialName("duration_sec") val durationSec: Double? = null,
) {
    val label: String get() = name?.takeIf { it.isNotBlank() } ?: title?.takeIf { it.isNotBlank() } ?: filename ?: id
    val picture: String get() = posterVideoId ?: id
}

@Serializable
data class GroupedPage(val items: List<GroupedCard> = emptyList(), val next: String? = null)

@Serializable
data class FacetLetter(val letter: String, val videos: Long = 0)

@Serializable
data class FacetSpeaker(val name: String, val videos: Long = 0)

@Serializable
data class FacetLanguage(val code: String, val videos: Long = 0)

@Serializable
data class TvFacets(
    val letters: List<FacetLetter> = emptyList(),
    val speakers: List<FacetSpeaker> = emptyList(),
    val languages: List<FacetLanguage> = emptyList(),
)

@Serializable
data class TvCardPage(val items: List<TvCard> = emptyList())

@Serializable
data class ItemVideo(val id: String, val title: String? = null, val filename: String = "", @SerialName("duration_sec") val durationSec: Double? = null)

@Serializable
data class CollectionItem(
    @SerialName("video_id") val videoId: String? = null,
    val id: String? = null,
    val title: String? = null,
    val filename: String = "",
    @SerialName("duration_sec") val durationSec: Double? = null,
    val video: ItemVideo? = null,
) {
    /** A manual collection's item ({video_id, video}) or a saved filter's (a video). */
    val card: TvCard?
        get() = when {
            video != null -> TvCard(video.id, video.title, video.filename, durationSec = video.durationSec)
            id != null -> TvCard(id, title, filename, durationSec = durationSec)
            videoId != null -> TvCard(videoId)
            else -> null
        }
}

@Serializable
data class CollectionItems(val items: List<CollectionItem> = emptyList())

@Serializable
data class Added(val added: Int = 0)

@Serializable
data class MyVideo(val series: SeriesName? = null)
