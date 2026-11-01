package com.hamzalabs.maktaba.player.live

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.shell.PlaceholderScreen
import com.hamzalabs.maktaba.player.shell.PlayerNavigator

// A placeholder until story 48.30 merges: it replaces this file (keeping these signatures, which
// shell/AppScreens.kt calls) and turns its section on in PlayerSection.enabled.

/** Live TV: what is on now and the guide (FR-21, FR-22). */
@Composable
fun LiveTvScreen(nav: PlayerNavigator) = PlaceholderScreen(stringResource(R.string.section_live_tv), nav)

/** A channel's live player (FR-23), by the channel's id. */
@Composable
fun ChannelScreen(id: String, nav: PlayerNavigator) = PlaceholderScreen(stringResource(R.string.section_live_tv), nav)
