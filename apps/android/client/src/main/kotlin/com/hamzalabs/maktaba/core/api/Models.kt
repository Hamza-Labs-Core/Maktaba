package com.hamzalabs.maktaba.core.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import okhttp3.HttpUrl
import java.time.OffsetDateTime

/** A signed-in user (`user` in the sign-in answer, `GET /api/auth/me`). */
@Serializable
data class User(
    val id: String,
    val username: String,
    @SerialName("is_admin") val isAdmin: Boolean = false,
)

/**
 * `GET /api/auth/me`: who is signed in, and the home's name for people (an admin renames it
 * in the web Settings; a home that does not say keeps the name the app has).
 */
@Serializable
data class Me(
    val id: String,
    val username: String,
    @SerialName("is_admin") val isAdmin: Boolean = false,
    @SerialName("home_name") private val rawHomeName: String? = null,
) {
    val user: User get() = User(id, username, isAdmin)
    val homeName: String? get() = rawHomeName?.trim()?.takeIf { it.isNotEmpty() }
}

/**
 * `POST /api/auth/login` and `/api/auth/refresh` for a native client (`X-Maktaba-Client:
 * native`): the tokens in the body, not cookies.
 */
@Serializable
data class AuthTokens(
    @SerialName("access_token") val accessToken: String,
    @SerialName("refresh_token") val refreshToken: String,
    val user: User? = null,
)

/** `GET /api/libraries` (an admin's also carries the roots). */
@Serializable
data class Library(
    val id: String,
    val name: String,
    val roots: List<String> = emptyList(),
)

/** A video in `GET /api/videos` and `GET /api/videos/{id}`. */
@Serializable
data class Video(
    val id: String,
    @SerialName("library_id") val libraryId: String = "",
    val filename: String = "",
    val title: String? = null,
    val description: String? = null,
    @SerialName("duration_sec") val durationSec: Double? = null,
) {
    /** What a person reads: the title, else the file's name. */
    val displayTitle: String get() = title?.takeIf { it.isNotEmpty() } ?: filename
}

/** A page of a list (`{"items": [...], "next": cursor}`). */
@Serializable
data class Page<T>(val items: List<T> = emptyList(), val next: String? = null)

/**
 * `POST /api/stream/sessions`: where the video plays from. `direct_url` is the file itself;
 * `manifest_url` an HLS playlist. Both are signed (`?sig=`, good until `expires_at`) and
 * relative to the home. The home's web port also wants a signed-in caller on `/stream/`
 * (`ApiClient.playbackToken`, `StreamAuthInterceptor`).
 */
@Serializable
data class StreamSession(
    @SerialName("session_id") val sessionId: String,
    val mode: String = "",
    @SerialName("manifest_url") val manifestUrl: String? = null,
    @SerialName("direct_url") val directUrl: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
    /** The qualities a transcode can be capped to (empty: none offered, as for direct play). */
    val ladder: List<Rendition> = emptyList(),
    /** The quality this session plays at, when the home says. */
    @SerialName("current_rendition") val currentRendition: Rendition? = null,
) {
    /** When the signed URL stops working, in epoch milliseconds (null: the home did not say). */
    val expiresMillis: Long?
        get() = expiresAt?.let { runCatching { OffsetDateTime.parse(it).toInstant().toEpochMilli() }.getOrNull() }

    /**
     * The URL the player opens, against the home's address. A home served under a base path
     * (`https://example.com/maktaba`) that answers with a root path (`/stream/…`) is read
     * under its base path.
     */
    fun playbackUrl(home: HttpUrl): HttpUrl? = (directUrl ?: manifestUrl)?.let {
        val base = home.encodedPath.trimEnd('/')
        val path = if (base.isNotEmpty() && it.startsWith("/") && !it.startsWith("//") && !it.startsWith("$base/")) base + it else it
        home.resolve(path)
    }

    val isHls: Boolean get() = directUrl == null && manifestUrl != null
}

