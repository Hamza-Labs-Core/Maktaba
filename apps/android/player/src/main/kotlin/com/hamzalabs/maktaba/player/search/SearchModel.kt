package com.hamzalabs.maktaba.player.search

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.api.RecentSearch
import com.hamzalabs.maktaba.core.api.TitleHit
import com.hamzalabs.maktaba.core.api.TranscriptHit
import com.hamzalabs.maktaba.core.api.clearRecentSearches
import com.hamzalabs.maktaba.core.api.forgetRecentSearch
import com.hamzalabs.maktaba.core.api.recentSearches
import com.hamzalabs.maktaba.core.api.search
import com.hamzalabs.maktaba.core.api.searchSuggestions
import com.hamzalabs.maktaba.core.api.searchTitles
import com.hamzalabs.maktaba.core.api.searchTranscripts
import com.hamzalabs.maktaba.core.diagnostics.AppLog
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
) {
    val isEmpty: Boolean get() = titles.isEmpty() && transcripts.isEmpty()
    val moreTitles: Boolean get() = searchId.isNotEmpty() && titles.size < titlesTotal
    val moreTranscripts: Boolean get() = searchId.isNotEmpty() && transcripts.size < transcriptsTotal
}

/**
 * Search's state (FR-19, FR-20), kept by the signed-in home so leaving the section and coming
 * back finds the same query and results. The screen calls the suspend functions from its own
 * scope; every answer is from the home (`/api/search*`), which decides what this user may see.
 */
class SearchModel(private val client: ApiClient, private val signedOut: () -> Unit = {}) {
    /** What is typed in the field. */
    var query by mutableStateOf("")
    /** The caller's own past searches starting with what is typed (`GET /api/search/suggest`). */
    var suggestions by mutableStateOf<List<String>>(emptyList())
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

    private var generation = 0

    /** Asks for the suggestions for [q]; blank asks nothing. A failure keeps the list empty. */
    suspend fun suggest(q: String) {
        if (q.isBlank()) {
            suggestions = emptyList()
            return
        }
        quietly("search suggest") { suggestions = client.searchSuggestions(q) }
    }

    suspend fun loadRecent() = quietly("search recent") { recent = client.recentSearches() }

    /**
     * Runs [q] as typed (the client trims it; nothing else changes, so Arabic is sent as is) in
     * hybrid mode. A newer submit wins over an older one still on its way.
     */
    suspend fun submit(q: String = query) {
        if (q.isBlank()) return
        query = q
        submitted = q
        suggestions = emptyList()
        val mine = ++generation
        searching = true
        problem = null
        try {
            val r = client.search(q)
            if (mine != generation) return
            answer = SearchAnswer(
                q = r.q.ifEmpty { q.trim() },
                searchId = r.searchId,
                titles = r.videos,
                titlesTotal = maxOf(r.videosTotal, r.videos.size),
                transcripts = r.transcriptVideos,
                transcriptsTotal = maxOf(r.transcriptTotal, r.transcriptVideos.size),
                degraded = r.degraded,
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

    /** Submits the last query again (the error's Try again). */
    suspend fun retry() {
        submitted?.let { submit(it) }
    }

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
        client.forgetRecentSearch(r.searchId)
        recent = recent.filter { it.searchId != r.searchId }
    }

    /** Forgets every recent search on the home, then here. */
    suspend fun clearRecent() = quietly("search clear") {
        client.clearRecentSearches()
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
