package com.hamzalabs.maktaba.tv.playback

import androidx.annotation.OptIn
import androidx.media3.common.PlaybackException
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.HttpDataSource
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.BODY_KEPT
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.api.Problem
import com.hamzalabs.maktaba.core.api.details
import com.hamzalabs.maktaba.core.api.errorKind
import com.hamzalabs.maktaba.core.api.retryAfterSeconds
import com.hamzalabs.maktaba.core.api.timeRefusal
import com.hamzalabs.maktaba.core.api.timeUnknown

/**
 * What a player failure means to a person: the home's answer to a playlist or segment (its
 * status and problem JSON), the client's own failure (a refused refresh), or the network.
 * The same as the phone Player's `playbackFailure` (apps/android/player).
 */
@OptIn(UnstableApi::class)
fun tvPlaybackFailure(e: PlaybackException): Failure {
    var c: Throwable? = e
    while (c != null) {
        if (c is HttpDataSource.InvalidResponseCodeException) {
            val body = String(c.responseBody).take(BODY_KEPT)
            val retryAfter = c.headerFields.entries.firstOrNull { it.key.equals("Retry-After", ignoreCase = true) }?.value?.firstOrNull()
            val problem = Problem.parse(body)
            return Failure(errorKind(c.responseCode, problem), details(c.responseCode, body), retryAfterSeconds(retryAfter), timeUnknown(c.responseCode, problem))
        }
        if (c is ApiException) return Failure.of(c)
        if (c.cause === c) break
        c = c.cause
    }
    val kind = when (e.errorCode) {
        PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_FAILED,
        PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_TIMEOUT -> ErrorKind.Unreachable
        else -> ErrorKind.Server
    }
    return Failure(kind, listOfNotNull(e.errorCodeName, e.message).joinToString(": "))
}

/**
 * The time refusal in a player failure (Epic 57 story 57.14): the streaming service answered a
 * playlist or segment with 403 `parental-time`, or the client's own call was refused so.
 */
@OptIn(UnstableApi::class)
fun tvPlaybackTimeRefusal(e: PlaybackException): com.hamzalabs.maktaba.core.api.TimeRefusal? {
    var c: Throwable? = e
    while (c != null) {
        if (c is HttpDataSource.InvalidResponseCodeException) {
            return timeRefusal(c.responseCode, Problem.parse(String(c.responseBody).take(BODY_KEPT)))
        }
        if (c is ApiException) return c.timeRefusal()
        if (c.cause === c) break
        c = c.cause
    }
    return null
}
