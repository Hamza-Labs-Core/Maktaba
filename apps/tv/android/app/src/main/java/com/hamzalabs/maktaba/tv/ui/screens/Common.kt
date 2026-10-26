package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
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
val Gutter = 56.dp

/** What a screen loads: loading, the value, or why it could not. */
sealed interface Load<out T> {
    data object Loading : Load<Nothing>
    data class Ready<T>(val value: T) : Load<T>
    data class Failed(val why: String) : Load<Nothing>
}

/**
 * Loads with [load] whenever [key] changes (or a person presses Try again). A signed-out answer
 * sends the app back to signing in ([signedOut]).
 */
@Composable
fun <T> rememberLoad(key: Any?, signedOut: () -> Unit, load: suspend () -> T): Pair<Load<T>, () -> Unit> {
    var state by remember(key) { mutableStateOf<Load<T>>(Load.Loading) }
    var attempt by remember(key) { mutableStateOf(0) }
    LaunchedEffect(key, attempt) {
        state = Load.Loading
        state = try {
            Load.Ready(load())
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException.SignedOut) {
            signedOut()
            Load.Failed(e.message ?: "")
        } catch (e: Exception) {
            Load.Failed(e.message ?: e.toString())
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
        is Load.Failed -> Centered {
            Text(stringResource(R.string.could_not_load, state.why), style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Text)
            Box(Modifier.padding(top = 16.dp)) { MaktabaButton(onClick = retry) { Text(stringResource(R.string.try_again)) } }
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

