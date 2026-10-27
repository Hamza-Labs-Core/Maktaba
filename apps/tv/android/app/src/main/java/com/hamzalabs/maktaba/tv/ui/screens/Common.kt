package com.hamzalabs.maktaba.tv.ui.screens

import android.util.Log
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextAlign
import androidx.tv.material3.Icon
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.api.errorDetails
import com.hamzalabs.maktaba.core.ui.ErrorAction
import com.hamzalabs.maktaba.core.ui.errorActions
import com.hamzalabs.maktaba.core.ui.icon
import com.hamzalabs.maktaba.core.ui.label
import com.hamzalabs.maktaba.core.ui.sentence
import com.hamzalabs.maktaba.core.R as CoreR
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.ReadingPanel
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.CancellationException

/** The TV's overscan gutters: content stays clear of a TV's cropped edges. */
val Gutter = 48.dp

/** What a screen loads: loading, the value, or why it could not. */
sealed interface Load<out T> {
    data object Loading : Load<Nothing>
    data class Ready<T>(val value: T) : Load<T>
    data class Failed(val failure: Failure) : Load<Nothing>
}

/**
 * Loads with [load] whenever [key] changes (or a person presses Try again). A signed-out answer
 * sends the app back to signing in ([signedOut]).
 */
@Composable
fun <T> rememberLoad(key: Any?, signedOut: () -> Unit, load: suspend () -> T): Pair<Load<T>, () -> Unit> {
    var state by remember(key) { mutableStateOf<Load<T>>(Load.Loading) }
    var attempt by remember(key) { mutableIntStateOf(0) }
    LaunchedEffect(key, attempt) {
        state = Load.Loading
        state = try {
            Load.Ready(load())
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException.SignedOut) {
            signedOut()
            Load.Failed(Failure.of(e))
        } catch (e: Exception) {
            Log.w("MaktabaTV", "load: ${e.errorDetails()}", e)
            Load.Failed(Failure.of(e))
        }
    }
    return state to { attempt++ }
}

/** Loading, or a failure with Try again, centred on a reading panel. */
@Composable
fun <T> LoadState(state: Load<T>, retry: () -> Unit, content: @Composable (T) -> Unit) {
    when (state) {
        is Load.Ready -> content(state.value)
        Load.Loading -> Centered { Text(stringResource(R.string.loading), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text) }
        is Load.Failed -> TvErrorState(state.failure, stringResource(CoreR.string.error_title), onRetry = retry)
    }
}

/**
 * A failure as the TV shows it: an icon, [title], the kind's one sentence (never the home's own
 * text) and the actions that fit it in a row, the first focused so OK does what most helps.
 * The row follows the layout's direction (right to left in Arabic).
 */
@Composable
fun TvErrorState(
    failure: Failure,
    title: String,
    video: Boolean = false,
    onRetry: (() -> Unit)? = null,
    onSignIn: (() -> Unit)? = null,
    onChooseHome: (() -> Unit)? = null,
    onBack: (() -> Unit)? = null,
) {
    val actions = errorActions(failure.kind, onRetry != null, onSignIn != null, onChooseHome != null, onBack != null)
    val first = remember { FocusRequester() }
    LaunchedEffect(failure) { runCatching { first.requestFocus() } }
    Centered {
        Column(
            Modifier.widthIn(max = 720.dp).testTag("error-state"),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Icon(failure.kind.icon, contentDescription = null, tint = MaktabaPalette.Gold, modifier = Modifier.size(56.dp))
            Text(title, style = MaterialTheme.typography.headlineSmall, color = MaktabaPalette.Text, textAlign = TextAlign.Center)
            Text(
                stringResource(failure.kind.sentence(video)),
                style = MaterialTheme.typography.titleMedium,
                color = MaktabaPalette.Muted,
                textAlign = TextAlign.Center,
            )
            Row(Modifier.padding(top = 8.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                actions.forEachIndexed { i, a ->
                    val run: () -> Unit = when (a) {
                        ErrorAction.TryAgain -> onRetry
                        ErrorAction.SignIn -> onSignIn
                        ErrorAction.OtherHome -> onChooseHome
                        ErrorAction.Back -> onBack
                    } ?: {}
                    MaktabaButton(
                        onClick = run,
                        modifier = (if (i == 0) Modifier.focusRequester(first) else Modifier).testTag("error-action-${a.name}"),
                    ) { Text(stringResource(a.label())) }
                }
            }
        }
    }
}

@Composable
fun Centered(content: @Composable androidx.compose.foundation.layout.ColumnScope.() -> Unit) {
    Box(Modifier.fillMaxSize().padding(Gutter), contentAlignment = Alignment.Center) {
        ReadingPanel(contentPadding = 32.dp, content = content)
    }
}

/** A length as people read it: 1:02:03, or 4:05 under an hour. */
fun clock(sec: Double): String {
    val s = sec.toLong().coerceAtLeast(0)
    val h = s / 3600
    val m = (s % 3600) / 60
    val r = s % 60
    return if (h > 0) "%d:%02d:%02d".format(h, m, r) else "%d:%02d".format(m, r)
}

