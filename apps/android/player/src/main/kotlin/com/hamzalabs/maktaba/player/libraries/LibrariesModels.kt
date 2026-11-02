package com.hamzalabs.maktaba.player.libraries

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Episode
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.api.HubRows
import com.hamzalabs.maktaba.core.api.LibrarySeriesItem
import com.hamzalabs.maktaba.core.api.LibrarySummary
import com.hamzalabs.maktaba.core.api.Season
import com.hamzalabs.maktaba.core.api.SeriesItem
import com.hamzalabs.maktaba.core.api.VideoDetail
import com.hamzalabs.maktaba.core.api.VideoFacets
import com.hamzalabs.maktaba.core.api.VideoQuery
import com.hamzalabs.maktaba.core.api.VideoSort
import com.hamzalabs.maktaba.core.api.libraryHub
import com.hamzalabs.maktaba.core.api.librarySeries
import com.hamzalabs.maktaba.core.api.librarySummaries
import com.hamzalabs.maktaba.core.api.markWatched
import com.hamzalabs.maktaba.core.api.series
import com.hamzalabs.maktaba.core.api.seriesEpisodes
import com.hamzalabs.maktaba.core.api.seriesPage
import com.hamzalabs.maktaba.core.api.setPinnedLibraries
import com.hamzalabs.maktaba.core.api.videoFacets
import com.hamzalabs.maktaba.core.api.videoPage
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import kotlinx.coroutines.CancellationException

// What the Libraries section's screens hold (FR-14 to FR-17): plain state over the browse client
// (48.8), so the screens draw it and the tests drive it. Every route already scopes to the caller's
// libraries on the home; nothing here filters by role.

/**
 * Runs [block] the way every screen of the section loads: a sign-out goes to the start screen,
 * a cancel stays a cancel, anything else is kept as the [Failure] to show (never the raw answer).
 */
internal suspend fun loading(signedOut: () -> Unit, what: String, onFailure: (Failure) -> Unit, block: suspend () -> Unit) {
    try {
        block()
    } catch (e: ApiException.SignedOut) {
        signedOut()
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        AppLog.failure(what, e)
        onFailure(Failure.of(e))
    }
}

/**
 * The pinned first, in their pin order, then the rest in [order] (the ids as the home sent them;
 * by default the list's own order) (FR-14).
 */
fun pinnedFirst(libraries: List<LibrarySummary>, order: List<String> = libraries.map { it.id }): List<LibrarySummary> {
    val (pinned, rest) = libraries.partition { it.pinned }
    val at = { id: String -> order.indexOf(id).let { i -> if (i < 0) Int.MAX_VALUE else i } }
    return pinned.sortedBy { it.pinPosition ?: Int.MAX_VALUE } + rest.sortedBy { at(it.id) }
}

/** The pins after toggling [id]: unpinned if pinned, else pinned at the end (as the web's sidebar). */
fun togglePins(libraries: List<LibrarySummary>, id: String): List<String> {
    val pins = pinnedFirst(libraries).filter { it.pinned }.map { it.id }
    return if (id in pins) pins - id else pins + id
}

/** The Libraries root: its two tabs. */
enum class RootTab { Libraries, Series }

/** What the list beside the page has chosen (a wide window or the hinge). */
sealed interface Chosen {
    data class Library(val id: String) : Chosen
    data class Series(val id: String) : Chosen
}

/** The Libraries root (FR-14) and the Series tab (FR-16). */
class LibrariesState(private val client: ApiClient, private val signedOut: () -> Unit) {
    var libraries by mutableStateOf<List<LibrarySummary>>(emptyList())
        private set
    var series by mutableStateOf<List<SeriesItem>>(emptyList())
        private set
    var librariesLoaded by mutableStateOf(false)
        private set
    var seriesLoaded by mutableStateOf(false)
        private set
    var problem by mutableStateOf<Failure?>(null)
    var seriesProblem by mutableStateOf<Failure?>(null)
    /** The shown tab; showing the other one shows its first page beside the list. */
    var tab by mutableStateOf(RootTab.Libraries)
        private set
    /** The page beside the list; null picks the first of the shown tab. */
    var chosen by mutableStateOf<Chosen?>(null)
    /** The libraries in the home's order: an unpinned one goes back to its place. */
    private var order: List<String> = emptyList()

