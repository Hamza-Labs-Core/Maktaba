package com.hamzalabs.maktaba.core.search

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Facet
import com.hamzalabs.maktaba.core.api.FacetBucket
import com.hamzalabs.maktaba.core.api.FacetChoice
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.api.RecentSearch
import com.hamzalabs.maktaba.core.api.SearchFacets
import com.hamzalabs.maktaba.core.api.SearchSort
import com.hamzalabs.maktaba.core.api.Suggestion
import com.hamzalabs.maktaba.core.api.TitleHit
import com.hamzalabs.maktaba.core.api.TranscriptHit
import com.hamzalabs.maktaba.core.api.clearRecentSearches
import com.hamzalabs.maktaba.core.api.forgetRecentSearch
import com.hamzalabs.maktaba.core.api.recentSearches
import com.hamzalabs.maktaba.core.api.search
import com.hamzalabs.maktaba.core.api.didYouMean
import com.hamzalabs.maktaba.core.api.searchSuggest
import com.hamzalabs.maktaba.core.api.searchTitles
import com.hamzalabs.maktaba.core.api.searchTranscripts
import com.hamzalabs.maktaba.core.api.withLibraries
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import com.hamzalabs.maktaba.core.session.ReadOrder
import kotlinx.coroutines.CancellationException

/** How long typing rests before the field asks for suggestions. */
const val SUGGEST_DELAY_MS = 250L

/** What a submitted search found: Titles and In the transcript, each paged from its kept result set. */
data class SearchAnswer(
    /** the query as it was sent */
    val q: String,
    /** the kept result set (empty: not kept, no more pages) */
    val searchId: String,
    val titles: List<TitleHit>,
    val titlesTotal: Int,
    val transcripts: List<TranscriptHit>,
    val transcriptsTotal: Int,
    /** the meaning half did not answer: text matches only */
    val degraded: Boolean,
    /** the order the lists are in (story 49.8) */
    val sort: SearchSort = SearchSort.Relevance,
    /** the corrected query worth offering; null when none (story 49.8) */
    val didYouMean: String? = null,
    /** counts of the videos found, from the first page; null from an older home or when none were found */
    val facets: SearchFacets? = null,
) {
    val isEmpty: Boolean get() = titles.isEmpty() && transcripts.isEmpty()
    val moreTitles: Boolean get() = searchId.isNotEmpty() && titles.size < titlesTotal
    val moreTranscripts: Boolean get() = searchId.isNotEmpty() && transcripts.size < transcriptsTotal

    /** The video a page beside the results shows when none is chosen: the first title, else the first transcript's. */
    val first: String? get() = titles.firstOrNull()?.videoId ?: transcripts.firstOrNull()?.videoId
}

/**
 * Search's state (FR-19, FR-20), kept by the signed-in home so leaving the section and coming
 * back finds the same query and results. The Android Player's and the Android TV app's (story
 * 49.11): one model, each app its own screen. The screen calls the suspend functions from its own
 * scope; every answer is from the home (`/api/search*`), which decides what this user may see.
 */
class SearchModel(private val client: ApiClient, private val signedOut: () -> Unit = {}) {
    /** What is typed in the field. */
    var query by mutableStateOf("")
    /**
     * What is typed, completed (`GET /api/search/suggest`): earlier searches, words, titles and
     * speakers, each with its kind (an older home's have none).
     */
    var suggestions by mutableStateOf<List<Suggestion>>(emptyList())
        private set
    /** The order asked for (story 49.11): the answer's, once there is one. New words keep it. */
    var sort by mutableStateOf(SearchSort.Relevance)
        private set
    /** The facet buckets chosen; new words drop them (they were the old results' counts). */
    var choice by mutableStateOf(FacetChoice())
        private set
    /** Library names seen in the facets, by id: a chosen one the narrowed facets lost keeps its name. */
    var libraryNames by mutableStateOf<Map<String, String>>(emptyMap())
        private set
    /** Recent searches, newest first (`GET /api/search/recent`). */
    var recent by mutableStateOf<List<RecentSearch>>(emptyList())
        private set
    /** The last submitted search's answer; null before the first. */
    var answer by mutableStateOf<SearchAnswer?>(null)
        private set
    /** A search is on its way. */
    var searching by mutableStateOf(false)
        private set
    /** A page after the first is on its way. */
    var paging by mutableStateOf(false)
        private set
    /** Why the last search failed; null when it did not. */
    var problem by mutableStateOf<Failure?>(null)
        private set
    /** The query of the last submit (what Try again sends again). */
    var submitted by mutableStateOf<String?>(null)
        private set

    /**
     * The video shown beside the results when the window is half folded like a book (the
     * other page); null: the first result. A new search forgets it.
     */
    var chosen by mutableStateOf<String?>(null)

    /**
     * Whether search by meaning is on at the home (story 51.11): off, a search asks for keyword
     * results only (`fts`). Until the home says, it is on.
     */
    var meaningOn: () -> Boolean = { true }

    private var generation = 0

    /** The suggestions asked for last: an older word's answer that arrives later is dropped. */
    private var suggested = 0

    /**
     * Asks for the suggestions for [q]; blank asks nothing. A failure keeps the list empty. A
     * newer word's ask, or a search, wins over one still on its way.
     */
    suspend fun suggest(q: String) {
        val mine = ++suggested
        if (q.isBlank()) {
            suggestions = emptyList()
            return
        }
        quietly("search suggest") {
            val s = client.searchSuggest(q)
            if (mine == suggested) suggestions = s
        }
    }

