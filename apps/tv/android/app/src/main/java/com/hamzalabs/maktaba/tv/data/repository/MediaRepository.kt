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
}
