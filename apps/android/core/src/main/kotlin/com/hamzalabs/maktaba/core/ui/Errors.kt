package com.hamzalabs.maktaba.core.ui

import androidx.annotation.StringRes
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Block
import androidx.compose.material.icons.outlined.CloudOff
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material.icons.outlined.HourglassTop
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.Schedule
import androidx.compose.material.icons.outlined.SearchOff
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextDirection
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.Failure

/** The one sentence a person reads for [this] kind ([video]: about a video, else anything). */
@StringRes
fun ErrorKind.sentence(video: Boolean = false): Int = when (this) {
    ErrorKind.NotFound -> if (video) R.string.error_not_found_video else R.string.error_not_found
    ErrorKind.NotReady -> R.string.error_not_ready
    ErrorKind.SignedOut -> R.string.error_signed_out
    ErrorKind.NoAccess -> R.string.error_no_access
    ErrorKind.Unreachable -> R.string.error_unreachable
    ErrorKind.Busy -> R.string.error_busy
    ErrorKind.Server -> R.string.error_server
}

/** The picture beside the sentence. */
val ErrorKind.icon: ImageVector
    get() = when (this) {
        ErrorKind.NotFound -> Icons.Outlined.SearchOff
        ErrorKind.NotReady -> Icons.Outlined.HourglassTop
        ErrorKind.SignedOut -> Icons.Outlined.Lock
        ErrorKind.NoAccess -> Icons.Outlined.Block
        ErrorKind.Unreachable -> Icons.Outlined.CloudOff
        ErrorKind.Busy -> Icons.Outlined.Schedule
        ErrorKind.Server -> Icons.Outlined.ErrorOutline
    }

/** What a person can do about a failure, the first being what most helps (a TV focuses it). */
enum class ErrorAction { TryAgain, SignIn, OtherHome, Back }

/**
 * The actions for [kind], of those the screen offers: Sign in again after a 401, Try again
 * when a moment later may work, Choose another home when this one does not answer, and Back.
 */
fun errorActions(
    kind: ErrorKind,
    canRetry: Boolean,
    canSignIn: Boolean,
    canChooseHome: Boolean,
    canGoBack: Boolean,
): List<ErrorAction> = buildList {
    val retryHelps = kind != ErrorKind.NotFound && kind != ErrorKind.NoAccess && kind != ErrorKind.SignedOut
    if (kind == ErrorKind.SignedOut && canSignIn) add(ErrorAction.SignIn)
    if (retryHelps && canRetry) add(ErrorAction.TryAgain)
    if (kind == ErrorKind.Unreachable && canChooseHome) add(ErrorAction.OtherHome)
    if (canGoBack) add(ErrorAction.Back)
    // nothing else to offer: a list can at least ask again
    if (isEmpty() && canRetry) add(ErrorAction.TryAgain)
}

@StringRes
fun ErrorAction.label(): Int = when (this) {
    ErrorAction.TryAgain -> R.string.try_again
    ErrorAction.SignIn -> R.string.error_sign_in_again
    ErrorAction.OtherHome -> R.string.other_home
    ErrorAction.Back -> R.string.error_back
}

/**
 * A failure in words a person reads: an icon, [title], the kind's one sentence, the actions that
 * fit it (the first filled) and, folded away, the home's own words (Details). Mirrors for a
 * right-to-left language like the rest of the layout.
 *
 * [video]: the failure is about a video (its "not found" sentence says so).
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun ErrorState(
    failure: Failure,
    title: String,
    modifier: Modifier = Modifier,
    video: Boolean = false,
    onRetry: (() -> Unit)? = null,
    onSignIn: (() -> Unit)? = null,
    onChooseHome: (() -> Unit)? = null,
    onBack: (() -> Unit)? = null,
) {
    val actions = errorActions(failure.kind, onRetry != null, onSignIn != null, onChooseHome != null, onBack != null)
    var showDetails by rememberSaveable(failure) { mutableStateOf(false) }
    val muted = LocalContentColor.current.copy(alpha = 0.75f)
    Column(
        modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp).testTag("error-state"),
        verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Icon(failure.kind.icon, contentDescription = null, modifier = Modifier.size(48.dp), tint = muted)
        Text(title, style = MaterialTheme.typography.titleLarge, textAlign = TextAlign.Center, modifier = Modifier.testTag("error-title"))
        Text(
            stringResource(failure.kind.sentence(video)),
            style = MaterialTheme.typography.bodyLarge,
            color = muted,
            textAlign = TextAlign.Center,
            modifier = Modifier.widthIn(max = 480.dp).testTag("error-sentence"),
        )
        FlowRow(
            Modifier.padding(top = 8.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.CenterHorizontally),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            actions.forEachIndexed { i, a ->
                val run: () -> Unit = when (a) {
                    ErrorAction.TryAgain -> onRetry
                    ErrorAction.SignIn -> onSignIn
                    ErrorAction.OtherHome -> onChooseHome
                    ErrorAction.Back -> onBack
                } ?: {}
                val tag = Modifier.testTag("error-action-${a.name}")
                if (i == 0) Button(onClick = run, modifier = tag) { Text(stringResource(a.label())) }
                else OutlinedButton(onClick = run, modifier = tag) { Text(stringResource(a.label())) }
            }
        }
        val details = failure.details
        if (!details.isNullOrBlank()) {
            TextButton(onClick = { showDetails = !showDetails }, modifier = Modifier.testTag("error-details-toggle")) {
                Text(stringResource(R.string.error_details), color = muted)
            }
            if (showDetails) {
                SelectionContainer {
                    Text(
                        details,
                        // the home's words are English and paths: left to right in any language
                        style = MaterialTheme.typography.bodySmall.copy(textDirection = TextDirection.Ltr),
                        fontFamily = FontFamily.Monospace,
                        color = muted,
                        modifier = Modifier.widthIn(max = 560.dp).testTag("error-details"),
                    )
                }
            }
        }
    }
}
