package com.hamzalabs.maktaba.desktop.video

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Chapter
import com.hamzalabs.maktaba.core.api.Episode
import com.hamzalabs.maktaba.core.api.MyVideo
import com.hamzalabs.maktaba.core.api.NotAvailable
import com.hamzalabs.maktaba.core.api.VideoDetail
import com.hamzalabs.maktaba.core.api.chapters
import com.hamzalabs.maktaba.core.api.markWatched
import com.hamzalabs.maktaba.core.api.myVideo
import com.hamzalabs.maktaba.core.api.seriesEpisodes
import com.hamzalabs.maktaba.core.api.setFavorite
import com.hamzalabs.maktaba.core.api.videoDetail
import com.hamzalabs.maktaba.core.session.ReadOrder
import com.hamzalabs.maktaba.desktop.ResumePosition
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope

/** How many of the following episodes the page shows. */
private const val NEXT_EPISODES = 12

/**
 * A video's page (story 55.06), as the Android `VideoScreen`: the video, the person's own state
 * of it (favorite, playlists, its series), its chapters when they are on, and the episodes after
 * it. Favorite and Mark watched show at once and run in [ReadOrder.saving]: a read that began
 * before does not undo them; a failure puts them back and [saveFailed] says so.
 */
class VideoPageState(
    private val client: ApiClient,
    val id: String,
    private val signedOut: () -> Unit,
    /** Whether chapters are on now (story 55.02): off, none are asked for. */
    private val chaptersOn: () -> Boolean = { true },
) {
    var detail by mutableStateOf<VideoDetail?>(null)
        private set
    var mine by mutableStateOf<MyVideo?>(null)
        private set
    var chapters by mutableStateOf<List<Chapter>>(emptyList())
        private set
    var nextEpisodes by mutableStateOf<List<Episode>>(emptyList())
        private set
    /** Where this is in its series ("Season 2, Episode 5"), when it is an episode. */
    var episode by mutableStateOf<Episode?>(null)
        private set
    var favorite by mutableStateOf(false)
        private set
    var watched by mutableStateOf(false)
        private set
    var problem by mutableStateOf<Throwable?>(null)
        private set
    var notAvailable by mutableStateOf(false)
        private set
    var saveFailed by mutableStateOf(false)
        private set

    private val order = ReadOrder()

    /** Where Resume starts (seconds), or null: never played, finished, or over 95% in. */
    val resumeSec: Int?
        get() {
            val d = detail ?: return null
            val pos = mine?.positionSec ?: d.playbackState?.positionSec ?: 0.0
            val done = mine?.completed ?: d.playbackState?.completed ?: false
            return ResumePosition.of(pos, done, d.durationSec).takeIf { it > 0 }
        }

    suspend fun load() {
        val ticket = order.begin()
        try {
            coroutineScope {
                val d = async { client.videoDetail(id) }
                val m = async { runCatching { client.myVideo(id) }.getOrNull() }
                val c = async { if (chaptersOn()) runCatching { client.chapters(id) }.getOrDefault(emptyList()) else emptyList() }
                val det = d.await()
                val my = m.await()
                val ch = c.await()
                if (!order.fresh(ticket)) return@coroutineScope
                detail = det
                mine = my
                chapters = ch
                favorite = my?.favorite ?: det.favorite ?: false
                watched = my?.completed ?: det.playbackState?.completed ?: false
                problem = null
            }
        } catch (e: NotAvailable) {
            notAvailable = true
            return
        } catch (e: ApiException.SignedOut) {
            signedOut()
            return
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            problem = e
            return
        }
        loadEpisodes()
    }

    private suspend fun loadEpisodes() {
        val series = mine?.series ?: return
        val all = runCatching { client.seriesEpisodes(series.id) }.getOrNull()?.seasons?.flatMap { it.episodes } ?: return
        val at = all.indexOfFirst { it.videoId == id }
        episode = all.getOrNull(at)
        nextEpisodes = if (at >= 0) all.drop(at + 1).take(NEXT_EPISODES) else emptyList()
    }

    suspend fun toggleFavorite() = save(favorite, { favorite = it }) { client.setFavorite(listOf(id), it) }

    suspend fun toggleWatched() = save(watched, { watched = it }) { client.markWatched(listOf(id), it) }

    private suspend fun save(was: Boolean, show: (Boolean) -> Unit, call: suspend (Boolean) -> Unit) {
        show(!was)
        saveFailed = false
        try {
            order.saving { call(!was) }
        } catch (e: ApiException.SignedOut) {
            signedOut()
        } catch (e: CancellationException) {
            show(was)
            throw e
        } catch (_: Exception) {
            show(was)
            saveFailed = true
        }
    }
}
