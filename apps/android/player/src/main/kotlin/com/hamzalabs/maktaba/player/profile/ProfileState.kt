package com.hamzalabs.maktaba.player.profile

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.api.HistoryEntry
import com.hamzalabs.maktaba.core.api.PlaylistDoc
import com.hamzalabs.maktaba.core.api.PlaylistSummary
import com.hamzalabs.maktaba.core.api.SignInSession
import com.hamzalabs.maktaba.core.api.VideoDetail
import com.hamzalabs.maktaba.core.api.endSignInSession
import com.hamzalabs.maktaba.core.api.favorites
import com.hamzalabs.maktaba.core.api.forgetHistory
import com.hamzalabs.maktaba.core.api.homeVersion
import com.hamzalabs.maktaba.core.api.playlist
import com.hamzalabs.maktaba.core.api.playlists
import com.hamzalabs.maktaba.core.api.signInSessions
import com.hamzalabs.maktaba.core.api.watchHistory
import kotlinx.coroutines.CancellationException

/** A list a page shows: loading, its rows, or why it could not be read. */
sealed interface Load<out T> {
    data object Loading : Load<Nothing>
    data class Ready<T>(val value: T) : Load<T>
    data class Failed(val failure: Failure) : Load<Nothing>
}

/** The Profile's pages: the section's own, shown in its place (Back returns to [parent]). */
sealed interface ProfilePage {
    data object Root : ProfilePage
    data object History : ProfilePage
    data object Favorites : ProfilePage
    data object Playlists : ProfilePage
    data class Playlist(val id: String, val name: String) : ProfilePage
    data object Sessions : ProfilePage
    data object Playback : ProfilePage
    data object Diagnostics : ProfilePage

    val parent: ProfilePage get() = if (this is Playlist) Playlists else Root
}

/**
 * The Profile's state, kept by the signed-in home (showing another section and coming back
 * finds the same page and lists). The calls run in the screen's scope; a 401 the client could
 * not refresh goes back to signing in ([signedOut]).
 */
class ProfileState(private val client: ApiClient, private val signedOut: () -> Unit) {
    var page: ProfilePage by mutableStateOf(ProfilePage.Root)

    var history: Load<List<HistoryEntry>> by mutableStateOf(Load.Loading)
        private set
    /** The last page of history was full: there may be more. */
    var historyMore: Boolean by mutableStateOf(false)
        private set
    var sessions: Load<List<SignInSession>> by mutableStateOf(Load.Loading)
        private set
    var playlists: Load<List<PlaylistSummary>> by mutableStateOf(Load.Loading)
        private set
    var playlist: Load<PlaylistDoc> by mutableStateOf(Load.Loading)
        private set
    var favorites: Load<List<VideoDetail>> by mutableStateOf(Load.Loading)
        private set
    private var favoritesNext: String? = null
    val favoritesMore: Boolean get() = favoritesNext != null
    var homeVersion: String? by mutableStateOf(null)
        private set

    /** A change that did not go through (a removal, a sign-out of a session), until shown. */
    var problem: Failure? by mutableStateOf(null)

    suspend fun loadHistory() {
        history = Load.Loading
        history = attempt { client.watchHistory(limit = PAGE).also { historyMore = it.size == PAGE } }
    }

    suspend fun moreHistory() {
        val shown = (history as? Load.Ready)?.value ?: return
        runCatchingApi { client.watchHistory(offset = shown.size, limit = PAGE) }?.let { more ->
            historyMore = more.size == PAGE
            history = Load.Ready(shown + more.filter { m -> shown.none { it.videoId == m.videoId } })
        }
    }

    /** Removes [entry] from the history at once; puts it back if the home refuses. */
    suspend fun forget(entry: HistoryEntry) {
        val before = (history as? Load.Ready)?.value ?: return
        history = Load.Ready(before.filterNot { it.videoId == entry.videoId })
        if (runCatchingApi { client.forgetHistory(entry.videoId) } == null) history = Load.Ready(before)
    }

    suspend fun loadSessions() {
        sessions = Load.Loading
        sessions = attempt { client.signInSessions() }
    }

    /** Signs [session] out; it leaves the list once the home has done it. */
    suspend fun endSession(session: SignInSession) {
        runCatchingApi { client.endSignInSession(session.id) } ?: return
        val now = (sessions as? Load.Ready)?.value ?: return
        sessions = Load.Ready(now.filterNot { it.id == session.id })
    }

    suspend fun loadPlaylists() {
        playlists = Load.Loading
        playlists = attempt { client.playlists() }
    }

    suspend fun loadPlaylist(id: String) {
        playlist = Load.Loading
        playlist = attempt { client.playlist(id) }
    }

    /** The first video of playlist [id], to play it from the start (null: empty, or it failed). */
    suspend fun firstOf(id: String): String? = runCatchingApi { client.playlist(id) }?.items?.firstOrNull()?.id

    suspend fun loadFavorites() {
        favorites = Load.Loading
        favorites = attempt { client.favorites().also { favoritesNext = it.next }.items }
    }

    suspend fun moreFavorites() {
        val shown = (favorites as? Load.Ready)?.value ?: return
        val cursor = favoritesNext ?: return
        runCatchingApi { client.favorites(cursor) }?.let { page ->
            favoritesNext = page.next
            favorites = Load.Ready(shown + page.items)
        }
    }

    suspend fun loadHomeVersion() {
        if (homeVersion == null) homeVersion = runCatching { client.homeVersion() }.getOrNull()
    }

    private suspend fun <T> attempt(block: suspend () -> T): Load<T> = try {
        Load.Ready(block())
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        if (e is ApiException.SignedOut) signedOut()
        Load.Failed(Failure.of(e))
    }

    /** [block]'s answer, or null with [problem] set. */
    private suspend fun <T> runCatchingApi(block: suspend () -> T): T? = try {
        block()
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        if (e is ApiException.SignedOut) signedOut()
        problem = Failure.of(e)
        null
    }

    private companion object {
        const val PAGE = 50
    }
}
