package com.hamzalabs.maktaba.player.live

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.hamzalabs.maktaba.player.shell.PlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerSection

/**
 * FR-5: [enabled] with Live TV only when the user can see a channel ([hasChannels] true). Until
 * the home has said (null), and when it answered none, the section is not offered: a section
 * that only ever says "no channels" is no choice.
 */
fun liveSections(enabled: List<PlayerSection>, hasChannels: Boolean?): List<PlayerSection> =
    if (hasChannels == true) enabled else enabled.filter { it != PlayerSection.LiveTv }.ifEmpty { enabled }

/**
 * The sections the shell offers for [home]: [enabled], with Live TV hidden while the user can
 * see no channel. Asks `GET /api/channels/now` each time the app comes to the front (one request,
 * never a poll in the background, NFR-16); Live TV's own refreshes update it too.
 */
@Composable
fun rememberShownSections(home: PlayerHome, enabled: List<PlayerSection> = PlayerSection.enabled): List<PlayerSection> {
    val live = home.liveTv()
    if (PlayerSection.LiveTv in enabled) {
        val env = LocalLiveEnv.current
        val lifecycle = LocalLifecycleOwner.current.lifecycle
        LaunchedEffect(live, lifecycle) {
            lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) { live.loadNow(env.nowMs()) }
        }
    }
    return liveSections(enabled, live.hasChannels)
}
