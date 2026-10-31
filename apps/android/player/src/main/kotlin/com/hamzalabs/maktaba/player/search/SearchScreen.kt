package com.hamzalabs.maktaba.player.search

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.shell.PlaceholderScreen
import com.hamzalabs.maktaba.player.shell.PlayerNavigator

// A placeholder until story 48.29 merges: it replaces this file (keeping these signatures, which
// shell/AppScreens.kt calls) and turns its section on in PlayerSection.enabled.

/** Search (FR-19, FR-20). */
@Composable
fun SearchScreen(nav: PlayerNavigator) = PlaceholderScreen(stringResource(R.string.section_search), nav)
