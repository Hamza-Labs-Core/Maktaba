package com.hamzalabs.maktaba.player.home

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.shell.PlaceholderScreen
import com.hamzalabs.maktaba.player.shell.PlayerNavigator

// A placeholder until story 48.26 merges: it replaces this file (keeping these signatures, which
// shell/AppScreens.kt calls) and turns its section on in PlayerSection.enabled.

/** Home: the hero and the rows (FR-10 to FR-13). */
@Composable
fun HomeScreen(nav: PlayerNavigator) = PlaceholderScreen(stringResource(R.string.section_home), nav)
