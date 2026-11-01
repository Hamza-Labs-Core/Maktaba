package com.hamzalabs.maktaba.player.watch

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.shell.PlaceholderScreen
import com.hamzalabs.maktaba.player.shell.PlayerNavigator

// A placeholder until story 48.32 merges: it replaces this file (keeping these signatures, which
// shell/AppScreens.kt calls) and turns its section on in PlayerSection.enabled.

/** The player opened as a route: [videoId] from [startSec] (null: where the person left it). */
@Composable
fun WatchRouteScreen(videoId: String, startSec: Int?, nav: PlayerNavigator) =
    PlaceholderScreen(stringResource(R.string.shell_video), nav)
