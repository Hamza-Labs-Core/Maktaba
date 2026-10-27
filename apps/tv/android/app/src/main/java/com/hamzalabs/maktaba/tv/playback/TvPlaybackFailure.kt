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
            return Failure(errorKind(c.responseCode, Problem.parse(body)), details(c.responseCode, body), retryAfterSeconds(retryAfter))
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
