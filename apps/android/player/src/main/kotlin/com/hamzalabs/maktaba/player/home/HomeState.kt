package com.hamzalabs.maktaba.player.home

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.api.HomeRails
import com.hamzalabs.maktaba.core.api.NotAvailable
import com.hamzalabs.maktaba.core.api.NowAnswer
import com.hamzalabs.maktaba.core.api.Recommendations
import com.hamzalabs.maktaba.core.api.VideoCard
import com.hamzalabs.maktaba.core.api.dismissRecommendation
import com.hamzalabs.maktaba.core.api.homeRails
import com.hamzalabs.maktaba.core.api.recommendations
import com.hamzalabs.maktaba.core.api.videoDetail
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.withContext

/**
 * Home's data (FR-10 to FR-13), kept by the signed-in home ([PlayerHome.keep][com.hamzalabs.maktaba.player.shell.PlayerHome.keep])
 * so another section and back finds it, and on disk ([cache]) so the next launch does:
 * - [open] shows what is kept at once and reads Home again behind it (FR-12);
 * - `GET /api/home` is what Home needs; the recommendations and Live TV on now are extra rows,
 *   and a failure of either leaves its rows out (or as they were) without failing Home;
 * - a refresh that fails while rows are shown keeps them and says so ([stale]).
 *
 * Every route scopes to the person's libraries on the home (NFR-12): nothing here filters by role.
 */
class HomeState(
    private val client: ApiClient,
    private val cache: HomeCache?,
    /** `GET /api/channels/now`, or null when there is no Live TV for this person. */
    private val liveNow: suspend () -> NowAnswer?,
    private val signedOut: () -> Unit,
) {
    var rails by mutableStateOf<HomeRails?>(null)
        private set
    var recs by mutableStateOf<Recommendations?>(null)
        private set
    var now by mutableStateOf<NowAnswer?>(null)
        private set

    /** Videos read for recommendation cards (`GET /api/videos/{id}`), by id. */
    val known = mutableStateMapOf<String, VideoCard>()

    /** Why Home could not load, when there is nothing to show instead. */
    var problem by mutableStateOf<Failure?>(null)
        private set

    /** A refresh failed: the rows shown are the last ones. */
    var stale by mutableStateOf(false)
        private set

    /** A read is under way (the first one, or pull to refresh). */
    var loading by mutableStateOf(false)
        private set

    private var restored = false
    private val asking = mutableSetOf<String>()

    /** The rows to show under the hero, in FR-10's order, empty rows left out. */
    val rows: List<HomeRow> get() = rails?.let { homeRows(it, recs, now, known) }.orEmpty()

    /** Home answered and has nothing at all (FR-13). */
    val empty: Boolean get() = rails?.let { isEmptyHome(it, rows) } == true

    /** Each time Home shows: what is kept first (from disk on a new launch), then a fresh read. */
    suspend fun open() {
        if (!restored) {
            restored = true
            if (rails == null) {
                withContext(Dispatchers.IO) { cache?.read() }?.let { kept ->
                    rails = kept.rails
                    recs = kept.recs
                    known.putAll(kept.known)
                }
            }
        }
        load()
    }

    /** Reads Home, its recommendations and Live TV on now together. */
    suspend fun load() {
        loading = true
        try {
            coroutineScope {
                val r = async { runCatching { client.recommendations() } }
                val n = async { runCatching { liveNow() } }
                val h = try {
                    client.homeRails()
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ApiException.SignedOut) {
                    signedOut()
                    return@coroutineScope
                } catch (e: Exception) {
                    AppLog.failure("player home", e)
                    if (rails == null) problem = Failure.of(e) else stale = true
                    return@coroutineScope
                }
                rails = h
                problem = null
                stale = false
                r.await().onSuccess { recs = it }.onFailure { extraFailed("recommendations", it) }
                n.await().onSuccess { now = it }.onFailure { extraFailed("live now", it); now = null }
                save()
            }
        } finally {
            loading = false
        }
    }

    private fun extraFailed(what: String, e: Throwable) {
        if (e is CancellationException) throw e
        if (e is ApiException.SignedOut) signedOut() else AppLog.failure("player home $what", e)
    }

    /** Reads [videoId] for a recommendation's card; a video the person may no longer see leaves the rows. */
    suspend fun resolve(videoId: String) {
        if (videoId in known || !asking.add(videoId)) return
        try {
            val v = client.videoDetail(videoId)
            known[videoId] = VideoCard(id = v.id, title = v.displayTitle, filename = v.filename, libraryId = v.libraryId, durationSec = v.durationSec)
            save()
        } catch (e: CancellationException) {
            throw e
        } catch (e: NotAvailable) {
            drop(videoId)
        } catch (e: ApiException.SignedOut) {
            signedOut()
        } catch (e: Exception) {
            AppLog.failure("player home video", e)
        } finally {
            asking -= videoId
        }
    }

    /**
     * Not interested (FR-11): the card leaves its rail at once, and the home is told
     * (`DELETE /api/recommendations/items/{id}`); when it cannot be told, the card comes back.
     * True when the home took it.
     */
    suspend fun dismiss(videoId: String): Boolean {
        val before = recs
        drop(videoId)
        return try {
            client.dismissRecommendation(videoId)
            save()
            true
        } catch (e: CancellationException) {
            recs = before
            throw e
        } catch (e: ApiException.SignedOut) {
            signedOut()
            false
        } catch (e: Exception) {
            AppLog.failure("player home not interested", e)
            recs = before
            false
        }
    }

    private fun drop(videoId: String) {
        val r = recs ?: return
        recs = r.copy(rails = r.rails.map { rail -> rail.copy(items = rail.items.filterNot { it.videoId == videoId }) })
    }

    private suspend fun save() {
        val r = rails ?: return
        val kept = CachedHome(r, recs, known.toMap())
        withContext(Dispatchers.IO) { cache?.write(kept) }
    }
}
