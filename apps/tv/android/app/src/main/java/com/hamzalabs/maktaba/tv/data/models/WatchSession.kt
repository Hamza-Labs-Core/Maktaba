package com.hamzalabs.maktaba.tv.data.models

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** `POST /api/watch/start` body (Story 29.1). */
@Serializable
data class WatchStartRequest(
    @SerialName("video_id") val videoId: String,
    @SerialName("device_type") val deviceType: String,
    val platform: String,
    val quality: String,
)

/** `POST /api/watch/start` response. [sessionId] is null when the user
 * has paused tracking (Story 29.4) — the caller then sends no beats. */
@Serializable
data class WatchStartResponse(
    @SerialName("session_id") val sessionId: String? = null,
    val tracking: Boolean = false,
)

/** `POST /api/watch/heartbeat` body. */
@Serializable
data class WatchHeartbeatRequest(
    @SerialName("session_id") val sessionId: String,
    @SerialName("position_sec") val positionSec: Long,
)

/** `POST /api/watch/stop` body. */
@Serializable
data class WatchStopRequest(
    @SerialName("session_id") val sessionId: String,
    @SerialName("position_sec") val positionSec: Long,
)

/** Response of heartbeat/stop; the player ignores the body. */
@Serializable
data class WatchSessionView(
    @SerialName("session_id") val sessionId: String = "",
    val state: String = "",
)
