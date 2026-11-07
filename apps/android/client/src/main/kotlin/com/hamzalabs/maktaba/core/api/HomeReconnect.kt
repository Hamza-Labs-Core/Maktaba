package com.hamzalabs.maktaba.core.api

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

/**
 * A home that is reconnecting behind the relay (Epic 50 story 50.13 §4): the relay holds a
 * request up to 15 s for a home it just saw, then answers `503 {"error": "home_reconnecting"}`.
 * That is a blip, not an outage: [ApiClient] tries again after 2, 4, 8 and then every 15 s, up
 * to 60 s, and the status strip says "Reconnecting to your home…" while any request waits.
 * `502 server_offline` (the home not connected at all) is not retried.
 */
object HomeReconnect {
    /** The waits between tries: 2, 4, 8, then 15 s, 60 s in all. */
    val WAITS_MS = longArrayOf(2_000, 4_000, 8_000, 15_000, 15_000, 15_000)

    private val _waiting = MutableStateFlow(0)

    /** How many requests wait for the home now (the strip shows a line while above 0). */
    val waiting: StateFlow<Int> = _waiting.asStateFlow()

    internal fun started() = _waiting.update { it + 1 }

    internal fun stopped() = _waiting.update { (it - 1).coerceAtLeast(0) }

    /** Whether [a] is the relay's home_reconnecting. */
    fun isReconnecting(a: HttpAnswer): Boolean {
        if (a.code != 503) return false
        val text = String(a.body)
        return Regex(""""(error|code)"\s*:\s*"home_reconnecting"""").containsMatchIn(text)
    }
}
