package com.hamzalabs.maktaba.remote

import com.hamzalabs.maktaba.core.api.FailureGroup

// What a "Needs attention" group says. Its cause is a grouping key, not words: a cause the engine
// could not name is "kind:normalised message", the message's numbers made <n> and its paths <path>
// (pipeline engine/causes.py). The title is the cause's words, by its code or its kind, in this
// language; the home's label (api queues/causes.go) only for a code this app has no words for.

/** The cause codes' words (and the kinds' the stages raise that are no code of their own). */
internal val causeWords: Map<String, Int> = mapOf(
    "nas_timeout" to R.string.cause_nas_timeout,
    "nas_invalid_argument" to R.string.cause_nas_invalid_argument,
    "nas_partial_read" to R.string.cause_nas_partial_read,
    "file_missing" to R.string.cause_file_missing,
    "ffmpeg_decode" to R.string.cause_ffmpeg_decode,
    "youtube_bot_check" to R.string.cause_youtube_bot_check,
    "youtube_rate_limited" to R.string.cause_youtube_rate_limited,
    "youtube_unavailable" to R.string.cause_youtube_unavailable,
    "plex_unreachable" to R.string.cause_plex_unreachable,
    "plex_auth" to R.string.cause_plex_auth,
    "dizquetv_unreachable" to R.string.cause_dizquetv_unreachable,
    "readback_mismatch" to R.string.cause_readback_mismatch,
    "no_handler" to R.string.cause_no_handler,
    "graph_violation" to R.string.cause_graph_violation,
    "db_error" to R.string.cause_db_error,
    "shm_full" to R.string.cause_shm_full,
    "disk_full" to R.string.cause_disk_full,
    "permission_denied" to R.string.cause_permission_denied,
    "ffmpeg_rc" to R.string.cause_ffmpeg_rc,
    "awaiting_review" to R.string.cause_awaiting_review,
    "stale_graph" to R.string.cause_stale_graph,
    "model_unavailable" to R.string.cause_model_unavailable,
    "chapter_frames_failed" to R.string.cause_ffmpeg_rc,
    "thumbnail_unreadable" to R.string.cause_thumbnail_unreadable,
    "video_unhashed" to R.string.cause_video_unhashed,
    "unknown" to R.string.cause_unknown,
)

/** What fixes a group Retry does not, by its action. */
private val cannotRetryWords: Map<String, Int> = mapOf(
    "open_settings" to R.string.cannot_retry_open_settings,
    "apply" to R.string.cannot_retry_apply,
    "show_workers" to R.string.cannot_retry_show_workers,
)

/** A generated cause's kind ("nas_timeout:<path>: no answer" is nas_timeout's), else the cause. */
internal fun causeKind(cause: String): String = cause.substringBefore(':').ifEmpty { cause }

/** True when [s] is a grouping key, never words to show: a code's "kind:..." or a placeholder. */
internal fun looksLikeKey(s: String): Boolean =
    Regex("^[a-z][a-z0-9_]*:").containsMatchIn(s) || Regex("<(n|path|id|hex)>").containsMatchIn(s)

/** A group's title: its cause's words, else its kind's, else the home's label, else the kind in words. */
internal fun Say.groupTitle(g: FailureGroup): String {
    causeWords[g.cause]?.let { return t(it) }
    causeWords[causeKind(g.cause)]?.let { return t(it) }
    if (g.label.isNotBlank() && !looksLikeKey(g.label)) return g.label
    return Say.spaced(causeKind(g.cause)).replaceFirstChar { it.uppercase() }
}

/** Why a group has no Retry, and what does fix it; null when Retry does. */
internal fun Say.cannotRetry(g: FailureGroup): String? {
    if (g.retryable) return null
    val what = cannotRetryWords[g.action]?.let { t(it) } ?: Say.spaced(g.action)
    return t(R.string.cannot_retry, what)
}

/**
 * A job's message as it should read: a repr's escapes as real line breaks (never a backslash-n),
 * blank lines dropped, and each part in its own direction (an Arabic file name in an English
 * line keeps its slashes and parts in order: [isolateRtl]).
 */
internal fun readableMessage(message: String): String =
    message
        .replace("\\r\\n", "\n").replace("\\n", "\n").replace("\\r", "\n").replace("\\t", " ")
        .lines().map { it.trimEnd() }.filter { it.isNotBlank() }
        .joinToString("\n") { isolateRtl(it) }
        .trim()

private const val FSI = '⁨'
private const val PDI = '⁩'

private fun isRtl(c: Char): Boolean = Character.getDirectionality(c).let {
    it == Character.DIRECTIONALITY_RIGHT_TO_LEFT || it == Character.DIRECTIONALITY_RIGHT_TO_LEFT_ARABIC
}

/**
 * Each part of [line] between ": " separators that has right-to-left letters, set apart in its
 * own direction (Unicode's first-strong isolate): "<file>: no answer" with an Arabic file name
 * reads as the name, then ": no answer", in an English line or an Arabic one.
 */
internal fun isolateRtl(line: String): String {
    if (line.none(::isRtl)) return line
    return line.split(": ").joinToString(": ") { part -> if (part.any(::isRtl)) "$FSI$part$PDI" else part }
}