    /** The order of the recent searches' reads and their removals: a read older than one is dropped. */
    private val recentOrder = ReadOrder()

    suspend fun loadRecent() {
        val ticket = recentOrder.begin()
        quietly("search recent") {
            val r = client.recentSearches()
            if (recentOrder.fresh(ticket)) recent = r
        }
    }

    /**
     * Runs [q] as typed (the client trims it; nothing else changes, so Arabic is sent as is) in
     * hybrid mode, or keyword only while search by meaning is off ([meaningOn]), in the [sort]
     * on screen. New words drop the filters chosen (they were the old results' counts); the same
     * words, did-you-mean and a recent search keep them ([keepFilters]). A newer submit wins
     * over an older one still on its way.
     */
    suspend fun submit(q: String = query, keepFilters: Boolean = q.trim() == submitted?.trim()) {
        if (q.isBlank()) return
        if (!keepFilters) choice = FacetChoice()
        run(q)
    }

    /** Did you mean: runs the corrected words with the order and filters kept. */
    suspend fun acceptDidYouMean() {
        answer?.didYouMean?.let { submit(it, keepFilters = true) }
    }

    /** Another order: the same search again. */
    suspend fun sortBy(s: SearchSort) {
        if (s == sort) return
        sort = s
        again()
    }

    /** A facet chip pressed: its bucket joins the filters, or leaves them; the search runs again. */
    suspend fun toggle(f: Facet, bucket: FacetBucket) {
        choice = choice.toggle(f, bucket)
        again()
    }

    /** One filter in use taken off. */
    suspend fun remove(f: Facet, value: String) {
        if (!choice.has(f, value)) return
        choice = choice.remove(f, value)
        again()
    }

    /** Clear all filters. */
    suspend fun clearFilters() {
        if (choice.isEmpty) return
        choice = FacetChoice()
        again()
    }

    private suspend fun again() {
        submitted?.let { run(it) }
    }

    private suspend fun run(q: String) {
        query = q
        submitted = q
        chosen = null
        suggestions = emptyList()
        suggested++
        val mine = ++generation
        searching = true
        problem = null
        try {
            val asked = sort
            val r = client.search(q, mode = if (meaningOn()) "hybrid" else "fts", sort = asked, filters = choice.filters())
            if (mine != generation) return
            val shown = if (r.sort.isBlank()) asked else SearchSort.of(r.sort)
            sort = shown
            libraryNames = libraryNames.withLibraries(r.facets)
            val asSent = r.q.ifEmpty { q.trim() }
            answer = SearchAnswer(
                q = asSent,
                searchId = r.searchId,
                titles = r.videos,
                titlesTotal = maxOf(r.videosTotal, r.videos.size),
                transcripts = r.transcriptVideos,
                transcriptsTotal = maxOf(r.transcriptTotal, r.transcriptVideos.size),
                // meaning switched off is not a failure: nothing says the results are degraded
                degraded = r.degraded && r.semantic != "off",
                sort = shown,
                didYouMean = didYouMean(asSent, r.didYouMean),
                facets = r.facets,
            )
        } catch (e: ApiException.SignedOut) {
            signedOut()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            if (mine != generation) return
            AppLog.failure("player search", e)
            problem = Failure.of(e)
        } finally {
            if (mine == generation) searching = false
        }
        // the search is now the newest recent one
        loadRecent()
    }

    /** Submits the last query again, with its order and filters (the error's Try again). */
    suspend fun retry() = again()

    /** The next page of Titles. */
    suspend fun moreTitles() = page { a ->
        val p = client.searchTitles(a.searchId, a.titles.size)
        a.copy(titles = a.titles + p.items.filter { n -> a.titles.none { it.videoId == n.videoId } }, titlesTotal = if (p.items.isEmpty()) a.titles.size else p.total)
    }

    /** The next page of In the transcript. */
    suspend fun moreTranscripts() = page { a ->
        val p = client.searchTranscripts(a.searchId, a.transcripts.size)
        a.copy(
            transcripts = a.transcripts + p.items.filter { n -> a.transcripts.none { it.videoId == n.videoId } },
            transcriptsTotal = if (p.items.isEmpty()) a.transcripts.size else p.total,
        )
    }

    /** Forgets one recent search on the home, then here. */
    suspend fun forget(r: RecentSearch) = quietly("search forget") {
        recentOrder.saving { client.forgetRecentSearch(r.searchId) }
        recent = recent.filter { it.searchId != r.searchId }
    }

    /** Forgets every recent search on the home, then here. */
    suspend fun clearRecent() = quietly("search clear") {
        recentOrder.saving { client.clearRecentSearches() }
        recent = emptyList()
    }

    private suspend fun page(next: suspend (SearchAnswer) -> SearchAnswer) {
        val a = answer ?: return
        if (paging || a.searchId.isEmpty()) return
        val mine = generation
        paging = true
        try {
            quietly("search page") {
                val updated = next(a)
                if (mine == generation) answer = updated
            }
        } finally {
            paging = false
        }
    }

    private suspend fun quietly(where: String, block: suspend () -> Unit) {
        try {
            block()
        } catch (e: ApiException.SignedOut) {
            signedOut()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            AppLog.failure(where, e)
        }
    }
}
