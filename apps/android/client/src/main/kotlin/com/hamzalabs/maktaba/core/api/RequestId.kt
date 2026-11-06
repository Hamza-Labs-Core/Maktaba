package com.hamzalabs.maktaba.core.api

import java.security.SecureRandom

/**
 * The request id every call to the home carries (Epic 53 story 53.08): `X-Request-Id`, a UUIDv7
 * the app makes; calls within a second of each other (one screen's loads) share one, so the
 * home's lines for them can be followed through the API, the pipeline and streaming. The last 20
 * are kept for a problem report.
 */
object RequestId {
    const val HEADER = "X-Request-Id"
    const val WINDOW_MS = 1_000L
    private val random = SecureRandom()
    private var current: Pair<String, Long>? = null
    private val kept = ArrayDeque<String>()

    /** The current action's id: the one of a call within the last second, else a new one. */
    @Synchronized
    fun next(now: Long = System.currentTimeMillis()): String {
        current?.let { (id, at) ->
            if (now - at <= WINDOW_MS) {
                current = id to now
                return id
            }
        }
        val id = v7(now)
        current = id to now
        kept.addLast(id)
        while (kept.size > 20) kept.removeFirst()
        return id
    }

    /** The last actions' ids, oldest first. */
    @Synchronized
    fun recent(): List<String> = kept.toList()

    /** The short reference an error screen shows: the id's first 8 characters. */
    fun short(id: String): String = id.take(8)

    /** A UUIDv7: 48 bits of milliseconds, random bits, version 7, variant 10. */
    fun v7(now: Long = System.currentTimeMillis()): String {
        val b = ByteArray(16).also { random.nextBytes(it) }
        var ms = now
        for (i in 5 downTo 0) {
            b[i] = (ms and 0xff).toByte()
            ms = ms ushr 8
        }
        b[6] = ((b[6].toInt() and 0x0f) or 0x70).toByte()
        b[8] = ((b[8].toInt() and 0x3f) or 0x80).toByte()
        val h = b.joinToString("") { "%02x".format(it) }
        return "${h.substring(0, 8)}-${h.substring(8, 12)}-${h.substring(12, 16)}-${h.substring(16, 20)}-${h.substring(20)}"
    }
}
