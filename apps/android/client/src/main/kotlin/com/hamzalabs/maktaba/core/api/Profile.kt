package com.hamzalabs.maktaba.core.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// What the Player's Profile reads (Epic 48, story 48.31; FR-25, FR-26, FR-28), as the Apple
// client (`MaktabaKit/Browse.swift`) and the web read it: the person's watch history, their
// signed-in sessions, their playlists and favorites, and the home's version. Every `/api/me`
// route is the caller's own (another person's session or playlist answers 404), so nothing
// here filters by role. Read tolerantly, as Browse.kt: missing fields default, an odd row is
// left out of a list, and a 404 is [NotAvailable].

/** A watched video in the person's history (`GET /api/me/history`), newest first. */
@Serializable
data class HistoryEntry(
    @SerialName("video_id") val videoId: String,
    val title: String = "",
    @SerialName("duration_sec") val durationSec: Double? = null,
    @SerialName("times_watched") val timesWatched: Int = 0,
    @SerialName("last_watched_at") val lastWatchedAt: String? = null,
    @SerialName("position_sec") val positionSec: Double = 0.0,
    val completed: Boolean = false,
) {
    /** How far in the person is (0..1), or null when there is nothing to show. */
    val watchedFraction: Double? get() = if (completed) 1.0 else watchedFraction(positionSec, durationSec, false)
}

/** A signed-in session of the person (`GET /api/me/sessions`): a browser's, on any device. */
@Serializable
data class SignInSession(
    val id: String,
    @SerialName("created_at") val createdAt: String? = null,
    @SerialName("last_seen_at") val lastSeenAt: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
    val ip: String? = null,
    @SerialName("user_agent") val userAgent: String? = null,
    /** The session the request came with (a browser's cookie; never this app's tokens). */
    val current: Boolean = false,
)

/** A playlist of the person (`GET /api/me/playlists`): its visible videos' count and length. */
@Serializable
data class PlaylistSummary(
    val id: String,
    val name: String = "",
    val description: String = "",
    val videos: Long = 0,
    @SerialName("duration_sec") val durationSec: Double = 0.0,
    @Serializable(with = LenientList::class) @SerialName("poster_video_ids") val posterVideoIds: List<String> = emptyList(),
    @SerialName("updated_at") val updatedAt: String? = null,
    @SerialName("hand_ordered") val handOrdered: Boolean = false,
)

/** `GET /api/me/playlists/{id}`: a playlist and its videos, in its order. */
@Serializable
data class PlaylistDoc(
    val playlist: PlaylistSummary,
    @Serializable(with = LenientList::class) val items: List<VideoCard> = emptyList(),
)

@Serializable
internal data class HistoryDoc(@Serializable(with = LenientList::class) val items: List<HistoryEntry> = emptyList())

@Serializable
internal data class SessionsDoc(@Serializable(with = LenientList::class) val items: List<SignInSession> = emptyList())

@Serializable
internal data class PlaylistsDoc(@Serializable(with = LenientList::class) val items: List<PlaylistSummary> = emptyList())

@Serializable
internal data class HomeVersionDoc(val version: String? = null)

/** [s] as one encoded path segment. */
private fun segment(s: String): String = java.net.URLEncoder.encode(s, Charsets.UTF_8.name()).replace("+", "%20")

/** [block], with a 404 as [NotAvailable]. */
private inline fun <T> profile(block: () -> T): T = try {
    block()
} catch (e: ApiException.Status) {
    if (e.code == 404) throw NotAvailable(e) else throw e
}

/** `GET /api/me/history?offset=&limit=`: the person's watched videos, newest first. */
suspend fun ApiClient.watchHistory(offset: Int = 0, limit: Int = 50): List<HistoryEntry> =
    profile { get<HistoryDoc>("/api/me/history", mapOf("offset" to offset.toString(), "limit" to limit.toString())).items }

/** `DELETE /api/me/history/{video}`: forgets one video (its resume point too). */
suspend fun ApiClient.forgetHistory(videoId: String) {
    profile { send(request("DELETE", "/api/me/history/${segment(videoId)}")) }
}

/** `GET /api/me/sessions`: the person's signed-in sessions. */
suspend fun ApiClient.signInSessions(): List<SignInSession> = profile { get<SessionsDoc>("/api/me/sessions").items }

/** `DELETE /api/me/sessions/{id}`: signs that session out. */
suspend fun ApiClient.endSignInSession(id: String) {
    profile { send(request("DELETE", "/api/me/sessions/${segment(id)}")) }
}

/** `GET /api/me/playlists`: the person's playlists, the latest changed first. */
suspend fun ApiClient.playlists(): List<PlaylistSummary> = profile { get<PlaylistsDoc>("/api/me/playlists").items }

/** `GET /api/me/playlists/{id}`: one playlist and its videos. */
suspend fun ApiClient.playlist(id: String): PlaylistDoc = profile { get("/api/me/playlists/${segment(id)}") }

/** The person's favorites, a page at a time (`GET /api/me/videos?favorite=true`, as the web). */
suspend fun ApiClient.favorites(cursor: String? = null, limit: Int = 60): Page<VideoDetail> =
    videoPage(VideoQuery(favorite = true, cursor = cursor, limit = limit))

/** The home's version (`GET /api/system/version`), or null when it does not say. */
suspend fun ApiClient.homeVersion(): String? =
    profile { get<HomeVersionDoc>("/api/system/version").version?.trim()?.takeIf { it.isNotEmpty() } }
