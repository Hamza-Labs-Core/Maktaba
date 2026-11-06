package com.hamzalabs.maktaba.desktop.libraries

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.HubRows
import com.hamzalabs.maktaba.core.api.LibrarySeriesItem
import com.hamzalabs.maktaba.core.api.NotAvailable
import com.hamzalabs.maktaba.core.api.SeriesSort
import com.hamzalabs.maktaba.core.api.VideoDetail
import com.hamzalabs.maktaba.core.api.VideoFacets
import com.hamzalabs.maktaba.core.api.VideoQuery
import com.hamzalabs.maktaba.core.api.VideoSort
import com.hamzalabs.maktaba.core.api.libraryHub
import com.hamzalabs.maktaba.core.api.librarySeries
import com.hamzalabs.maktaba.core.api.videoFacets
import com.hamzalabs.maktaba.core.api.videoPage

/** A library's tabs. */
enum class LibraryTab { Videos, Series }

/** A library grid's sorts: newest first, or A to Z. */
enum class LibrarySort(val key: String, val sort: VideoSort, val order: String?) {
    Newest("lib.sort.newest", VideoSort.Newest, "desc"),
    Title("lib.sort.title", VideoSort.Title, "asc"),
}

/**
 * A library's page (story 55.05), or every library's videos when [id] is null ("All videos"):
 * its hub rows, its videos a page at a time with sort and filters (language, speaker), and its
 * series. A change of filter or sort starts the grid again, and a page asked for under the old
 * query is dropped; a video seen on an earlier page is not shown twice.
 */
class LibraryVideos(private val client: ApiClient, val id: String?, private val signedOut: () -> Unit) {
    var name by mutableStateOf("")
        private set
    var rows by mutableStateOf(HubRows())
        private set
    var facets by mutableStateOf(VideoFacets())
        private set
    var tab by mutableStateOf(LibraryTab.Videos)
    var sort by mutableStateOf(LibrarySort.Newest)
        private set
    var language by mutableStateOf<String?>(null)
        private set
    var speaker by mutableStateOf<String?>(null)
        private set

    val videos = mutableStateListOf<VideoDetail>()
    var next by mutableStateOf<String?>(null)
        private set
    var loaded by mutableStateOf(false)
        private set
    var busy by mutableStateOf(false)
        private set
    var problem by mutableStateOf<Throwable?>(null)
        private set
    var notAvailable by mutableStateOf(false)
        private set

    val series = mutableStateListOf<LibrarySeriesItem>()
    var seriesLoaded by mutableStateOf(false)
        private set
    private var seriesNext: Int? = 0
    val moreSeries: Boolean get() = seriesNext != null

    /** Bumped by every new query: a page that answers for an older one is dropped. */
    private var generation = 0

    fun query(cursor: String? = null) = VideoQuery(
        library = id, sort = sort.sort, order = sort.order, language = language, speaker = speaker, cursor = cursor,
    )

    /** The hub, the facets and the first page. */
    suspend fun open() {
        if (id != null) {
            try {
                val hub = client.libraryHub(id)
                name = hub.library.name
                rows = hub.rows
            } catch (e: NotAvailable) {
                notAvailable = true
                return
            } catch (e: Exception) {
                loading(signedOut, { problem = it }) { throw e }
            }
        }
        loading(signedOut, {}) { facets = client.videoFacets(id) }
        restart()
    }

    suspend fun choose(sort: LibrarySort = this.sort, language: String? = this.language, speaker: String? = this.speaker) {
        this.sort = sort
        this.language = language
        this.speaker = speaker
        restart()
    }

    /** The grid from its first page, for the query now. */
    suspend fun restart() {
        val mine = ++generation
        videos.clear()
        next = null
        loaded = false
        page(null, mine)
    }

    /** The next page, as the grid nears its end. */
    suspend fun more() {
        val cursor = next ?: return
        if (!busy) page(cursor, generation)
    }

    private suspend fun page(cursor: String?, mine: Int) {
        busy = true
        try {
            loading(signedOut, { if (mine == generation) problem = it }) {
                val p = client.videoPage(query(cursor))
                if (mine != generation) return@loading
                val have = videos.map { it.id }.toSet()
                videos.addAll(p.items.filter { it.id !in have })
                next = p.next
                loaded = true
                problem = null
            }
        } catch (e: NotAvailable) {
            notAvailable = true
        } finally {
            if (mine == generation) busy = false
        }
    }

    suspend fun loadSeries() {
        val lib = id ?: return
        val offset = seriesNext ?: return
        loading(signedOut, { problem = it }) {
            val p = client.librarySeries(lib, SeriesSort.Name, offset)
            val have = series.map { it.id }.toSet()
            series.addAll(p.items.filter { it.id !in have })
            seriesNext = p.next
            seriesLoaded = true
        }
    }
}
