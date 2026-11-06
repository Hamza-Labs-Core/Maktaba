package com.hamzalabs.maktaba.desktop.profile

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.CloudLinkState
import com.hamzalabs.maktaba.core.api.HistoryEntry
import com.hamzalabs.maktaba.core.api.NotAvailable
import com.hamzalabs.maktaba.core.api.PlaylistDoc
import com.hamzalabs.maktaba.core.api.PlaylistSummary
import com.hamzalabs.maktaba.core.api.SignInSession
import com.hamzalabs.maktaba.core.api.VideoDetail
import com.hamzalabs.maktaba.core.api.cloudLink
import com.hamzalabs.maktaba.core.api.endSignInSession
import com.hamzalabs.maktaba.core.api.favorites
import com.hamzalabs.maktaba.core.api.forgetHistory
import com.hamzalabs.maktaba.core.api.homeVersion
import com.hamzalabs.maktaba.core.api.playlist
import com.hamzalabs.maktaba.core.api.playlists
import com.hamzalabs.maktaba.core.api.setFavorite
import com.hamzalabs.maktaba.core.api.signInSessions
import com.hamzalabs.maktaba.core.api.watchHistory
import com.hamzalabs.maktaba.core.session.ReadOrder
import kotlinx.coroutines.CancellationException

/** How many history rows a page holds. */
const val HISTORY_PAGE = 50

/** A list of Profile: its rows, whether it loaded, why not, and whether the home has it at all. */
class ProfileList<T>(private val key: (T) -> String) {
    var items by mutableStateOf<List<T>>(emptyList())
        internal set
    var loaded by mutableStateOf(false)
        internal set
    var problem by mutableStateOf<Throwable?>(null)
        internal set
    /** An older home without the route (404): the group is not shown. */
    var missing by mutableStateOf(false)
        internal set
    /** More pages after these. */
    var more by mutableStateOf(false)
        internal set
    /** The last change did not work: the row is back. */
    var failed by mutableStateOf(false)
        internal set
    internal val order = ReadOrder()
    internal var next: String? = null

    internal fun id(t: T) = key(t)
}

/**
 * Profile on the desktop (story 55.09), as the Android `ProfileState`: watch history (paged),
 * favorites (paged), playlists, the browser sessions signed in as this person, the home's
 * version and the Maktaba Cloud link. Forget, remove a favorite and end a session show at once
 * and run in their list's [ReadOrder.saving]: a read that began before does not bring the row
 * back; a failure puts it back and the list's `failed` says so.
 */
class ProfileState(private val client: ApiClient, private val signedOut: () -> Unit = {}) {
    val history = ProfileList<HistoryEntry> { it.videoId }
    val favorites = ProfileList<VideoDetail> { it.id }
    val playlists = ProfileList<PlaylistSummary> { it.id }
    val sessions = ProfileList<SignInSession> { it.id }
    var homeVersion by mutableStateOf<String?>(null)
        private set
    var cloud by mutableStateOf<CloudLinkState?>(null)
        private set

    suspend fun loadHistory() = first(history) {
        val page = client.watchHistory(0, HISTORY_PAGE)
        Page(page, more = page.size >= HISTORY_PAGE)
    }

    suspend fun moreHistory() = more(history) {
        val page = client.watchHistory(history.items.size, HISTORY_PAGE)
        Page(page, more = page.size >= HISTORY_PAGE)
    }

    suspend fun loadFavorites() = first(favorites) {
        val p = client.favorites()
        Page(p.items, more = p.next != null, next = p.next)
    }

    suspend fun moreFavorites() = more(favorites) {
        val p = client.favorites(favorites.next)
        Page(p.items, more = p.next != null, next = p.next)
    }

    suspend fun loadPlaylists() = first(playlists) { Page(client.playlists()) }

    suspend fun loadSessions() = first(sessions) { Page(client.signInSessions()) }

    suspend fun loadAbout() {
        homeVersion = runCatching { client.homeVersion() }.getOrNull()
        cloud = runCatching { client.cloudLink() }.getOrNull()
    }

    /** One playlist and its videos. */
    suspend fun playlist(id: String): PlaylistDoc = client.playlist(id)

    suspend fun forget(e: HistoryEntry) = remove(history, e) { client.forgetHistory(e.videoId) }

    suspend fun unfavorite(v: VideoDetail) = remove(favorites, v) { client.setFavorite(listOf(v.id), false) }

    suspend fun endSession(s: SignInSession) = remove(sessions, s) { client.endSignInSession(s.id) }

    /**
     * A page read: its rows, whether more come after, and the cursor to them. The list takes
     * the cursor only with the rows, when the answer is still fresh: a stale answer never moves
     * it past a page the list does not have.
     */
    private class Page<T>(val items: List<T>, val more: Boolean = false, val next: String? = null)

    private fun <T> ProfileList<T>.cursor(p: Page<T>) {
        more = p.more
        next = p.next
    }

    private suspend fun <T> first(list: ProfileList<T>, read: suspend () -> Page<T>) {
        val ticket = list.order.begin()
        guard(list) {
            val r = read()
            if (!list.order.fresh(ticket)) return@guard
            list.cursor(r)
            list.items = r.items
            list.loaded = true
            list.problem = null
        }
    }

    private suspend fun <T> more(list: ProfileList<T>, read: suspend () -> Page<T>) {
        if (!list.more) return
        val ticket = list.order.begin()
        guard(list) {
            val r = read()
            if (!list.order.fresh(ticket)) return@guard
            list.cursor(r)
            val have = list.items.map(list::id).toSet()
            list.items = list.items + r.items.filter { list.id(it) !in have }
        }
    }

    private suspend fun <T> remove(list: ProfileList<T>, row: T, call: suspend () -> Unit) {
        val before = list.items
        val at = before.indexOfFirst { list.id(it) == list.id(row) }
        list.items = before.filter { list.id(it) != list.id(row) }
        list.failed = false
        try {
            list.order.saving { call() }
        } catch (e: ApiException.SignedOut) {
            signedOut()
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            // only that row back, in its place
            list.items = list.items.toMutableList().apply { add(at.coerceIn(0, size), row) }
            list.failed = true
        }
    }

    private suspend fun <T> guard(list: ProfileList<T>, block: suspend () -> Unit) {
        try {
            block()
        } catch (e: NotAvailable) {
            list.missing = true
            list.loaded = true
        } catch (e: ApiException.SignedOut) {
            signedOut()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            list.problem = e
        }
    }
}
