package com.hamzalabs.maktaba.core.api

import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import java.io.IOException

/**
 * Why something failed, in the words a person reads (each kind has one sentence in
 * the core's `strings.xml`, see `ui/Errors.kt`). The home's own text (its problem JSON) is
 * never what a person reads: it goes to the log and the error screen's Details.
 */
enum class ErrorKind {
    /** The video (or its file) is not on the home: 404 or 410. */
    NotFound,
    /** The home is still preparing the video: a stream's playlist that is not written yet. */
    NotReady,
    /** The sign-in ended: 401, or the refresh token was refused. */
    SignedOut,
    /** This account may not: 403. */
    NoAccess,
    /** No answer from the home: the network, or a machine that cannot reach its home. */
    Unreachable,
    /** The home answered "not now": 429, or 503 (with Retry-After when it said when). */
    Busy,
    /** Anything else the home answered (a 5xx, an answer this app cannot read). */
    Server,
}

/**
 * An RFC 7807 problem as the home sends it: `{"type", "title", "status", "detail"}` and, from a
 * node, a machine-readable `code` (`home_unreachable`, `paused`, ...).
 */
data class Problem(
    val type: String = "",
    val title: String = "",
    val status: Int = 0,
    val detail: String = "",
    val code: String = "",
) {
    /** The last part of [type] (`rendition-not-found`): the home's domain differs by install. */
    val slug: String get() = type.substringAfter("/problems/", "").trimEnd('/')

    companion object {
        /** The problem in [body], or null when it is not one (an HTML page, plain text, empty). */
        fun parse(body: String): Problem? {
            val text = body.trim()
            if (!text.startsWith("{")) return null
            val o = runCatching { ApiClient.json.parseToJsonElement(text) as? JsonObject }.getOrNull() ?: return null
            fun s(k: String) = (o[k] as? JsonPrimitive)?.contentOrNull.orEmpty()
            val p = Problem(s("type"), s("title"), s("status").toDoubleOrNull()?.toInt() ?: 0, s("detail"), s("code"))
            return if (p.type.isEmpty() && p.title.isEmpty() && p.code.isEmpty()) null else p
        }
    }
}

/** The problem slugs a stream answers while a fresh transcode has not written its playlist yet. */
private val notReadySlugs = setOf("manifest-not-found", "rendition-not-found")

/** What an HTTP [status] with [problem] (null: not problem JSON) means to a person. */
fun errorKind(status: Int, problem: Problem?): ErrorKind {
    val slug = problem?.slug.orEmpty()
    val code = problem?.code.orEmpty()
    return when {
        code == "home_unreachable" || code == "home_timeout" -> ErrorKind.Unreachable
        slug == "home-unreachable" || slug == "home-timeout" || slug == "video-unavailable-offline" -> ErrorKind.Unreachable
        status == 404 && slug in notReadySlugs -> ErrorKind.NotReady
        status == 404 || status == 410 -> ErrorKind.NotFound
        status == 401 -> ErrorKind.SignedOut
        status == 403 -> ErrorKind.NoAccess
        status == 429 || status == 503 -> ErrorKind.Busy
        else -> ErrorKind.Server
    }
}

/**
 * What [this] failure means to a person. An [IOException] (the player's stream requests) is the
 * network, unless it carries the client's own [ApiException].
 */
fun Throwable.errorKind(): ErrorKind {
    var c: Throwable? = this
    while (c != null) {
        when (c) {
            is ApiException.SignedOut -> return ErrorKind.SignedOut
            is ApiException.Transport -> return ErrorKind.Unreachable
            is ApiException.NotReady -> return ErrorKind.NotReady
            is ApiException.Status -> return errorKind(c.code, c.problem)
            is ApiException.Decoding -> return ErrorKind.Server
            else -> {}
        }
        if (c.cause === c) break
        c = c.cause
    }
    return if (this is IOException) ErrorKind.Unreachable else ErrorKind.Server
}

/**
 * A failure as a screen keeps it: its [kind] (what a person reads) and the home's own words for
 * the Details disclosure and the log ([details]: never the main message).
 */
data class Failure(val kind: ErrorKind, val details: String? = null, val retryAfterSec: Long? = null) {
    companion object {
        fun of(e: Throwable): Failure {
            val status = e.findCause<ApiException.Status>()
            return Failure(e.errorKind(), e.errorDetails(), status?.retryAfterSec)
        }
    }
}

/**
 * The home's own words for [this] failure: the status, the problem's slug, title and detail (or
 * the start of a body that is not problem JSON). For the Details disclosure and the log only.
 */
fun Throwable.errorDetails(): String? {
    val s = findCause<ApiException.Status>()
    if (s != null) return details(s.code, s.body)
    val nr = findCause<ApiException.NotReady>()
    if (nr != null) return details(nr.code, nr.body)
    return (findCause<ApiException>() ?: this).message?.takeIf { it.isNotBlank() }
}

/** [status] and [body] as lines a person can read out to whoever runs the home. */
fun details(status: Int, body: String): String {
    val p = Problem.parse(body)
    return buildList {
        add(listOfNotNull("HTTP $status".takeIf { status > 0 }, p?.slug?.takeIf { it.isNotEmpty() }, p?.code?.takeIf { it.isNotEmpty() }).joinToString(" · "))
        if (p != null) {
            if (p.title.isNotBlank()) add(p.title)
            if (p.detail.isNotBlank()) add(p.detail)
        } else if (body.isNotBlank()) {
            add(body.trim().take(500))
        }
    }.filter { it.isNotBlank() }.joinToString("\n").ifBlank { "HTTP $status" }
}

/** `Retry-After` in seconds (only the delta form: a home sends that). */
fun retryAfterSeconds(header: String?): Long? = header?.trim()?.toLongOrNull()?.takeIf { it >= 0 }

private inline fun <reified T : Throwable> Throwable.findCause(): T? {
    var c: Throwable? = this
    while (c != null) {
        if (c is T) return c
        if (c.cause === c) return null
        c = c.cause
    }
    return null
}
