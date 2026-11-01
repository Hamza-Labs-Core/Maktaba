package com.hamzalabs.maktaba.player.libraries

import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.staticCompositionLocalOf
import com.hamzalabs.maktaba.player.shell.PlayerNavigator
import com.hamzalabs.maktaba.player.shell.Route

/** Opens the video with this id; provided by [VideoHost] around each screen of the section. */
val LocalOpenVideo = staticCompositionLocalOf<(String) -> Unit> { {} }

/** Gives [content] a way to open a video: its page (48.28), pushed on this section's stack. */
@Composable
internal fun VideoHost(nav: PlayerNavigator, content: @Composable () -> Unit) {
    CompositionLocalProvider(LocalOpenVideo provides { id -> nav.open(Route.Video(id)) }, content = content)
}
