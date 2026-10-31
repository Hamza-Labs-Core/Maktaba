package com.hamzalabs.maktaba.player.video

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.shell.PlaceholderScreen
import com.hamzalabs.maktaba.player.shell.PlayerNavigator

// A placeholder until story 48.28 merges: it replaces this file (keeping these signatures, which
// shell/AppScreens.kt calls) and turns its section on in PlayerSection.enabled.

/** A video's page (FR-18). */
@Composable
fun VideoScreen(id: String, nav: PlayerNavigator) = PlaceholderScreen(stringResource(R.string.shell_video), nav)
