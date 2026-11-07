package com.hamzalabs.maktaba.core.api

import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import java.time.Instant

/**
 * Maktaba Cloud's status (Epic 50 story 50.11): `https://status.maktaba.tv/summary.json`, read
 * only when the app reaches its home through the cloud (the relay's address). Read at start and
 * every 5 minutes while in front; it never blocks: a failed read shows nothing. The status strip
 * (53.06) draws its line after the home's own.
 */
@Serializable
data class CloudSummary(
    val components: Map<String, String> = emptyMap(),
    val incident: Incident? = null,
    val maintenance: List<Maintenance> = emptyList(),
) {
    @Serializable
    data class Title(val en: String = "", val ar: String = "")

    @Serializable
    data class Incident(
        val id: String,
        val state: String = "",
        val components: List<String>? = null,
        val title: Title = Title(),
        val url: String = "",
    )

    @Serializable
    data class Maintenance(
        val id: String? = null,
        @SerialName("starts_at") val startsAt: String,
        @SerialName("ends_at") val endsAt: String,
        val components: List<String> = emptyList(),
        val title: Title = Title(),
        val url: String? = null,
    )
}

object CloudStatusRules {
    const val SITE = "https://status.maktaba.tv"
    /** The relay's public names: a home at `<slug>.relay.maktaba.tv` is reached through it. */
    const val RELAY_DOMAIN = "relay.maktaba.tv"
    const val POLL_MS = 5 * 60_000L
    private const val AHEAD_MS = 24 * 3600_000L

    /** The app reaches the home at [home] through Maktaba Cloud. */
    fun throughCloud(home: HttpUrl?): Boolean {
        val host = home?.host?.lowercase() ?: return false
        return host == RELAY_DOMAIN || host.endsWith(".$RELAY_DOMAIN")
    }

    /** The feed's address, with the minute in it: GitHub Pages lets caches keep a copy for 10 minutes. */
    fun summaryUrl(nowMs: Long): String = "$SITE/summary.json?t=${nowMs / 60_000}"

    /**
     * The line, or null: the relay or the API down or slow, or maintenance on either from 24
     * hours before it starts until it ends; worst first. Notifications alone never show. [fmt]
     * gives a moment's date and time in the person's language.
     */
    fun line(s: CloudSummary?, nowMs: Long, fmt: (Long) -> Pair<String, String>): StripLine? {
        if (s == null) return null
        fun incident(comp: String) = s.incident?.takeIf { it.components?.contains(comp) ?: true }
        for (state in listOf("outage", "degraded")) {
            for (comp in listOf("relay", "api")) {
                if (s.components[comp] != state) continue
                val key = when (comp to state) {
                    "relay" to "outage" -> StripKey.RELAY_OUTAGE
                    "relay" to "degraded" -> StripKey.RELAY_DEGRADED
                    "api" to "outage" -> StripKey.API_OUTAGE
                    else -> StripKey.API_DEGRADED
                }
                val inc = incident(comp)
                return StripLine(key, id = "cloud:${inc?.id ?: "$comp-$state"}", link = inc?.url?.takeIf { it.isNotEmpty() } ?: SITE)
            }
        }
        val next = s.maintenance
            .filter { m -> m.components.any { it == "relay" || it == "api" } }
            .mapNotNull { m ->
                val a = runCatching { Instant.parse(m.startsAt).toEpochMilli() }.getOrNull()
                val b = runCatching { Instant.parse(m.endsAt).toEpochMilli() }.getOrNull()
                if (a == null || b == null) null else Triple(m, a, b)
            }
            .filter { (_, a, b) -> a - AHEAD_MS <= nowMs && nowMs < b }
            .minByOrNull { it.second } ?: return null
        val (m, a, b) = next
        val start = fmt(a)
        return StripLine(
            StripKey.MAINTENANCE,
            id = "cloud:${m.id ?: m.startsAt}",
            args = listOf(start.first, start.second, fmt(b).second),
            link = m.url ?: SITE,
        )
    }

    private val json = Json { ignoreUnknownKeys = true }

    /** Reads the feed; null when it does not answer (no line: the strip shows nothing new). */
    suspend fun fetch(http: OkHttpClient, nowMs: Long = System.currentTimeMillis(), url: String = summaryUrl(nowMs)): CloudSummary? = try {
        val target = url.toHttpUrlOrNull() ?: return null
        val (code, body) = http.exchange(Request.Builder().url(target).header("Cache-Control", "no-cache").build())
        if (code == 200) json.decodeFromString(CloudSummary.serializer(), String(body)) else null
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        null
    }
}
