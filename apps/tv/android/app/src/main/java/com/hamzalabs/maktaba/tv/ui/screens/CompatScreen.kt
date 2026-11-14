package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.ui.compatText
import com.hamzalabs.maktaba.tv.ui.components.LibraryBackdrop
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.ReadingPanel
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * Update the home or Update this app on the TV (story 58.01), instead of signing in: what to do,
 * then Try again (focused first) or another home. The words are the phones' ([compatText]).
 */
@Composable
fun CompatScreen(block: AppSession.CompatBlock, onTryAgain: () -> Unit, onOtherHome: () -> Unit) {
    val text = compatText(block)
    val focus = remember { FocusRequester() }
    LaunchedEffect(block) { runCatching { focus.requestFocus() } }
    LibraryBackdrop {
        Box(Modifier.fillMaxSize().padding(horizontal = Gutter, vertical = 48.dp), contentAlignment = Alignment.Center) {
            ReadingPanel(Modifier.widthIn(max = 880.dp).testTag("compat-page")) {
                Column(verticalArrangement = Arrangement.spacedBy(16.dp)) {
                    Text(
                        stringResource(text.title),
                        style = MaterialTheme.typography.headlineMedium,
                        color = MaktabaPalette.Text,
                        modifier = Modifier.semantics { heading() }.testTag("compat-title"),
                    )
                    text.lines.forEachIndexed { i, line ->
                        Text(
                            stringResource(line.res, *line.args.toTypedArray()),
                            style = MaterialTheme.typography.bodyLarge,
                            color = MaktabaPalette.Muted,
                            modifier = Modifier.testTag("compat-line-$i"),
                        )
                    }
                    Row(Modifier.padding(top = 8.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                        MaktabaButton(onClick = onTryAgain, modifier = Modifier.focusRequester(focus).testTag("compat-try-again")) {
                            Text(stringResource(CoreR.string.progress_retry))
                        }
                        MaktabaButton(onClick = onOtherHome, modifier = Modifier.testTag("compat-other-home")) {
                            Text(stringResource(CoreR.string.other_home))
                        }
                    }
                }
            }
        }
    }
}
