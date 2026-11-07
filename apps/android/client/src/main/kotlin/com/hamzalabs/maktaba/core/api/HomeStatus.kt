package com.hamzalabs.maktaba.core.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * The home's status (Epic 53 story 53.06): `/status.json`, answered by the supervisor even when
 * the API or the database is down (docs/api/status.md). The apps read it at start, every 10 s
 * while in front, and at once when a request fails with a server error; a failed read changes
 * nothing (an older home has no `/status.json`: no strip).
 */
@Serializable
data class HomeStatusDoc(
    val v: Int = 0,
    val mode: String = "normal",
    val overall: String = "ok",
    val components: Map<String, String> = emptyMap(),
    @SerialName("features_down") val featuresDown: List<String> = emptyList(),
)

/** What a strip line is about: each has its words in the core's strings (status_*). */
enum class StripKey {
    OFFLINE, RESTARTING, DATABASE, SAFE, STREAMING, STREAMING_NO_TV, SEARCH, PROCESSING, LOGS,
    // Maktaba Cloud's lines (Epic 50 story 50.11)
    RELAY_DEGRADED, RELAY_OUTAGE, API_DEGRADED, API_OUTAGE, MAINTENANCE,
    // a request waiting for the home behind the relay (story 50.13)
    RECONNECTING,
}

/**
 * One line of the strip; [details] is where an admin's Details goes (a path on the home). A
 * cloud line (story 50.11) has its [id] (the incident's, so a closed line comes back for the
 * next), its words' [args] (the maintenance window) and an outside Details [link] for everyone.
 */
data class StripLine(
    val key: StripKey,
    val details: String? = null,
    val id: String? = null,
    val args: List<String> = emptyList(),
    val link: String? = null,
)

/**
 * The strip's lines for a status, highest priority first (story 53.06 §1): offline; the home
 * restarting or its database down; safe mode; a part a viewer feels (playback, search);
 * admin-only parts (processing, log search). A feature that is off is no outage: an off group
 * has no line, and Live TV is left out of the playback sentence when it is off.
 */
object StatusStripRules {
    private val bad = setOf("degraded", "restarting", "down")

    /**
     * [cloud] is Maktaba Cloud's line (story 50.11, [CloudStatusRules.line]) when the app reaches
     * the home through the cloud: after the home's own lines, before the admin-only ones.
     */
    fun lines(
        s: HomeStatusDoc?,
        online: Boolean,
        admin: Boolean,
        liveTvOff: Boolean = false,
        cloud: StripLine? = null,
        reconnecting: Boolean = false,
    ): List<StripLine> {
        val out = mutableListOf<StripLine>()
        if (!online) out += StripLine(StripKey.OFFLINE) else if (reconnecting) out += StripLine(StripKey.RECONNECTING)
        if (s == null || s.v != 1) return out + listOfNotNull(cloud)
        fun bad(g: String) = (s.components[g] ?: "ok") in bad
        fun admin(link: String) = if (admin) link else null
        when {
            bad("database") -> out += StripLine(StripKey.DATABASE, admin("/recovery"))
            s.overall == "down" || bad("api") -> out += StripLine(StripKey.RESTARTING, admin("/recovery"))
        }
        if (s.mode == "safe") out += StripLine(StripKey.SAFE, admin("/recovery"))
        if (bad("streaming")) {
            out += StripLine(if (liveTvOff) StripKey.STREAMING_NO_TV else StripKey.STREAMING, admin("/admin/health"))
        }
        if (bad("search")) out += StripLine(StripKey.SEARCH, admin("/admin/health"))
        if (cloud != null) out += cloud
        if (admin && bad("processing")) out += StripLine(StripKey.PROCESSING, "/admin/health")
        if (admin && bad("logs")) out += StripLine(StripKey.LOGS, "/admin/health")
        return out
    }

    /** Whether playback is down on the home: the player says so instead of a generic error. */
    fun playbackDown(s: HomeStatusDoc?): Boolean = (s?.components?.get("streaming") ?: "ok") in bad
}
