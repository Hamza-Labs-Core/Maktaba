package com.hamzalabs.maktaba.core.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.compat.CompatVerdict
import com.hamzalabs.maktaba.core.session.AppSession

/** One line of a compatibility page: a string and its arguments. */
data class CompatLine(val res: Int, val args: List<String> = emptyList())

/** What a compatibility page says (story 58.01): its title and its lines, in order. */
data class CompatText(val title: Int, val lines: List<CompatLine>)

/**
 * The words of [block]'s page. Update the home names the home and its version, and speaks to its
 * admin (Admin → Updates) or anyone else (ask the admin); before anyone signed in here it says
 * both. Update this app says how, in the words of this app's platform.
 */
fun compatText(block: AppSession.CompatBlock): CompatText = when (block.verdict) {
    CompatVerdict.UpdateHome -> CompatText(
        R.string.compat_update_home_title,
        buildList {
            block.homeVersion?.takeIf { it.isNotBlank() }?.let { add(CompatLine(R.string.compat_home_version, listOf(block.homeName, it))) }
            if (block.admin != false) add(CompatLine(R.string.compat_update_home_admin, listOf(block.homeName)))
            if (block.admin != true) add(CompatLine(R.string.compat_update_home_viewer, listOf(block.homeName)))
        },
    )
    else -> CompatText(
        R.string.compat_update_app_title,
        listOf(
            CompatLine(R.string.compat_update_app_why, listOf(block.homeName)),
            CompatLine(updateAppHow(block.platform)),
        ),
    )
}

/** How to update this app on [platform], as `X-Maktaba-Client` names it. */
fun updateAppHow(platform: String): Int = when (platform) {
    "firetv" -> R.string.compat_update_app_firetv
    "androidtv" -> R.string.compat_update_app_androidtv
    else -> R.string.compat_update_app_android
}

/**
 * The full-screen page shown instead of signing in when this app and the home are too far
 * apart (phones, foldables, tablets): what to do, Try again once it is done, or another home.
 */
@Composable
fun CompatPage(block: AppSession.CompatBlock, onTryAgain: () -> Unit, onOtherHome: () -> Unit) {
    val text = compatText(block)
    StartBackdrop {
        Box(Modifier.fillMaxSize().safeDrawingPadding().verticalScroll(rememberScrollState()), contentAlignment = Alignment.Center) {
            Column(
                Modifier.widthIn(max = 520.dp).fillMaxWidth().padding(horizontal = 20.dp, vertical = 28.dp),
                verticalArrangement = Arrangement.spacedBy(20.dp),
            ) {
                StartPanel(Modifier.testTag("compat-page")) {
                    Text(
                        stringResource(text.title),
                        style = MaterialTheme.typography.headlineSmall,
                        color = MaterialTheme.colorScheme.onSurface,
                        modifier = Modifier.semantics { heading() }.testTag("compat-title"),
                    )
                    text.lines.forEachIndexed { i, line ->
                        Text(
                            stringResource(line.res, *line.args.toTypedArray()),
                            style = MaterialTheme.typography.bodyLarge,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.testTag("compat-line-$i"),
                        )
                    }
                    Button(onClick = onTryAgain, modifier = Modifier.fillMaxWidth().testTag("compat-try-again")) {
                        Text(stringResource(R.string.progress_retry))
                    }
                    TextButton(onClick = onOtherHome, modifier = Modifier.align(Alignment.CenterHorizontally).testTag("compat-other-home")) {
                        Text(stringResource(R.string.other_home))
                    }
                }
            }
        }
    }
}