/** A rung of a stream session's `ladder`: a quality the home can transcode to. */
@Serializable
data class Rendition(
    val height: Int = 0,
    val width: Int = 0,
    @SerialName("bitrate_kbps") val bitrateKbps: Int = 0,
    val codec: String = "",
)

/** A group of failed jobs in "Needs attention" (`GET /api/issues`). */
@Serializable
data class FailureGroup(
    val stage: String,
    val cause: String,
    val label: String,
    val count: Int,
    /** "retry", "apply", "open_settings" or "show_workers". */
    val action: String,
    @SerialName("sample_message") val sampleMessage: String? = null,
    @SerialName("first_at") val firstAt: String? = null,
    @SerialName("last_at") val lastAt: String? = null,
) {
    val id: String get() = "$stage:$cause"
    val retryable: Boolean get() = action == "retry"
}

@Serializable
data class FailureGroups(val groups: List<FailureGroup> = emptyList())

/** `GET /api/queues`: the processing stages, their counts, and whether all is paused. */
@Serializable
data class QueueSnapshot(
    @SerialName("paused_all") val pausedAll: Boolean = false,
    val stages: List<Stage> = emptyList(),
    val queues: List<QueueSummary> = emptyList(),
    @SerialName("issues_count") val issuesCount: Int = 0,
    /** the health strip: the NAS, the CPU, the backlog */
    val health: HealthNow? = null,
) {
    /** The stages a person sees (not hidden), each with its counts. */
    val rows: List<StageRow>
        get() {
            val byStage = queues.associateBy { it.stage }
            return stages.filterNot { it.hidden }.map { StageRow(it, byStage[it.stage]) }
        }
}

@Serializable
data class Stage(
    val stage: String,
    val label: String = "",
    val lane: String = "",
    val enabled: Boolean = true,
    val hidden: Boolean = false,
    val paused: Boolean = false,
    @SerialName("disabled_reason") val disabledReason: String? = null,
)

@Serializable
data class QueueSummary(
    val stage: String,
    val counts: Counts = Counts(),
    @SerialName("rate_per_min") val ratePerMin: Double = 0.0,
    @SerialName("eta_sec") val etaSec: Double? = null,
    @SerialName("oldest_waiting_sec") val oldestWaitingSec: Double? = null,
    @SerialName("served_by") val servedBy: List<String> = emptyList(),
    @SerialName("no_worker") val noWorker: Boolean = false,
    /**
     * Why nothing runs: paused, paused_all, pool_full, debouncing, backoff, service_retrying,
     * upstream, disabled, not_configured, awaiting_review, no_worker, idle (null: it runs).
     */
    @SerialName("waiting_reason") val waitingReason: String? = null,
    /** the outside service ("plex", "dizquetv") whose failures put waiting jobs into backoff */
    @SerialName("retrying_service") val retryingService: String? = null,
    val health: StageHealth? = null,
)

@Serializable
data class Counts(
    val pending: Int = 0,
    val ready: Int = 0,
    val running: Int = 0,
    val paused: Int = 0,
    @SerialName("failed_open") val failedOpen: Int = 0,
    @SerialName("done_24h") val done24h: Int = 0,
)

data class StageRow(val stage: Stage, val summary: QueueSummary?) {
    val id: String get() = stage.stage
    val label: String get() = stage.label.ifEmpty { stage.stage }
    val counts: Counts get() = summary?.counts ?: Counts()
}

/** `GET /api/cluster`: this home and the machines that work for it. */
@Serializable
data class ClusterOverview(
    val home: ClusterHome = ClusterHome(),
    val nodes: List<Machine> = emptyList(),
)

@Serializable
data class ClusterHome(
    val name: String = "",
    val version: String = "",
    @SerialName("web_url") val webUrl: String? = null,
)

@Serializable
data class Machine(
    val id: String,
    val name: String = "",
    /** "pending", "approved", "rejected" or "revoked". */
    val state: String = "",
    val online: Boolean = false,
    val drain: Boolean = false,
    val os: String? = null,
    val arch: String? = null,
    @SerialName("last_seen_at") val lastSeenAt: String? = null,
    val version: String? = null,
)
