package com.hamzalabs.maktaba.tv.data.repository

import com.hamzalabs.maktaba.tv.data.SettingsStore
import com.hamzalabs.maktaba.tv.data.api.ApiProvider
import com.hamzalabs.maktaba.tv.data.api.MaktabaApi
import com.hamzalabs.maktaba.tv.data.api.TokenStore
import com.hamzalabs.maktaba.tv.data.models.Library
import com.hamzalabs.maktaba.tv.data.models.LoginRequest
import com.hamzalabs.maktaba.tv.data.models.Media
import com.hamzalabs.maktaba.tv.data.models.MediaRail
import com.hamzalabs.maktaba.tv.data.models.SearchResult
import com.hamzalabs.maktaba.tv.data.models.WatchHeartbeatRequest
import com.hamzalabs.maktaba.tv.data.models.WatchStartRequest
import com.hamzalabs.maktaba.tv.data.models.WatchStopRequest

/**
 * Single entry point the UI uses for data. Wraps the Retrofit [api] and
 * returns `Result<T>` so screens render an error state instead of
 * crashing on a network hiccup.
 *
 * The repo rebuilds its [api] when the server URL changes — see
 * [rebuild]. A real app would inject this (Hilt); the scaffold keeps a
 * hand-wired instance to stay dependency-light.
 */
class MediaRepository(
    private val settings: SettingsStore,
    private val tokens: TokenStore,
) {
    private var api: MaktabaApi = ApiProvider.create(settings.serverUrl, tokens)

    /** Re-point the client after the user edits the server URL. */
    fun rebuild() {
        api = ApiProvider.create(settings.serverUrl, tokens)
    }

    val isLoggedIn: Boolean get() = tokens.isLoggedIn

    suspend fun login(username: String, password: String): Result<Unit> = runCatching {
        val res = api.login(LoginRequest(username, password))
        tokens.save(res.accessToken, res.refreshToken)
    }

    fun logout() = tokens.clear()

    suspend fun homeRails(): Result<List<MediaRail>> = runCatching {
        api.recommendations().rails
    }

    suspend fun libraries(): Result<List<Library>> = runCatching {
        api.libraries().items
    }

    suspend fun libraryItems(libraryId: String): Result<List<Media>> = runCatching {
        api.libraryItems(libraryId).items.map(SearchResult::asMedia)
    }

    suspend fun search(query: String): Result<List<SearchResult>> = runCatching {
        api.search(query).items
    }

    /** Absolute HLS manifest URL for a video id. */
    fun hlsUrl(videoId: String): String =
        "${settings.serverUrl.trimEnd('/')}/api/stream/$videoId/index.m3u8"

    // Watch analytics (Story 29.1). Failures are swallowed into Result so
    // a dropped beat never interrupts playback.

    /** Open a watch session. Returns null when tracking is paused. */
    suspend fun watchStart(videoId: String): Result<String?> = runCatching {
        val r = api.watchStart(WatchStartRequest(videoId, "tv", "androidtv", "auto"))
        if (r.tracking) r.sessionId else null
    }

    /** Advance an open session with the current position (seconds). */
    suspend fun watchHeartbeat(sessionId: String, positionSec: Long): Result<Unit> = runCatching {
        api.watchHeartbeat(WatchHeartbeatRequest(sessionId, positionSec))
        Unit
    }

    /** Close a session with a final position (seconds). Idempotent. */
    suspend fun watchStop(sessionId: String, positionSec: Long): Result<Unit> = runCatching {
        api.watchStop(WatchStopRequest(sessionId, positionSec))
        Unit
    }
}
