package com.hamzalabs.maktaba.desktop.search

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Facet
import com.hamzalabs.maktaba.core.api.FacetBucket
import com.hamzalabs.maktaba.core.api.FacetChoice
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
import com.hamzalabs.maktaba.core.session.ReadOrder
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay

/** How long typing rests before the field asks for suggestions (story 55.04). */
const val SUGGEST_DELAY_MS = 200L

/** What a submitted search found: Titles and In the transcript, each paged from its kept result set. */
data class SearchAnswer(
    val q: String,
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
}

/**
 * Search on the desktop (story 55.04), as the Android `SearchModel`: suggestions as the person
 * types (after [SUGGEST_DELAY_MS] of rest; an answer for an older word is dropped), a search on
 * Enter (hybrid, or `fts` — words only — while search by meaning is off), more pages of each
 * group, and the recent searches when the field is empty. Removing a recent search, or all of
 * them, shows at once and runs in [ReadOrder.saving]: a read that began before does not bring
 * one back. Kept by the signed-in home.
 */
class SearchModel(
    private val client: ApiClient,
    private val signedOut: () -> Unit = {},
    /** Whether search by meaning is on at the home (story 55.02). */
    var meaningOn: () -> Boolean = { true },
    private val rest: suspend (Long) -> Unit = { delay(it) },
) {
    var query by mutableStateOf("")
    /** Earlier searches, words, titles and speakers, each with its kind (an older home's have none; story 49.11). */
    var suggestions by mutableStateOf<List<Suggestion>>(emptyList())
        private set
    /** The order asked for: the answer's, once there is one. New words keep it. */
    var sort by mutableStateOf(SearchSort.Relevance)
        private set
    /** The facet buckets chosen; new words drop them (they were the old results' counts). */
    var choice by mutableStateOf(FacetChoice())
        private set
    /** Library names seen in the facets, by id: a chosen one the narrowed facets lost keeps its name. */
    var libraryNames by mutableStateOf<Map<String, String>>(emptyMap())
        private set
    /** The suggestion Up and Down have chosen (-1: none). */
    var highlighted by mutableIntStateOf(-1)
    var recent by mutableStateOf<List<RecentSearch>>(emptyList())
        private set
    var answer by mutableStateOf<SearchAnswer?>(null)
        private set
    var searching by mutableStateOf(false)
        private set
    var paging by mutableStateOf(false)
        private set
    var problem by mutableStateOf<Throwable?>(null)
        private set
    var submitted by mutableStateOf<String?>(null)
        private set
    /** The video shown beside the results on a wide window. */
    var chosen by mutableStateOf<String?>(null)

    private var generation = 0
    private var typed = 0
    private val recentOrder = ReadOrder()

    /**
     * The person typed [q]: after a rest, its suggestions. A newer word's call wins: an answer
     * for an older one that arrives later is dropped. Blank asks nothing.
     */
    suspend fun typing(q: String) {
        query = q
        val mine = ++typed
        highlighted = -1
        if (q.isBlank()) {
            suggestions = emptyList()
            return
        }
        rest(SUGGEST_DELAY_MS)
        if (mine != typed) return
        quietly {
            val s = client.searchSuggest(q)
            if (mine == typed) suggestions = s
        }
    }

    /** Up (-1) or Down (+1) through the suggestions. */
    fun move(by: Int) {
        if (suggestions.isEmpty()) return
        highlighted = (highlighted + by).coerceIn(-1, suggestions.lastIndex)
    }

    suspend fun loadRecent() {
        val ticket = recentOrder.begin()
        quietly {
            val r = client.recentSearches()
            if (recentOrder.fresh(ticket)) recent = r
        }
    }

    /**
     * Enter: the highlighted suggestion, else what is typed. A title opens its video
     * ([openVideo]); an earlier search, a word or a speaker is searched. An empty query searches
     * nothing.
     */
    suspend fun enter(openVideo: (String) -> Unit = {}) {
        val s = suggestions.getOrNull(highlighted)
        if (s == null) submit(query) else choose(s, openVideo)
    }

    /** A suggestion chosen: a title opens its video, any other is searched as its text. */
    suspend fun choose(s: Suggestion, openVideo: (String) -> Unit) {
        val video = s.opensVideo
        if (video == null) {
            submit(s.text)
            return
        }
        suggestions = emptyList()
        highlighted = -1
        typed++
        openVideo(video)
    }

    /**
     * Runs [q] in the [sort] on screen. New words drop the filters chosen (they were the old
     * results' counts); the same words, did-you-mean and a recent search keep them ([keepFilters]).
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
        highlighted = -1
        typed++
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
            problem = e
        } finally {
            if (mine == generation) searching = false
        }
        loadRecent()
    }

    /** The last search again, with its order and filters (the error's Try again). */
    suspend fun retry() = again()

    /** The field is cleared: recent searches again, with no filters. */
    fun clear() {
        query = ""
        choice = FacetChoice()
        suggestions = emptyList()
        answer = null
        problem = null
        submitted = null
        typed++
        generation++
        searching = false
    }

    suspend fun moreTitles() = page { a ->
        val p = client.searchTitles(a.searchId, a.titles.size)
        a.copy(titles = a.titles + p.items.filter { n -> a.titles.none { it.videoId == n.videoId } }, titlesTotal = if (p.items.isEmpty()) a.titles.size else p.total)
    }

    suspend fun moreTranscripts() = page { a ->
        val p = client.searchTranscripts(a.searchId, a.transcripts.size)
        a.copy(
            transcripts = a.transcripts + p.items.filter { n -> a.transcripts.none { it.videoId == n.videoId } },
            transcriptsTotal = if (p.items.isEmpty()) a.transcripts.size else p.total,
        )
    }

    /** Removes one recent search: gone at once; back in its place when the home cannot be told. */
    suspend fun forget(r: RecentSearch) {
        val before = recent
        recent = recent.filter { it.searchId != r.searchId }
        val ok = runSaving { client.forgetRecentSearch(r.searchId) }
        if (!ok) recent = before.filter { b -> b.searchId == r.searchId || recent.any { it.searchId == b.searchId } }
    }

    /** Clear all: empty at once; back when the home cannot be told. */
    suspend fun clearRecent() {
        val before = recent
        recent = emptyList()
        if (!runSaving { client.clearRecentSearches() }) recent = before
    }

    private suspend fun runSaving(call: suspend () -> Unit): Boolean = try {
        recentOrder.saving { call() }
        true
    } catch (e: ApiException.SignedOut) {
        signedOut()
        false
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        false
    }

    private suspend fun page(next: suspend (SearchAnswer) -> SearchAnswer) {
        val a = answer ?: return
        if (paging || a.searchId.isEmpty()) return
        val mine = generation
        paging = true
        try {
            quietly {
                val updated = next(a)
                if (mine == generation) answer = updated
            }
        } finally {
            paging = false
        }
    }

    private suspend fun quietly(block: suspend () -> Unit) {
        try {
            block()
        } catch (e: ApiException.SignedOut) {
            signedOut()
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            // suggestions, recent searches and pages are extras: the page stays as it is
        }
    }
}
