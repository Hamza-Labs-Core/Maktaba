package com.hamzalabs.maktaba.player.libraries

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlaceholderScreen
import com.hamzalabs.maktaba.player.shell.PlayerNavigator

// Until story 48.27 merges, the Libraries section is today's browser (LibraryBrowser.kt): the
// libraries, their videos, a video's page and the player, in every layout of the window. 48.27
// replaces this file and LibraryBrowser.kt, keeping these signatures (shell/AppScreens.kt calls them).

/** The Libraries section's root. */
@Composable
fun LibrariesScreen(nav: PlayerNavigator) {
    val home = LocalPlayerHome.current
    LibraryBrowser(home.session, home.user, home.client, home.themes, onPlaying = home.onPlaying)
}

/** A library's videos or series (FR-15). */
@Composable
fun LibraryScreen(id: String, nav: PlayerNavigator) = PlaceholderScreen(stringResource(R.string.libraries), nav)

/** A series' page (FR-17). */
@Composable
fun SeriesScreen(id: String, nav: PlayerNavigator) = PlaceholderScreen(stringResource(R.string.libraries), nav)
