package com.hamzalabs.maktaba.core.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.CheckCircle
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material.icons.outlined.HourglassTop
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.api.PairingCode
import com.hamzalabs.maktaba.core.session.ConnectProgress
import com.hamzalabs.maktaba.core.session.FailReason
import kotlinx.coroutines.delay
import java.util.Locale

/**
 * What connecting is doing now (story 48.37), as every connect, sign-in, restore and retry
 * screen shows it: never a bare spinner. A wait says its step (finding the home, the address
 * tried and which of how many, signing in, waiting for approval, reconnecting, the home busy)
 * with its countdown when it has one; a failure says why in plain words, the addresses tried,
 * and Retry ([onRetry], when given); every try after the first says its attempt number.
 *
 * [now] is the clock the countdowns read (`AppSession.now`).
 */
@Composable
fun ConnectStatus(
    progress: ConnectProgress,
    now: () -> Long,
    modifier: Modifier = Modifier,
    onRetry: (() -> Unit)? = null,
) {
    val colors = MaterialTheme.colorScheme
    val failed = progress as? ConnectProgress.Failed
    val counting = when (progress) {
        is ConnectProgress.AwaitingApproval -> progress.expiresAt
        is ConnectProgress.HomeBusy -> progress.retryAt
        else -> null
    }
    val left = counting?.let { countdown(it, now) }
    // the Arabic comma between addresses in Arabic
    val separator = if (LocalLayoutDirection.current == LayoutDirection.Rtl) "، " else ", "
    Column(
        modifier.fillMaxWidth().testTag("connect-status").semantics { liveRegion = LiveRegionMode.Polite },
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Row(verticalAlignment = Alignment.Top, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Box(Modifier.size(20.dp), contentAlignment = Alignment.Center) {
                when (progress) {
                    is ConnectProgress.Failed -> Icon(Icons.Outlined.ErrorOutline, null, tint = colors.error)
                    is ConnectProgress.Found -> Icon(Icons.Outlined.CheckCircle, null, tint = colors.primary)
                    is ConnectProgress.HomeBusy -> Icon(Icons.Outlined.HourglassTop, null, tint = colors.primary)
                    else -> CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                }
            }
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(
                    headline(progress, left),
                    style = MaterialTheme.typography.bodyMedium,
                    color = if (failed != null) colors.error else colors.onSurface,
                    modifier = Modifier.testTag("connect-status-text"),
                )
                if (progress is ConnectProgress.AwaitingApproval && left != null) {
                    Text(
                        stringResource(R.string.progress_code_expires, ltr(PairingCode.shown(progress.code)), ltr(left)),
                        style = MaterialTheme.typography.bodySmall,
                        color = colors.onSurfaceVariant,
                    )
                }
                if (failed != null && failed.tried.isNotEmpty()) {
                    Text(
                        stringResource(R.string.progress_tried, failed.tried.joinToString(separator) { ltr(it) }),
                        style = MaterialTheme.typography.bodySmall,
                        color = colors.onSurfaceVariant,
                    )
                }
                if (progress.attempt > 1) {
                    Text(
                        stringResource(R.string.progress_attempt, progress.attempt),
                        style = MaterialTheme.typography.labelSmall,
                        color = colors.onSurfaceVariant,
                        modifier = Modifier.testTag("connect-status-attempt"),
                    )
                }
            }
        }
        if (failed != null && onRetry != null) {
            OutlinedButton(onClick = onRetry, modifier = Modifier.fillMaxWidth().testTag("connect-retry")) {
                Text(stringResource(R.string.progress_retry))
            }
        }
    }
}

/**
 * A labelled wait that is not a connect step (the homes on this network, the homes in Maktaba
 * Cloud): its words beside the spinner, never a spinner alone.
 */
@Composable
fun StatusLine(text: String, modifier: Modifier = Modifier, busy: Boolean = true) {
    Row(modifier, verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        if (busy) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
        Text(text, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun headline(p: ConnectProgress, left: String?): String = when (p) {
    is ConnectProgress.Finding -> stringResource(R.string.progress_finding)
    is ConnectProgress.Trying -> stringResource(R.string.progress_trying, ltr(p.address), p.index, p.count)
    is ConnectProgress.Found -> stringResource(R.string.progress_found, p.name)
    is ConnectProgress.SigningIn -> stringResource(R.string.progress_signing_in, p.name)
    is ConnectProgress.AwaitingApproval -> stringResource(R.string.progress_awaiting)
    is ConnectProgress.Reconnecting -> stringResource(R.string.progress_reconnecting, p.name)
    is ConnectProgress.HomeBusy -> stringResource(R.string.progress_busy, p.name, ltr(left ?: ""))
    is ConnectProgress.Failed -> stringResource(
        when (p.reason) {
            FailReason.NoAnswer -> R.string.progress_fail_no_answer
            FailReason.NotAHome -> R.string.progress_fail_not_home
            FailReason.OtherHome -> R.string.progress_fail_other_home
            FailReason.WrongPassword -> R.string.progress_fail_wrong_password
            FailReason.ApprovalExpired -> R.string.progress_fail_expired
            FailReason.Busy -> R.string.progress_fail_busy
            FailReason.Other -> R.string.progress_fail_other
        },
    )
}

/** The time left until [until] as m:ss, ticking each second on [now]. */
@Composable
private fun countdown(until: Long, now: () -> Long): String {
    var t by remember(until) { mutableLongStateOf(now()) }
    LaunchedEffect(until) {
        while (t < until) {
            delay(1_000 - (until - t) % 1_000)
            t = now()
        }
    }
    return clock(until - t)
}

/** [ms] as m:ss (never below 0:00), in the digits an address and a code read in. */
internal fun clock(ms: Long): String {
    val s = (ms.coerceAtLeast(0) + 999) / 1000
    return String.format(Locale.ROOT, "%d:%02d", s / 60, s % 60)
}

/** [s] kept left to right inside a sentence in either direction (an address, a code, a time). */
private fun ltr(s: String) = "$LRI$s$PDI"

// Unicode's left-to-right isolate and its end, written as code points (lint: BidiSpoofing)
private val LRI = Char(0x2066)
private val PDI = Char(0x2069)