    fun show(tab: RootTab) {
        if (tab != this.tab) chosen = null
        this.tab = tab
    }

    suspend fun loadLibraries() = loading(signedOut, "player libraries", { problem = it }) {
        val all = client.librarySummaries()
        order = all.map { it.id }
        libraries = pinnedFirst(all, order)
        problem = null
        librariesLoaded = true
    }

    /** The Series tab's next page, null once the home sent a short one. */
    private var seriesNext: Int? = null
    private var seriesBusy = false
    val moreSeries: Boolean get() = seriesNext != null

    /** The Series tab from its first page (the first look, a refresh, Try again). */
    suspend fun loadSeries() = seriesPage(0)

    /**
     * The Series tab's next page, asked for as the grid nears its end: a home with thousands of
     * series shows the first at once and the rest as the person scrolls.
     */
    suspend fun loadMoreSeries() {
        val page = seriesNext ?: return
        if (!seriesBusy) seriesPage(page)
    }

    private suspend fun seriesPage(page: Int) {
        seriesBusy = true
        try {
            loading(signedOut, "player series", { seriesProblem = it }) {
                val r = client.seriesPage(page)
                val have = if (page == 0) emptySet() else series.map { it.id }.toSet()
                series = (if (page == 0) emptyList() else series) + r.items.filter { it.id !in have }
                seriesNext = if (r.more) page + 1 else null
                seriesProblem = null
                seriesLoaded = true
            }
        } finally {
            seriesBusy = false
        }
    }

    /**
     * Pins or unpins [id] (`PUT /api/me/pinned-libraries`): the grid moves at once, and the home's
     * answer (its order) wins; when the home refuses, the old order comes back.
     */
    suspend fun togglePin(id: String) {
        val before = libraries
        val pins = togglePins(before, id)
        libraries = pinnedFirst(withPins(before, pins), order)
        loading(signedOut, "player pin", { libraries = before; problem = it }) {
            val saved = client.setPinnedLibraries(pins)
            libraries = pinnedFirst(withPins(libraries, saved.libraryIds), order)
        }
    }

    private fun withPins(list: List<LibrarySummary>, pins: List<String>) = list.map { lib ->
        val at = pins.indexOf(lib.id)
        lib.copy(pinned = at >= 0, pinPosition = at.takeIf { it >= 0 })
    }
}

/** A library's tabs (FR-15). */
enum class LibraryTab { Videos, Series }

/** A library's grid sorts: newest first, or A to Z. */
enum class LibrarySort(val sort: VideoSort, val order: String?) {
    Newest(VideoSort.Newest, "desc"),
    Title(VideoSort.Title, "asc"),
}

/** A library's page (FR-15): its hub rows, its videos (by cursor, with facets and sort) and its series. */
class LibraryState(private val client: ApiClient, val id: String, private val signedOut: () -> Unit) {
    var name by mutableStateOf("")
    var rows by mutableStateOf(HubRows())
        private set
    var facets by mutableStateOf(VideoFacets())
        private set
    var tab by mutableStateOf(LibraryTab.Videos)
    var sort by mutableStateOf(LibrarySort.Newest)
    var language by mutableStateOf<String?>(null)
    var speaker by mutableStateOf<String?>(null)

    val videos = mutableStateListOf<VideoDetail>()
    var next by mutableStateOf<String?>(null)
        private set
    var loaded by mutableStateOf(false)
        private set
    var problem by mutableStateOf<Failure?>(null)
    /** The library is gone, or the caller may not see it (404). */
    var notAvailable by mutableStateOf(false)
        private set

    val series = mutableStateListOf<LibrarySeriesItem>()
    private var seriesNext: Int? = 0
    var seriesLoaded by mutableStateOf(false)
        private set
    val moreSeries: Boolean get() = seriesNext != null

    /** The query the grid's next page is asked with. */
    fun query(cursor: String? = null) = VideoQuery(
        library = id,
        sort = sort.sort,
        order = sort.order,
        language = language,
        speaker = speaker,
        cursor = cursor,
    )

