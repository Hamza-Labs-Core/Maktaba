package com.hamzalabs.maktaba.desktop.libraries

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Episode
import com.hamzalabs.maktaba.core.api.NotAvailable
import com.hamzalabs.maktaba.core.api.SeriesEpisodes
import com.hamzalabs.maktaba.core.api.SeriesItem
import com.hamzalabs.maktaba.core.api.markWatched
import com.hamzalabs.maktaba.core.api.series
import com.hamzalabs.maktaba.core.api.seriesEpisodes
import com.hamzalabs.maktaba.core.session.ReadOrder
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope

/**
 * A series' page (story 55.06), as the Android `SeriesPage`: the series, its seasons and
 * episodes, and Continue watching. Marking an episode or a season watched shows at once and runs
 * in [ReadOrder.saving]; a failure puts it back and [saveFailed] says so.
 */
class SeriesState(private val client: ApiClient, val id: String, private val signedOut: () -> Unit) {
    var series by mutableStateOf<SeriesItem?>(null)
        private set
    var episodes by mutableStateOf<SeriesEpisodes?>(null)
        private set
    var problem by mutableStateOf<Throwable?>(null)
        private set
    var notAvailable by mutableStateOf(false)
        private set
    var saveFailed by mutableStateOf(false)
        private set
    /** The season shown (its number). */
    var season by mutableStateOf<Int?>(null)

    private val order = ReadOrder()

    suspend fun load() {
        val ticket = order.begin()
        try {
            loading(signedOut, { problem = it }) {
                coroutineScope {
                    val s = async { client.series(id) }
                    val e = async { client.seriesEpisodes(id) }
                    val si = s.await()
                    val ep = e.await()
                    if (!order.fresh(ticket)) return@coroutineScope
                    series = si
                    episodes = ep
                    if (season == null || ep.seasons.none { it.season == season }) season = continueEpisode()?.season ?: ep.seasons.firstOrNull()?.season
                    problem = null
                }
            }
        } catch (e: NotAvailable) {
            notAvailable = true
        }
    }

    /**
     * Continue watching: the first episode not watched in the series' order (from where it was
     * left), else the home's next episode.
     */
    fun continueEpisode(): Episode? {
        val all = episodes?.seasons?.flatMap { it.episodes }.orEmpty()
        return all.firstOrNull { !it.watched } ?: episodes?.nextEpisode
    }

    /** Marks [ids] watched (or not), shown at once. */
    suspend fun mark(ids: List<String>, watched: Boolean) {
        val before = episodes ?: return
        episodes = before.copy(
            seasons = before.seasons.map { s ->
                s.copy(episodes = s.episodes.map { if (it.videoId in ids) it.copy(watched = watched, progressPct = if (watched) 100.0 else 0.0) else it })
            },
        )
        saveFailed = false
        loading(signedOut, { revert(before, ids); saveFailed = true }) {
            order.saving { client.markWatched(ids, watched) }
        }
    }

    /** Only [ids] back as they were (anything newer shown stays). */
    private fun revert(before: SeriesEpisodes, ids: List<String>) {
        val now = episodes ?: return
        val was = before.seasons.flatMap { it.episodes }.associateBy { it.videoId }
        episodes = now.copy(seasons = now.seasons.map { s -> s.copy(episodes = s.episodes.map { e -> if (e.videoId in ids) was[e.videoId] ?: e else e }) })
    }

    /** The season numbered [n]'s episode ids. */
    fun seasonIds(n: Int): List<String> = episodes?.seasons?.firstOrNull { it.season == n }?.episodes?.map { it.videoId }.orEmpty()
}
