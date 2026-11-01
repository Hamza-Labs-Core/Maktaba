package com.hamzalabs.maktaba.player.profile

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.shell.PlaceholderScreen
import com.hamzalabs.maktaba.player.shell.PlayerNavigator

// A placeholder until story 48.31 merges: it replaces this file (keeping these signatures, which
// shell/AppScreens.kt calls) and turns its section on in PlayerSection.enabled.

/** Profile and Settings (FR-25 to FR-31). */
@Composable
fun ProfileScreen(nav: PlayerNavigator) = PlaceholderScreen(stringResource(R.string.section_profile), nav)