    /** The hub rows and the facets: a row or a facet that does not answer is left out, never the page. */
    suspend fun loadHub() {
        loading(signedOut, "player library hub", { if (it.kind == ErrorKind.NotFound) notAvailable = true }) {
            val hub = client.libraryHub(id)
            if (hub.library.name.isNotBlank()) name = hub.library.name
            rows = hub.rows
        }
        loading(signedOut, "player facets", {}) { facets = client.videoFacets(id) }
    }

    /** The grid's first page, for the chosen facets and sort. */
    suspend fun reload() = loading(signedOut, "player library videos", ::failed) {
        val page = client.videoPage(query())
        videos.clear()
        videos.addAll(page.items)
        next = page.next
        problem = null
        loaded = true
    }

    /** The grid's next page, by cursor; null at the end. */
    suspend fun loadMore() {
        val cursor = next ?: return
        next = null
        loading(signedOut, "player library videos", { next = cursor; problem = it }) {
            val page = client.videoPage(query(cursor))
            videos.addAll(page.items.filter { v -> videos.none { it.id == v.id } })
            next = page.next
            problem = null
        }
    }

    /** The Series tab's next page (by offset). */
    suspend fun loadSeries() {
        val offset = seriesNext ?: return
        seriesNext = null
        loading(signedOut, "player library series", { seriesNext = offset; problem = it }) {
            val page = client.librarySeries(id, offset = offset)
            if (offset == 0) series.clear()
            series.addAll(page.items.filter { s -> series.none { it.id == s.id } })
            seriesNext = page.next
            seriesLoaded = true
        }
    }

    private fun failed(f: Failure) {
        if (f.kind == ErrorKind.NotFound) notAvailable = true
        problem = f
        loaded = true
    }
}

/** A series' page (FR-17): its seasons, Continue, and the watched marks. */
class SeriesState(private val client: ApiClient, val id: String, private val signedOut: () -> Unit) {
    var series by mutableStateOf<SeriesItem?>(null)
        private set
    var seasons by mutableStateOf<List<Season>>(emptyList())
        private set
    var next by mutableStateOf<Episode?>(null)
        private set
    var season by mutableStateOf<Int?>(null)
    var loaded by mutableStateOf(false)
        private set
    var problem by mutableStateOf<Failure?>(null)
    val notAvailable: Boolean get() = problem?.kind == ErrorKind.NotFound

    /** The chosen season's episodes. */
    val episodes: List<Episode> get() = seasons.firstOrNull { it.season == season }?.episodes ?: seasons.firstOrNull()?.episodes.orEmpty()

    suspend fun load() = loading(signedOut, "player series page", { problem = it; loaded = true }) {
        series = client.series(id)
        val doc = client.seriesEpisodes(id)
        seasons = doc.seasons
        next = doc.nextEpisode
        // the season of Continue's episode, else the first
        if (season == null || doc.seasons.none { it.season == season }) {
            season = doc.seasons.firstOrNull { s -> s.episodes.any { it.videoId == doc.nextEpisode?.videoId } }?.season
                ?: doc.seasons.firstOrNull()?.season
        }
        problem = null
        loaded = true
    }

    /** Marks [ids] watched or not (`POST /api/me/watched`), then shows them so; the home's answer is kept on the next load. */
    suspend fun mark(ids: List<String>, watched: Boolean) {
        if (ids.isEmpty()) return
        loading(signedOut, "player mark watched", { problem = it }) {
            client.markWatched(ids, watched)
            seasons = seasons.map { s ->
                s.copy(episodes = s.episodes.map { e -> if (e.videoId in ids) e.copy(watched = watched, progressPct = if (watched) 100.0 else 0.0) else e })
            }
            series = series?.let { s ->
                s.copy(watchedCount = seasons.sumOf { se -> se.episodes.count { it.watched } })
            }
        }
    }

    /** Marks every episode of the chosen season watched (or not, when all already are). */
    suspend fun markSeason() {
        val eps = episodes
        mark(eps.map { it.videoId }, watched = !eps.all { it.watched })
    }
}
